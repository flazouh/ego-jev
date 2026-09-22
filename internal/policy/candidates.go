package policy

import (
	"fmt"
	"regexp"
	"strings"
)

var risky = regexp.MustCompile(`(?i)\b(delete|remove|trash|erase|pay|payment|purchase|buy|order|checkout|check out|send|post|publish|tweet|reply|submit|confirm|transfer|withdraw|unsubscribe|sign out|log ?out|deactivate|install|share|invite|approve|merge)\b`)

// Candidates holds one ordered pool of targets per targeted operation, plus the risky controls held for the user.
type Candidates struct {
	Pools map[Op]*Pool
	Held  []Target
}

// Pool maps an option key sent to Jev to the target it stands for, in insertion order.
type Pool struct {
	Descriptions Ordered
	targets      map[string]Target
}

func (p *Pool) add(key, description string, t Target) {
	p.Descriptions.Set(key, description)
	if p.targets == nil {
		p.targets = map[string]Target{}
	}
	p.targets[key] = t
}

func (p *Pool) Target(key string) (Target, bool) {
	t, ok := p.targets[key]
	return t, ok
}

func describe(el Element) string {
	parts := []string{fmt.Sprintf("%s: %s", el.Role, el.Label)}
	if el.Context != "" {
		parts = append(parts, fmt.Sprintf("in %q", el.Context))
	}
	if el.Value != "" {
		parts = append(parts, fmt.Sprintf("current value %q", el.Value))
	}
	for _, kv := range [][2]string{{"checked", el.Checked}, {"selected", el.Selected}, {"expanded", el.Expanded}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	if el.Href != "" {
		parts = append(parts, "goes to "+el.Href)
	}
	return truncate(strings.Join(parts, ", "), 200)
}

func describeTarget(t Target) string {
	return describe(Element{Role: t.Role, Label: t.Label, Context: t.Context})
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func isRisky(el Element, allow []*regexp.Regexp) bool {
	text := el.Label + " " + el.Href
	if !risky.MatchString(text) {
		return false
	}
	for _, re := range allow {
		if re.MatchString(text) {
			return false
		}
	}
	return true
}

func targetOf(el Element) Target {
	return Target{ID: el.ID, Role: el.Role, Label: el.Label, Context: el.Context}
}

// BuildCandidates sorts every observed element into the pools Jev may choose from.
// Risky controls never enter a pool; they are listed in Held for the user.
func BuildCandidates(o Observation, allowRisky []*regexp.Regexp) Candidates {
	c := Candidates{Pools: map[Op]*Pool{Click: {}, Type: {}, Select: {}}}
	for _, el := range o.Elements {
		switch {
		case isRisky(el, allowRisky):
			c.Held = append(c.Held, targetOf(el))
		case len(el.Options) > 0:
			for _, opt := range el.Options {
				if opt.Selected {
					continue
				}
				t := targetOf(el)
				option := opt
				t.Option = &option
				c.Pools[Select].add(el.ID+"="+opt.Value, el.Label+" → "+opt.Label, t)
			}
		default:
			c.Pools[Click].add(el.ID, describe(el), targetOf(el))
			if el.Editable {
				c.Pools[Type].add(el.ID, describe(el), targetOf(el))
			}
		}
	}
	return c
}
