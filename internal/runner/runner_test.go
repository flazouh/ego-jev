package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/flazouh/ego-jev/internal/policy"
	"github.com/flazouh/ego-jev/internal/scripted"
)

// fakePage serves a fixed list of observations and records every operation.
type fakePage struct {
	views     []policy.Observation
	i         int
	performed []string
	held      []string
}

func (f *fakePage) Observe(int, int) (policy.Observation, error) {
	o := f.views[min(f.i, len(f.views)-1)]
	return o, nil
}

func (f *fakePage) Perform(op policy.Op, target *policy.Target, text string, _ policy.Viewport) (string, error) {
	entry := string(op)
	if target != nil {
		entry += " " + target.Label
	}
	if text != "" {
		entry += " = " + text
	}
	f.performed = append(f.performed, entry)
	f.i++
	return "fake", nil
}

func (f *fakePage) MarkHeld(ids []string) ([]string, error) {
	f.held = ids
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = `[data-ego-jev="` + id + `"]`
	}
	return out, nil
}

func view(text string, els ...policy.Element) policy.Observation {
	return policy.Observation{URL: "https://shop.test/" + text, Title: "Shop", Text: text, Elements: els}
}

var (
	searchBox = policy.Element{ID: "e1", Role: "searchbox", Label: "Search products", Editable: true}
	searchBtn = policy.Element{ID: "e2", Role: "button", Label: "Search"}
	deleteBtn = policy.Element{ID: "e3", Role: "button", Label: "Delete account"}
)

func TestRunTypesClicksAndFinishes(t *testing.T) {
	page := &fakePage{views: []policy.Observation{view("home", searchBox, searchBtn), view("typed", searchBox, searchBtn), view("results")}}
	chooser := &scripted.Chooser{Script: []scripted.Step{scripted.T(policy.Type, "Search products"), scripted.T(policy.Click, ": Search$"), scripted.S(policy.Done)}}
	res := Run(context.Background(), page, chooser, "Search for mugs", withValues(map[string]string{"search": "blue mug"}))
	if res.Status != Done || res.Steps != 2 {
		t.Fatalf("result = %+v", res)
	}
	want := []string{"TYPE Search products = blue mug", "CLICK Search"}
	if len(page.performed) != 2 || page.performed[0] != want[0] || page.performed[1] != want[1] {
		t.Fatalf("performed = %v", page.performed)
	}
	if res.Trace[0].TextFrom != "values" {
		t.Fatalf("textFrom = %q", res.Trace[0].TextFrom)
	}
}

func TestQuotedGoalTextIsPickedByJev(t *testing.T) {
	page := &fakePage{views: []policy.Observation{view("home", searchBox), view("typed", searchBox)}}
	chooser := &scripted.Chooser{Script: []scripted.Step{scripted.T(policy.Type, "Search"), scripted.S(policy.Done)}, Values: map[string]string{"Search products": "blue mug"}}
	res := Run(context.Background(), page, chooser, "Search for 'blue mug'", DefaultOptions())
	if res.Status != Done || page.performed[0] != "TYPE Search products = blue mug" || res.Trace[0].TextFrom != "goal" {
		t.Fatalf("result = %+v performed = %v", res, page.performed)
	}
}

func TestReviewMarksHeldControlsAndDoesNotAct(t *testing.T) {
	page := &fakePage{views: []policy.Observation{view("settings", searchBtn, deleteBtn)}}
	chooser := &scripted.Chooser{Script: []scripted.Step{scripted.S(policy.Review)}}
	res := Run(context.Background(), page, chooser, "Delete my account", DefaultOptions())
	if res.Status != Review || len(page.performed) != 0 || len(res.Held) != 1 || res.Held[0].Selector != `[data-ego-jev="e3"]` {
		t.Fatalf("result = %+v performed = %v", res, page.performed)
	}
}

type splitChooser struct{}

func (splitChooser) Choose(context.Context, policy.Request) (policy.Response, error) {
	return policy.Response{Answers: map[string]policy.Answer{
		"operation":    {Type: "choice", Choice: "CLICK", Probabilities: map[string]float64{"CLICK": 0.45, "WAIT": 0.4, "DONE": 0.15}},
		"click_target": {Type: "choice", Choice: "e2", Probabilities: map[string]float64{"e2": 1}},
	}}, nil
}

func TestUnsureStopsBeforeActing(t *testing.T) {
	page := &fakePage{views: []policy.Observation{view("home", searchBtn)}}
	res := Run(context.Background(), page, splitChooser{}, "g", DefaultOptions())
	if res.Status != Unsure || len(page.performed) != 0 || res.Reason == "" || res.Top == nil || len(res.Top.Operation) == 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestThreeUnchangedStepsAreStuck(t *testing.T) {
	page := &fakePage{views: []policy.Observation{view("same", searchBtn)}}
	chooser := &scripted.Chooser{Script: []scripted.Step{scripted.T(policy.Click, "Search")}}
	res := Run(context.Background(), page, chooser, "g", DefaultOptions())
	if res.Status != Stuck || len(page.performed) != 3 {
		t.Fatalf("result = %+v performed = %d", res, len(page.performed))
	}
}

func TestFieldWithNoKnownTextNeedsText(t *testing.T) {
	page := &fakePage{views: []policy.Observation{view("home", searchBox)}}
	chooser := &scripted.Chooser{Script: []scripted.Step{scripted.T(policy.Type, "Search")}}
	res := Run(context.Background(), page, chooser, "Find something", DefaultOptions())
	if res.Status != NeedsText || res.Field == nil || res.Field.Label != "Search products" || len(page.performed) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

type failingChooser struct{}

func (failingChooser) Choose(context.Context, policy.Request) (policy.Response, error) {
	return policy.Response{}, errors.New("typesafe returned HTTP 401")
}

func TestChooserErrorEndsTheRun(t *testing.T) {
	res := Run(context.Background(), &fakePage{views: []policy.Observation{view("home", searchBtn)}}, failingChooser{}, "g", DefaultOptions())
	if res.Status != Failed || res.Reason != "typesafe returned HTTP 401" {
		t.Fatalf("result = %+v", res)
	}
}

func withValues(v map[string]string) Options {
	o := DefaultOptions()
	o.Values = v
	return o
}

func TestMaxStepsReportsThePageAfterTheLastAction(t *testing.T) {
	page := &fakePage{views: []policy.Observation{view("one", searchBtn), view("two", searchBtn), view("three", searchBtn)}}
	chooser := &scripted.Chooser{Script: []scripted.Step{scripted.T(policy.Click, "Search")}}
	opts := DefaultOptions()
	opts.MaxSteps = 2
	res := Run(context.Background(), page, chooser, "g", opts)
	if res.Status != MaxSteps || res.Steps != 2 || res.URL != "https://shop.test/three" || res.History[1].Changed == nil {
		t.Fatalf("result = %+v", res)
	}
}
