// Package runner runs a goal step by step: observe, ask Jev, act, repeat, and stop for the caller when needed.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/flazouh/ego-jev/internal/policy"
)

// Status is how a run ended. Everything except Done hands control back to the caller at the current page.
type Status string

const (
	Done      Status = "done"
	Review    Status = "review"
	Unsure    Status = "unsure"
	Blocked   Status = "blocked"
	Stuck     Status = "stuck"
	NeedsText Status = "needs_text"
	MaxSteps  Status = "max_steps"
	Failed    Status = "error"
)

// Page is the browser side the runner needs.
type Page interface {
	Observe(maxElements, maxText int) (policy.Observation, error)
	Perform(op policy.Op, target *policy.Target, text string, viewport policy.Viewport) (string, error)
	MarkHeld(ids []string) ([]string, error)
}

// Chooser sends one request to Jev.
type Chooser interface {
	Choose(ctx context.Context, req policy.Request) (policy.Response, error)
}

// TextSource writes a field value when no other source has one. It returns false when the goal does not say.
type TextSource interface {
	Text(ctx context.Context, goal string, field policy.Target, page policy.Observation) (string, bool, error)
}

type Options struct {
	Model       string
	MaxSteps    int
	Gates       policy.Gates
	AllowRisky  []*regexp.Regexp
	Values      map[string]string
	MaxElements int
	MaxText     int
	// Text is optional. Without it, a field with no known value ends the run with needs_text.
	Text   TextSource
	OnStep func(Step)
}

// DefaultOptions is the starting point for every run. Callers change fields on it rather than building Options from zero.
func DefaultOptions() Options {
	return Options{
		Model:       "jev-latest",
		MaxSteps:    25,
		Gates:       policy.Gates{MinProbability: 0.5, MinMargin: 0.1},
		MaxElements: 200,
		MaxText:     3000,
		Values:      map[string]string{},
	}
}

// Step is one row of the trace.
type Step struct {
	Step      int       `json:"step"`
	Op        policy.Op `json:"op"`
	Target    string    `json:"target,omitempty"`
	P         float64   `json:"p"`
	TargetP   *float64  `json:"targetP,omitempty"`
	Text      string    `json:"text,omitempty"`
	TextFrom  string    `json:"textFrom,omitempty"`
	Via       string    `json:"via,omitempty"`
	ObserveMs int64     `json:"observeMs"`
	JevMs     int64     `json:"jevMs"`
	ActMs     int64     `json:"actMs,omitempty"`
}

type Held struct {
	policy.Target
	Selector string `json:"selector"`
}

// Votes are the leading options of an unsure decision.
type Votes struct {
	Operation []policy.Ranked `json:"operation"`
	Target    []policy.Ranked `json:"target,omitempty"`
}

type Result struct {
	Status  Status          `json:"status"`
	Reason  string          `json:"reason,omitempty"`
	Steps   int             `json:"steps"`
	Ms      int64           `json:"ms"`
	URL     string          `json:"url,omitempty"`
	Title   string          `json:"title,omitempty"`
	Held    []Held          `json:"held,omitempty"`
	Field   *policy.Target  `json:"field,omitempty"`
	Top     *Votes          `json:"top,omitempty"`
	History []policy.Action `json:"history"`
	Trace   []Step          `json:"trace"`
}

type run struct {
	page    Page
	chooser Chooser
	goal    string
	opts    Options
	started time.Time
	history []policy.Action
	trace   []Step
}

func since(t time.Time) int64 { return time.Since(t).Milliseconds() }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func (r *run) finish(status Status, o policy.Observation, fill func(*Result)) Result {
	res := Result{Status: status, Steps: len(r.history), Ms: since(r.started), URL: o.URL, Title: o.Title, History: r.history, Trace: r.trace}
	if fill != nil {
		fill(&res)
	}
	return res
}

// Run drives the page toward the goal until a stop status. Start opts from DefaultOptions.
func Run(ctx context.Context, page Page, chooser Chooser, goal string, opts Options) Result {
	r := &run{page: page, chooser: chooser, goal: goal, opts: opts, started: time.Now(), history: []policy.Action{}, trace: []Step{}}
	var previous string
	var obs policy.Observation
	for step := 1; ; step++ {
		if err := ctx.Err(); err != nil {
			return r.finish(Failed, obs, withReason(err.Error()))
		}
		t0 := time.Now()
		var err error
		obs, err = page.Observe(opts.MaxElements, opts.MaxText)
		if err != nil {
			return r.finish(Failed, obs, withReason(err.Error()))
		}
		mark := fingerprint(obs)
		// Whether the last action changed anything is only known once the next observation exists.
		if n := len(r.history); n > 0 {
			last := &r.history[n-1]
			changed := mark != previous || last.Op == policy.Type || last.Op == policy.Select
			last.Changed = &changed
		}
		previous = mark
		if why := noProgress(r.history); why != "" {
			return r.finish(Stuck, obs, withReason(why))
		}
		if len(r.history) >= opts.MaxSteps {
			return r.finish(MaxSteps, obs, nil)
		}

		res, done := r.step(ctx, step, obs, since(t0))
		if done {
			return res
		}
	}
}

func withReason(why string) func(*Result) { return func(r *Result) { r.Reason = why } }

