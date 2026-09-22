// Package scripted is a stand-in for Jev that answers from a fixed script. Tests use it to exercise the loop and the
// browser bridge without the network.
package scripted

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/flazouh/ego-jev/internal/policy"
)

// Step is one scripted decision: an operation and, for targeted operations, a pattern that picks the target by description.
type Step struct {
	Op     policy.Op
	Target *regexp.Regexp
}

func S(op policy.Op) Step { return Step{Op: op} }

func T(op policy.Op, pattern string) Step { return Step{Op: op, Target: regexp.MustCompile(pattern)} }

// Chooser answers step requests in script order. A value request for a field whose description contains a key of
// Values is answered with that key's phrase; any other value request gets NONE.
type Chooser struct {
	Script []Step
	Values map[string]string

	mu sync.Mutex
	i  int
}

var heads = map[policy.Op]string{policy.Click: "click_target", policy.Type: "type_target", policy.Select: "select_target"}

func one(pick string, options *policy.Ordered) policy.Answer {
	probs := map[string]float64{}
	for _, k := range options.Keys() {
		probs[k] = 0
	}
	probs[pick] = 1
	return policy.Answer{Type: "choice", Choice: pick, Probabilities: probs, Confidence: 1}
}

func noul(yes bool) policy.Answer {
	v := 0.0
	if yes {
		v = 1
	}
	return policy.Answer{Type: "noul", Noul: &v}
}

func (c *Chooser) Choose(ctx context.Context, req policy.Request) (policy.Response, error) {
	if q, ok := req.Questions["value"]; ok {
		return c.value(req.State.(policy.ValueState), q.Choices), nil
	}
	c.mu.Lock()
	step := c.Script[min(c.i, len(c.Script)-1)]
	c.i++
	n := c.i
	c.mu.Unlock()

	answers := map[string]policy.Answer{
		"operation": one(string(step.Op), req.Questions["operation"].Choices),
		"goal_met":  noul(step.Op == policy.Done),
	}
	if _, ok := req.Questions["needs_held"]; ok {
		answers["needs_held"] = noul(step.Op == policy.Review)
	}
	if head, ok := heads[step.Op]; ok {
		options := req.Questions[head].Choices
		pick := ""
		for _, k := range options.Keys() {
			if v, _ := options.Get(k); step.Target.MatchString(v) {
				pick = k
				break
			}
		}
		if pick == "" {
			return policy.Response{}, fmt.Errorf("script step %d: no %s target matches %s", n, step.Op, step.Target)
		}
		answers[head] = one(pick, options)
	}
	return policy.Response{Answers: answers}, nil
}

func (c *Chooser) value(state policy.ValueState, options *policy.Ordered) policy.Response {
	pick := "NONE"
	for field, phrase := range c.Values {
		if !strings.Contains(state.Field, field) {
			continue
		}
		for _, k := range options.Keys() {
			if v, _ := options.Get(k); v == phrase {
				pick = k
			}
		}
	}
	return policy.Response{Answers: map[string]policy.Answer{"value": one(pick, options)}}
}
