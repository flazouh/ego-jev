package policy

import "testing"

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
		{"weak vote, goal_met only fairly sure", "DONE", 0.49, 0.85, false, true},
		{"strong vote, goal_met leaning yes", "DONE", 0.85, 0.55, true, true},
		{"strong vote, goal_met says no", "DONE", 0.85, 0.4, false, true},
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
