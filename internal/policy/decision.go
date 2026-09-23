package policy

import (
	"fmt"
	"math"
	"sort"
)

const (
	// DONE is accepted with a strong DONE vote and a leaning-yes goal_met, or with a very sure goal_met alone.
	doneMinProbability = 0.8
	goalMetMin         = 0.5
	goalMetSure        = 0.9
	// A yes on needs_held alone stops the run for review, whatever the operation vote says.
	needsHeldMin = 0.5
	// Risky controls never reach a click or select head, so a split vote between two remaining targets is two safe moves.
	safeTargetMinProbability = 0.4
)

// Response is the body TypeSafe returns.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   map[string]int    `json:"usage,omitempty"`
}

type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
}

// Rank is one choice answer with the numbers the gates read.
type Rank struct {
	Choice      string
	Probability float64
	Margin      float64
	Top         []Ranked
}

type Ranked struct {
	Key         string  `json:"key"`
	Probability float64 `json:"probability"`
}

// Decision is what the runner does next. Sure is false when a gate failed; Why says which.
type Decision struct {
	Op         Op
	Target     *Target
	Operation  Rank
	TargetRank *Rank
	Sure       bool
	Why        string
}

func rank(resp Response, name string, q Question) (Rank, error) {
	a, ok := resp.Answers[name]
	if !ok {
		return Rank{}, fmt.Errorf("jev did not answer %q", name)
	}
	if a.Type != "choice" || q.Choices == nil {
		return Rank{}, fmt.Errorf("jev answered %q as %q, not as a choice", name, a.Type)
	}
	if _, offered := q.Choices.Get(a.Choice); !offered {
		return Rank{}, fmt.Errorf("jev picked %q for %q, which was not offered", a.Choice, name)
	}
	top := make([]Ranked, 0, len(a.Probabilities))
	for k, p := range a.Probabilities {
		top = append(top, Ranked{k, p})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Probability == top[j].Probability {
			return top[i].Key < top[j].Key
		}
		return top[i].Probability > top[j].Probability
	})
	p := a.Probabilities[a.Choice]
	runnerUp := 0.0
	for _, r := range top {
		if r.Key != a.Choice {
			runnerUp = r.Probability
			break
		}
	}
	return Rank{Choice: a.Choice, Probability: p, Margin: p - runnerUp, Top: top[:min(3, len(top))]}, nil
}

func (g Gates) clear(r Rank) bool {
	return r.Probability >= g.MinProbability && r.Margin >= g.MinMargin
}

func (r Rank) String() string { return fmt.Sprintf("p=%.2f, margin=%.2f", r.Probability, r.Margin) }

// noul returns the yes probability, or NaN when the question was not answered. NaN fails every threshold.
func noul(resp Response, name string) float64 {
	a, ok := resp.Answers[name]
	if !ok || a.Noul == nil {
		return math.NaN()
	}
	return *a.Noul
}

// Read validates Jev's answers against what the prompt offered and applies the gates.
func (p Prompt) Read(resp Response, g Gates) (Decision, error) {
	if _, asked := p.Request.Questions["needs_held"]; asked {
		if n := noul(resp, "needs_held"); n >= needsHeldMin {
			return Decision{Op: Review, Sure: true, Operation: Rank{Choice: string(Review), Probability: n, Margin: n - needsHeldMin, Top: []Ranked{{string(Review), n}}}}, nil
		}
	}
	operation, err := rank(resp, "operation", p.Request.Questions["operation"])
	if err != nil {
		return Decision{}, err
	}
	d := Decision{Op: Op(operation.Choice), Operation: operation}
	for _, t := range targeted {
		if t.Op != d.Op {
			continue
		}
		tr, err := rank(resp, t.Head, p.Request.Questions[t.Head])
		if err != nil {
			return Decision{}, err
		}
		target, _ := p.Candidates.Pools[t.Op].Target(tr.Choice)
		d.Target, d.TargetRank = &target, &tr
		ok := g.clear(tr)
		if t.Safe {
			ok = tr.Probability >= safeTargetMinProbability
		}
		if !ok {
			d.Why = fmt.Sprintf("target %s at %s", target.Label, tr)
		}
	}
	if d.Op == Done {
		// DONE takes no action, so its own two checks replace the operation gate.
		met := noul(resp, "goal_met")
		if !(operation.Probability >= doneMinProbability && met >= goalMetMin) && !(met >= goalMetSure) {
			d.Why = fmt.Sprintf("DONE not confirmed: p=%.2f, goal_met=%.2f", operation.Probability, met)
		}
	} else if !g.clear(operation) {
		d.Why = fmt.Sprintf("operation %s at %s", d.Op, operation)
	}
	d.Sure = d.Why == ""
	return d, nil
}
