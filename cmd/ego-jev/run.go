package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/flazouh/ego-jev/internal/browser"
	"github.com/flazouh/ego-jev/internal/config"
	"github.com/flazouh/ego-jev/internal/policy"
	"github.com/flazouh/ego-jev/internal/runner"
	"github.com/flazouh/ego-jev/internal/textgen"
	"github.com/flazouh/ego-jev/internal/typesafe"
)

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ", ") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

type runFlags struct {
	opts                      runner.Options
	space                     int
	name, page, url, envFile  string
	textModel, binary         string
	values, allowRisky        repeated
	asJSON, closeSpace, quiet bool
	noTextModel               bool
}

func parseRun(args []string, stderr io.Writer) (runFlags, string, error) {
	f := runFlags{opts: runner.DefaultOptions()}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&f.space, "space", 0, "resume the ego task space with this id")
	fs.StringVar(&f.name, "name", "ego-jev", "without --space, resume the task space with this name, or create it")
	fs.StringVar(&f.page, "page", "p1", "page label inside the task space")
	fs.StringVar(&f.url, "url", "", "navigate to this URL before the first step")
	fs.IntVar(&f.opts.MaxSteps, "max-steps", f.opts.MaxSteps, "stop after this many actions")
	fs.Float64Var(&f.opts.Gates.MinProbability, "min-p", f.opts.Gates.MinProbability, "lowest probability an operation or field vote may have")
	fs.Float64Var(&f.opts.Gates.MinMargin, "min-margin", f.opts.Gates.MinMargin, "lowest lead over the runner-up an operation or field vote may have")
	fs.Var(&f.values, "value", `text for a field, as "label substring=text" (repeatable)`)
	fs.Var(&f.allowRisky, "allow-risky", "case-insensitive regexp of a held control Jev may press (repeatable)")
	fs.BoolVar(&f.asJSON, "json", false, "print the full result as JSON")
	fs.BoolVar(&f.closeSpace, "close", false, "finish the task space when the run ends")
	fs.BoolVar(&f.quiet, "quiet", false, "do not print each step")
	fs.StringVar(&f.envFile, "env-file", config.DefaultEnvFile(), "dotenv file with API keys")
	fs.StringVar(&f.opts.Model, "model", f.opts.Model, "TypeSafe model")
	fs.StringVar(&f.textModel, "text-model", textgen.DefaultModel, "OpenRouter model that writes field text")
	fs.BoolVar(&f.noTextModel, "no-text-model", false, "never call a text model; stop with needs_text instead")
	fs.StringVar(&f.binary, "ego-browser", "ego-browser", "ego-browser executable")
	if err := fs.Parse(args); err != nil {
		return f, "", err
	}
	goal := strings.TrimSpace(strings.Join(fs.Args(), " "))
	switch {
	case goal == "":
		return f, "", errors.New(`missing goal: ego-jev run [flags] "goal"`)
	case f.opts.MaxSteps < 1:
		return f, "", errors.New("--max-steps must be at least 1")
	case !unit(f.opts.Gates.MinProbability) || !unit(f.opts.Gates.MinMargin):
		return f, "", errors.New("--min-p and --min-margin must be between 0 and 1")
	}
	for _, v := range f.values {
		label, text, ok := strings.Cut(v, "=")
		if !ok || label == "" {
			return f, "", fmt.Errorf(`--value %q: want "label substring=text"`, v)
		}
		f.opts.Values[label] = text
	}
	for _, pattern := range f.allowRisky {
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return f, "", fmt.Errorf("--allow-risky %q: %w", pattern, err)
		}
		f.opts.AllowRisky = append(f.opts.AllowRisky, re)
	}
	return f, goal, nil
}

func unit(v float64) bool { return v >= 0 && v <= 1 }

// output is what a run prints: the runner result plus where to resume.
type output struct {
	runner.Result
	SpaceID int    `json:"spaceId,omitempty"`
	Page    string `json:"page,omitempty"`
	Closed  bool   `json:"closed"`
}

func runCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, goal, err := parseRun(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitDone
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	out := execute(ctx, f, goal, stderr)
	if f.asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	} else {
		fmt.Fprintln(stdout, summary(out))
	}
	switch out.Status {
	case runner.Done:
		return exitDone
	case runner.Failed:
		return exitError
	}
	return exitHandoff
}

// execute runs the goal and always returns a printable result, including for setup failures.
func execute(ctx context.Context, f runFlags, goal string, stderr io.Writer) output {
	failed := func(err error) output {
		return output{Result: runner.Result{Status: runner.Failed, Reason: err.Error(), History: []policy.Action{}, Trace: []runner.Step{}}}
	}
	keys, err := config.Load(f.envFile)
	if err != nil {
		return failed(err)
	}
	apiKey := keys.Get("TYPESAFE_API_KEY")
	if apiKey == "" {
		return failed(fmt.Errorf("TYPESAFE_API_KEY is missing: set it or add it to %s", f.envFile))
	}
	if key := keys.Get("OPENROUTER_API_KEY"); key != "" && !f.noTextModel {
		t := textgen.NewOpenRouter(key)
		t.Model = f.textModel
		f.opts.Text = t
	}
	if !f.quiet {
		f.opts.OnStep = func(s runner.Step) { fmt.Fprintln(stderr, formatStep(s)) }
	}

	page, err := browser.Start(ctx, browser.Options{Space: f.space, Name: f.name, Page: f.page, Binary: f.binary})
	if err != nil {
		return failed(err)
	}
	var res runner.Result
	if f.url != "" {
		if err := page.Goto(f.url); err != nil {
			res = failed(err).Result
		}
	}
	if res.Status == "" {
		res = runner.Run(ctx, page, typesafe.New(apiKey), goal, f.opts)
	}
	out := output{Result: res, SpaceID: page.Info.SpaceID, Page: page.Info.Page}
	if err := page.Close(f.closeSpace); err != nil {
		fmt.Fprintln(stderr, "closing the bridge:", err)
	} else {
		out.Closed = f.closeSpace
	}
	return out
}

func formatStep(s runner.Step) string {
	line := fmt.Sprintf("step %-2d %-11s p=%.2f", s.Step, s.Op, s.P)
	if s.Target != "" {
		line += fmt.Sprintf("  %q", s.Target)
		if s.TargetP != nil {
			line += fmt.Sprintf(" p=%.2f", *s.TargetP)
		}
	}
	if s.Text != "" {
		line += fmt.Sprintf("  text=%q from %s", s.Text, s.TextFrom)
	}
	line += fmt.Sprintf("  jev %dms", s.JevMs)
	if s.ActMs > 0 {
		line += fmt.Sprintf("  act %dms", s.ActMs)
	}
	return line
}

func summary(o output) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s after %d steps in %.1fs", o.Status, o.Steps, float64(o.Ms)/1000)
	if o.Reason != "" {
		fmt.Fprintf(&b, ": %s", o.Reason)
	}
	if o.URL != "" {
		fmt.Fprintf(&b, "\npage: %s (%s)", o.URL, o.Title)
	}
	for _, h := range o.Held {
		fmt.Fprintf(&b, "\nheld for you: %s %q  selector %s", h.Role, h.Label, h.Selector)
	}
	if o.Field != nil {
		fmt.Fprintf(&b, "\nfield needing text: %s %q (pass --value %q)", o.Field.Role, o.Field.Label, o.Field.Label+"=...")
	}
	if o.SpaceID != 0 && !o.Closed {
		fmt.Fprintf(&b, "\ntask space %d, page %s is still open", o.SpaceID, o.Page)
	}
	return b.String()
}