// step decides and performs one operation. It returns done=true with the final result when the run must stop.
func (r *run) step(ctx context.Context, n int, obs policy.Observation, observeMs int64) (Result, bool) {
	prompt := policy.BuildPrompt(obs, policy.BuildCandidates(obs, r.opts.AllowRisky), r.goal, r.history, r.opts.Model)
	row := Step{Step: n, ObserveMs: observeMs}
	t1 := time.Now()
	resp, err := r.chooser.Choose(ctx, prompt.Request)
	if err != nil {
		return r.finish(Failed, obs, withReason(err.Error())), true
	}
	d, err := prompt.Read(resp, r.opts.Gates)
	if err != nil {
		return r.finish(Failed, obs, withReason(err.Error())), true
	}
	row.JevMs, row.Op, row.P = since(t1), d.Op, round3(d.Operation.Probability)
	if d.Target != nil {
		row.Target = d.Target.Label
		p := round3(d.TargetRank.Probability)
		row.TargetP = &p
	}
	stop := func(status Status, fill func(*Result)) (Result, bool) {
		r.trace = append(r.trace, row)
		r.emit(row)
		return r.finish(status, obs, fill), true
	}

	switch {
	case !d.Sure:
		votes := &Votes{Operation: d.Operation.Top}
		if d.TargetRank != nil {
			votes.Target = d.TargetRank.Top
		}
		return stop(Unsure, func(res *Result) { res.Reason, res.Top = d.Why, votes })
	case d.Op == policy.Done:
		return stop(Done, nil)
	case d.Op == policy.Blocked:
		return stop(Blocked, nil)
	case d.Op == policy.Review:
		held, err := r.markHeld(prompt.Candidates.Held)
		if err != nil {
			return stop(Failed, withReason(err.Error()))
		}
		return stop(Review, func(res *Result) { res.Held = held })
	}

	var text string
	if d.Op == policy.Type {
		value, from, err := r.textFor(ctx, *d.Target, obs)
		if err != nil {
			return stop(Failed, withReason(err.Error()))
		}
		row.Text, row.TextFrom, text = value, from, value
		if from == "" {
			field := *d.Target
			return stop(NeedsText, func(res *Result) { res.Field = &field })
		}
	}

	t2 := time.Now()
	via, err := r.page.Perform(d.Op, d.Target, text, obs.Viewport)
	if err != nil {
		return stop(Failed, withReason(err.Error()))
	}
	row.Via, row.ActMs = via, since(t2)
	r.trace = append(r.trace, row)
	r.emit(row)
	action := policy.Action{Op: d.Op, Text: text}
	if d.Target != nil {
		action.Label, action.Target = d.Target.Label, d.Target.ID
		if d.Target.Option != nil {
			action.Label += " → " + d.Target.Option.Label
		}
	}
	r.history = append(r.history, action)
	return Result{}, false
}

func (r *run) emit(s Step) {
	if r.opts.OnStep != nil {
		r.opts.OnStep(s)
	}
}

func (r *run) markHeld(held []policy.Target) ([]Held, error) {
	ids := make([]string, len(held))
	for i, h := range held {
		ids[i] = h.ID
	}
	selectors, err := r.page.MarkHeld(ids)
	if err != nil {
		return nil, err
	}
	if len(selectors) != len(held) {
		return nil, errors.New("markHeld returned the wrong number of selectors")
	}
	out := make([]Held, len(held))
	for i, h := range held {
		out[i] = Held{Target: h, Selector: selectors[i]}
	}
	return out, nil
}

// textFor finds what to type: a caller value, then a quoted phrase from the goal picked by Jev, then the text source.
// An empty source means no value was found.
func (r *run) textFor(ctx context.Context, field policy.Target, obs policy.Observation) (string, string, error) {
	for _, key := range slices.Sorted(maps.Keys(r.opts.Values)) {
		if strings.Contains(strings.ToLower(field.Label), strings.ToLower(key)) {
			return r.opts.Values[key], "values", nil
		}
	}
	if spans := policy.QuotedSpans(r.goal); len(spans) > 0 {
		prompt := policy.BuildValuePrompt(r.goal, field, spans, r.history, r.opts.Model)
		resp, err := r.chooser.Choose(ctx, prompt.Request)
		if err != nil {
			return "", "", err
		}
		if value, ok, err := prompt.Read(resp, r.opts.Gates); err != nil {
			return "", "", err
		} else if ok {
			return value, "goal", nil
		}
	}
	if r.opts.Text == nil {
		return "", "", nil
	}
	value, ok, err := r.opts.Text.Text(ctx, r.goal, field, obs)
	if err != nil || !ok {
		return "", "", err
	}
	return value, "text-model", nil
}

func fingerprint(o policy.Observation) string {
	type el struct{ L, V, C, E, S string }
	els := make([]el, len(o.Elements))
	for i, e := range o.Elements {
		els[i] = el{e.Label, e.Value, e.Checked, e.Expanded, e.Selected}
	}
	b, _ := json.Marshal([]any{o.URL, o.Title, o.Text, els})
	return string(b)
}

func noProgress(history []policy.Action) string {
	unchanged := func(a policy.Action) bool { return a.Changed != nil && !*a.Changed && a.Op != policy.Wait }
	if n := len(history); n >= 3 && unchanged(history[n-1]) && unchanged(history[n-2]) && unchanged(history[n-3]) {
		return "three steps with no visible change"
	}
	if n := len(history); n >= 5 {
		waits := 0
		for _, a := range history[n-5:] {
			if a.Op == policy.Wait {
				waits++
			}
		}
		if waits == 5 {
			return "five waits in a row"
		}
	}
	return ""
}
