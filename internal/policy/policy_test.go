package policy

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var gates = Gates{MinProbability: 0.5, MinMargin: 0.1}

func shop() Observation {
	return Observation{
		URL: "https://shop.test/", Title: "Shop", Text: "Welcome",
		Scroll: Scroll{Down: true},
		Elements: []Element{
			{ID: "e1", Role: "searchbox", Label: "Search products", Editable: true},
			{ID: "e2", Role: "button", Label: "Search"},
			{ID: "e3", Role: "combobox", Label: "Language", Value: "English", Options: []Option{{Value: "en", Label: "English", Selected: true}, {Value: "fr", Label: "French"}}},
			{ID: "e4", Role: "button", Label: "Delete account"},
			{ID: "e5", Role: "checkbox", Label: "Dark mode", Checked: "false"},
		},
	}
}

func choice(pick string, probs map[string]float64) Answer {
	return Answer{Type: "choice", Choice: pick, Probabilities: probs, Confidence: 0.9}
}

func yes(p float64) Answer { return Answer{Type: "noul", Noul: &p} }

func keys(m map[string]Question) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestCandidatesSortElementsAndHoldRiskyControls(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	if got := c.Pools[Click].Descriptions.Keys(); !reflect.DeepEqual(got, []string{"e1", "e2", "e5"}) {
		t.Fatalf("click pool = %v", got)
	}
	if got := c.Pools[Type].Descriptions.Keys(); !reflect.DeepEqual(got, []string{"e1"}) {
		t.Fatalf("type pool = %v", got)
	}
	if got := c.Pools[Select].Descriptions.Keys(); !reflect.DeepEqual(got, []string{"e3=fr"}) {
		t.Fatalf("select pool = %v", got)
	}
	if len(c.Held) != 1 || c.Held[0].Label != "Delete account" {
		t.Fatalf("held = %+v", c.Held)
	}
}

func TestAllowRiskyLetsAMatchingControlThrough(t *testing.T) {
	c := BuildCandidates(shop(), []*regexp.Regexp{regexp.MustCompile(`(?i)delete account`)})
	if _, ok := c.Pools[Click].Target("e4"); !ok || len(c.Held) != 0 {
		t.Fatalf("delete account should be clickable, held = %+v", c.Held)
	}
}

func TestOneRequestAsksEveryQuestionAtOnce(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	req := BuildPrompt(shop(), c, "Search for mugs", nil, "jev-latest")
	if got := keys(req.Request.Questions); !reflect.DeepEqual(got, []string{"click_target", "goal_met", "needs_held", "operation", "select_target", "type_target"}) {
		t.Fatalf("questions = %v", got)
	}
	ops := req.Request.Questions["operation"].Choices.Keys()
	if want := []string{"CLICK", "TYPE", "SELECT", "SCROLL_DOWN", "WAIT", "DONE", "REVIEW", "BLOCKED"}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("operations = %v, want %v", ops, want)
	}
	state := req.Request.State.(State)
	for _, e := range state.Elements {
		if strings.Contains(e, "Delete account") {
			t.Fatalf("held control leaked into elements: %q", e)
		}
	}
	if !reflect.DeepEqual(state.HeldForUser, []string{"button: Delete account"}) {
		t.Fatalf("held_for_user = %v", state.HeldForUser)
	}
}

