package policy

import (
	"strings"
	"testing"
)

func TestDoneGate(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	prompt := BuildPrompt(shop(), c, "Open the article", nil, "m")
	cases := []struct {
		name string
		op   string
		p    float64
		met  float64
		sure bool
		done bool
	}{
		{"weak vote, goal_met very sure", "DONE", 0.49, 0.95, true, true},
		{"top vote, goal_met clears the bar", "DONE", 0.7, 0.61, true, true},
		{"top vote, goal_met below the bar", "DONE", 0.7, 0.55, false, true},
		{"strong vote, goal_met says no", "DONE", 0.95, 0.3, false, true},
		{"another operation wins even when goal_met is sure", "WAIT", 0.9, 0.99, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := Response{Answers: map[string]Answer{
				"operation":  choice(tc.op, map[string]float64{tc.op: tc.p, "CLICK": 1 - tc.p}),
				"goal_met":   yes(tc.met),
				"needs_held": yes(0),
			}}
			d, err := prompt.Read(resp, gates)
			if err != nil {
				t.Fatal(err)
			}
			if d.Sure != tc.sure || (d.Op == Done) != tc.done {
				t.Fatalf("op=%s sure=%v why=%q, want done=%v sure=%v", d.Op, d.Sure, d.Why, tc.done, tc.sure)
			}
		})
	}
}

func TestClickPointsDropdownValuesToSelect(t *testing.T) {
	withDropdown := BuildPrompt(shop(), BuildCandidates(shop(), nil), "g", nil, "m")
	if v, _ := withDropdown.Request.Questions["operation"].Choices.Get("CLICK"); !strings.Contains(v, "use SELECT") {
		t.Fatalf("CLICK = %q", v)
	}
	o := Observation{Elements: []Element{{ID: "e1", Role: "button", Label: "Go"}}}
	without := BuildPrompt(o, BuildCandidates(o, nil), "g", nil, "m")
	if v, _ := without.Request.Questions["operation"].Choices.Get("CLICK"); strings.Contains(v, "SELECT") {
		t.Fatalf("CLICK mentions SELECT with no dropdown: %q", v)
	}
}