func TestRequestEncodesCriteriaInInsertionOrder(t *testing.T) {
	req := BuildPrompt(shop(), BuildCandidates(shop(), nil), "g", nil, "jev-latest")
	body, err := json.Marshal(req.Request.Questions["operation"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"criteria":{"CLICK":`) || strings.Index(string(body), `"WAIT"`) > strings.Index(string(body), `"DONE"`) {
		t.Fatalf("criteria out of order: %s", body)
	}
}

func TestPressEnterIsOnlyOfferedRightAfterTyping(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	if _, ok := BuildPrompt(shop(), c, "g", nil, "m").Request.Questions["operation"].Choices.Get("PRESS_ENTER"); ok {
		t.Fatal("PRESS_ENTER offered without typing")
	}
	after := BuildPrompt(shop(), c, "g", []Action{{Op: Type, Target: "e1"}}, "m")
	if _, ok := after.Request.Questions["operation"].Choices.Get("PRESS_ENTER"); !ok {
		t.Fatal("PRESS_ENTER missing after typing")
	}
}

func TestTargetsAreCutTo255(t *testing.T) {
	o := shop()
	o.Elements = nil
	for i := 1; i <= 300; i++ {
		o.Elements = append(o.Elements, Element{ID: fmt.Sprintf("e%d", i), Role: "link", Label: fmt.Sprintf("Link %d", i)})
	}
	req := BuildPrompt(o, BuildCandidates(o, nil), "g", nil, "m")
	if n := req.Request.Questions["click_target"].Choices.Len(); n != 255 {
		t.Fatalf("click targets = %d", n)
	}
}

func TestPromptReadUsesTheHeadThatMatchesTheOperation(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	req := BuildPrompt(shop(), c, "Search for mugs", nil, "m")
	resp := Response{Answers: map[string]Answer{
		"operation":     choice("TYPE", map[string]float64{"CLICK": 0.1, "TYPE": 0.85, "WAIT": 0.05}),
		"type_target":   choice("e1", map[string]float64{"e1": 1}),
		"click_target":  choice("e2", map[string]float64{"e1": 0.2, "e2": 0.7, "e5": 0.1}),
		"select_target": choice("e3=fr", map[string]float64{"e3=fr": 1}),
		"needs_held":    yes(0.1),
	}}
	d, err := req.Read(resp, gates)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != Type || d.Target.ID != "e1" || !d.Sure || d.Operation.Probability != 0.85 {
		t.Fatalf("decision = %+v", d)
	}
}

func TestCloseOperationVoteIsUnsure(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	req := BuildPrompt(shop(), c, "g", nil, "m")
	resp := Response{Answers: map[string]Answer{
		"operation":    choice("CLICK", map[string]float64{"CLICK": 0.45, "TYPE": 0.4, "WAIT": 0.15}),
		"click_target": choice("e2", map[string]float64{"e1": 0.1, "e2": 0.8, "e5": 0.1}),
	}}
	d, _ := req.Read(resp, gates)
	if d.Sure || !strings.Contains(d.Why, "operation") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestSplitVoteBetweenSafeClicksActsButBetweenFieldsDoesNot(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	req := BuildPrompt(shop(), c, "g", nil, "m")
	clickTie := Response{Answers: map[string]Answer{
		"operation":    choice("CLICK", map[string]float64{"CLICK": 0.9, "TYPE": 0.1}),
		"click_target": choice("e2", map[string]float64{"e2": 0.53, "e1": 0.47}),
	}}
	if d, _ := req.Read(clickTie, gates); !d.Sure {
		t.Fatalf("click tie should act: %+v", d)
	}
	fields := Observation{Elements: []Element{{ID: "e1", Role: "textbox", Label: "From", Editable: true}, {ID: "e2", Role: "textbox", Label: "To", Editable: true}}}
	c2 := BuildCandidates(fields, nil)
	req2 := BuildPrompt(fields, c2, "g", nil, "m")
	typeTie := Response{Answers: map[string]Answer{
		"operation":   choice("TYPE", map[string]float64{"TYPE": 0.9, "CLICK": 0.1}),
		"type_target": choice("e1", map[string]float64{"e1": 0.53, "e2": 0.47}),
	}}
	if d, _ := req2.Read(typeTie, gates); d.Sure {
		t.Fatalf("field tie should be unsure: %+v", d)
	}
}

func TestNeedsHeldStopsForReviewEvenWhenTheVoteIsSplit(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	req := BuildPrompt(shop(), c, "Delete my account", nil, "m")
	resp := Response{Answers: map[string]Answer{
		"operation":    choice("CLICK", map[string]float64{"CLICK": 0.32, "SCROLL_DOWN": 0.25, "REVIEW": 0.24, "TYPE": 0.19}),
		"click_target": choice("e2", map[string]float64{"e2": 0.8, "e1": 0.2}),
		"needs_held":   yes(0.9),
	}}
	d, _ := req.Read(resp, gates)
	if d.Op != Review || !d.Sure {
		t.Fatalf("decision = %+v", d)
	}
}

func TestAnOptionThatWasNeverOfferedIsRejected(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	req := BuildPrompt(shop(), c, "g", nil, "m")
	resp := Response{Answers: map[string]Answer{"operation": choice("CLICK", map[string]float64{"CLICK": 1}), "click_target": choice("e4", map[string]float64{"e4": 1})}}
	if _, err := req.Read(resp, gates); err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingAnswerIsNamedInTheError(t *testing.T) {
	c := BuildCandidates(shop(), nil)
	req := BuildPrompt(shop(), c, "g", nil, "m")
	resp := Response{Answers: map[string]Answer{"operation": choice("CLICK", map[string]float64{"CLICK": 1})}}
	if _, err := req.Read(resp, gates); err == nil || !strings.Contains(err.Error(), `did not answer "click_target"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestNoulCriteriaAreSentAsCriteria(t *testing.T) {
	req := BuildPrompt(shop(), BuildCandidates(shop(), nil), "g", nil, "m")
	body, _ := json.Marshal(req.Request.Questions["needs_held"])
	if !strings.Contains(string(body), `"criteria":{"true":`) {
		t.Fatalf("needs_held = %s", body)
	}
}

func TestElementContextIsPartOfItsDescription(t *testing.T) {
	o := Observation{Elements: []Element{{ID: "e1", Role: "combobox", Label: "Where else?", Editable: true, Context: "Enter your origin"}}}
	req := BuildPrompt(o, BuildCandidates(o, nil), "g", nil, "m")
	v, _ := req.Request.Questions["type_target"].Choices.Get("e1")
	if !strings.Contains(v, `in "Enter your origin"`) {
		t.Fatalf("description = %q", v)
	}
}

func TestValueQuestionCarriesFieldContextAndRecentActions(t *testing.T) {
	field := Target{ID: "e1", Role: "combobox", Label: "Where else?", Context: "Enter your origin"}
	req := BuildValuePrompt("from 'Zurich' to 'London'", field, []string{"Zurich", "London"}, []Action{{Op: Click, Label: "Where from?"}}, "m")
	state := req.Request.State.(ValueState)
	if !strings.Contains(state.Field, `in "Enter your origin"`) || len(state.RecentActions) != 1 || state.RecentActions[0].Label != "Where from?" {
		t.Fatalf("state = %+v", state)
	}
}

func TestValuePromptReadReturnsOnlyAClearPick(t *testing.T) {
	req := BuildValuePrompt("from 'Zurich' to 'London'", Target{Role: "combobox", Label: "From"}, []string{"Zurich", "London"}, nil, "m")
	cases := []struct {
		answer Answer
		want   string
		ok     bool
	}{
		{choice("v1", map[string]float64{"v1": 0.9, "v2": 0.1}), "Zurich", true},
		{choice("v1", map[string]float64{"v1": 0.5, "v2": 0.45, "NONE": 0.05}), "", false},
		{choice("NONE", map[string]float64{"NONE": 1}), "", false},
	}
	for _, tc := range cases {
		got, ok, err := req.Read(Response{Answers: map[string]Answer{"value": tc.answer}}, gates)
		if err != nil || got != tc.want || ok != tc.ok {
			t.Errorf("answer %v: got %q %v %v", tc.answer.Choice, got, ok, err)
		}
	}
}

func TestQuotedSpans(t *testing.T) {
	if got := QuotedSpans(`Search for 'blue mug' and "red cup", don't stop at “green”`); !reflect.DeepEqual(got, []string{"blue mug", "red cup", "green"}) {
		t.Fatalf("spans = %v", got)
	}
	if got := QuotedSpans("Find flights from Zurich to London"); len(got) != 0 {
		t.Fatalf("spans = %v", got)
	}
}
