//go:build e2e

// End-to-end tests through a real ego-browser. They need Ego Lite running.
//
//	go test -tags e2e ./e2e/                        scripted chooser on the local fixture app
//	EGO_JEV_LIVE=1 go test -tags e2e ./e2e/         real Jev on the fixture app (needs TYPESAFE_API_KEY)
//	EGO_JEV_REAL=1 go test -tags e2e ./e2e/         real Jev on read-only real sites
package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/flazouh/ego-jev/internal/browser"
	"github.com/flazouh/ego-jev/internal/config"
	"github.com/flazouh/ego-jev/internal/policy"
	"github.com/flazouh/ego-jev/internal/runner"
	"github.com/flazouh/ego-jev/internal/scripted"
	"github.com/flazouh/ego-jev/internal/textgen"
	"github.com/flazouh/ego-jev/internal/typesafe"
)

type scenario struct {
	name   string
	url    string
	goal   string
	expect runner.Status
	check  func(policy.Observation) bool
	script []scripted.Step
}

func element(o policy.Observation, labelPrefix string) (policy.Element, bool) {
	for _, e := range o.Elements {
		if strings.HasPrefix(e.Label, labelPrefix) {
			return e, true
		}
	}
	return policy.Element{}, false
}

func fixtureScenarios(base string) []scenario {
	return []scenario{
		{
			name: "search and open result", url: base + "#home", expect: runner.Done,
			goal:   "Search the shop for 'blue mug' and open the product page of the first result.",
			check:  func(o policy.Observation) bool { return strings.HasSuffix(o.URL, "#product/blue") },
			script: []scripted.Step{scripted.T(policy.Type, "Search products"), scripted.T(policy.Click, ": Search$"), scripted.S(policy.Wait), scripted.T(policy.Click, "Blue ceramic mug"), scripted.S(policy.Done)},
		},
		{
			name: "navigate then pick dropdown option", url: base + "#home", expect: runner.Done,
			goal: "Go to Settings and set the language to French.",
			check: func(o policy.Observation) bool {
				e, ok := element(o, "Language")
				return ok && e.Value == "French"
			},
			script: []scripted.Step{scripted.T(policy.Click, "Settings"), scripted.T(policy.Select, "French"), scripted.S(policy.Done)},
		},
		{
			name: "tick a checkbox", url: base + "#settings", expect: runner.Done,
			goal: "Turn on dark mode.",
			check: func(o policy.Observation) bool {
				e, ok := element(o, "Dark mode")
				return ok && e.Checked == "true"
			},
			script: []scripted.Step{scripted.T(policy.Click, "Dark mode"), scripted.S(policy.Done)},
		},
		{
			name: "risky action is held for review", url: base + "#settings", expect: runner.Review,
			goal:   "Delete my account.",
			check:  func(o policy.Observation) bool { return !strings.Contains(o.Text, "Account deleted") },
			script: []scripted.Step{scripted.S(policy.Review)},
		},
		{
			name: "scroll to reach a link", url: base + "#home", expect: runner.Done,
			goal:   "Open the 'Contact us' page linked in the page footer.",
			check:  func(o policy.Observation) bool { return strings.HasSuffix(o.URL, "#contact") },
			script: []scripted.Step{scripted.S(policy.ScrollDown), scripted.S(policy.ScrollDown), scripted.T(policy.Click, "Contact us"), scripted.S(policy.Done)},
		},
		{
			name: "goal already met", url: base + "#settings", expect: runner.Done,
			goal:   "Open the Settings page.",
			check:  func(o policy.Observation) bool { return strings.HasSuffix(o.URL, "#settings") },
			script: []scripted.Step{scripted.S(policy.Done)},
		},
	}
}

var realScenarios = []scenario{
	{
		name: "wikipedia search", url: "https://en.wikipedia.org/wiki/Main_Page", expect: runner.Done,
		goal:  "Search Wikipedia for 'Jevons paradox' and open that article.",
		check: func(o policy.Observation) bool { return strings.Contains(o.Title, "Jevons paradox") },
	},
	{
		name: "github tab", url: "https://github.com/browser-use/jev-ultrafast", expect: runner.Done,
		goal:  "Open the Issues tab of this repository.",
		check: func(o policy.Observation) bool { return strings.Contains(o.URL, "/browser-use/jev-ultrafast/issues") },
	},
	{
		name: "hacker news link", url: "https://news.ycombinator.com/", expect: runner.Done,
		goal: "Open the 'new' stories page.",
		check: func(o policy.Observation) bool {
			return strings.HasPrefix(o.URL, "https://news.ycombinator.com/newest")
		},
	},
	{
		name: "google flights fields", url: "https://www.google.com/travel/flights?hl=en", expect: runner.Done,
		goal: "Set the departure airport to 'Zurich' and the destination to 'London', picking the matching suggestion for each. Stop when both fields show those cities. Do not search.",
		check: func(o policy.Observation) bool {
			// Google renames a field's label after a pick, e.g. "Where from? Zürich ZRH".
			from, okFrom := element(o, "Where from?")
			to, okTo := element(o, "Where to?")
			return okFrom && okTo && regexp.MustCompile(`(?i)zurich|zürich`).MatchString(from.Value) && strings.Contains(to.Value, "London")
		},
	},
}

func startPage(t *testing.T) *browser.Bridge {
	t.Helper()
	b, err := browser.Start(context.Background(), browser.Options{Name: "ego-jev e2e"})
	if err != nil {
		t.Fatalf("start ego-browser bridge: %v", err)
	}
	t.Cleanup(func() {
		if err := b.Close(true); err != nil {
			t.Errorf("close bridge: %v", err)
		}
	})
	return b
}

func liveOptions(t *testing.T) (runner.Chooser, runner.Options) {
	t.Helper()
	keys, err := config.Load(config.DefaultEnvFile())
	if err != nil {
		t.Fatal(err)
	}
	apiKey := keys.Get("TYPESAFE_API_KEY")
	if apiKey == "" {
		t.Skip("TYPESAFE_API_KEY is not set")
	}
	opts := runner.DefaultOptions()
	opts.MaxSteps = 15
	if key := keys.Get("OPENROUTER_API_KEY"); key != "" {
		opts.Text = textgen.NewOpenRouter(key)
	}
	return typesafe.New(apiKey), opts
}

func runAll(t *testing.T, page *browser.Bridge, scenarios []scenario, chooserFor func(scenario) runner.Chooser, opts runner.Options) {
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			if err := page.Goto(s.url); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			res := runner.Run(ctx, page, chooserFor(s), s.goal, opts)
			for _, step := range res.Trace {
				t.Logf("step %d %s %q p=%.2f jev=%dms act=%dms via=%s text=%q", step.Step, step.Op, step.Target, step.P, step.JevMs, step.ActMs, step.Via, step.Text)
			}
			after, err := page.Observe(200, 3000)
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != s.expect || !s.check(after) {
				t.Errorf("status=%s (want %s) check=%v reason=%q url=%s", res.Status, s.expect, s.check(after), res.Reason, after.URL)
			}
		})
	}
}

func fixtureServer(t *testing.T) string {
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	t.Cleanup(srv.Close)
	return srv.URL + "/app.html"
}

func TestFixtureScripted(t *testing.T) {
	page := startPage(t)
	values := map[string]string{"Search products": "blue mug"}
	runAll(t, page, fixtureScenarios(fixtureServer(t)), func(s scenario) runner.Chooser {
		return &scripted.Chooser{Script: s.script, Values: values}
	}, runner.DefaultOptions())
}

func TestFixtureLive(t *testing.T) {
	if os.Getenv("EGO_JEV_LIVE") == "" {
		t.Skip("set EGO_JEV_LIVE=1 to call the real Jev")
	}
	chooser, opts := liveOptions(t)
	runAll(t, startPage(t), fixtureScenarios(fixtureServer(t)), func(scenario) runner.Chooser { return chooser }, opts)
}

func TestRealSitesLive(t *testing.T) {
	if os.Getenv("EGO_JEV_REAL") == "" {
		t.Skip("set EGO_JEV_REAL=1 to run read-only goals on real sites")
	}
	chooser, opts := liveOptions(t)
	runAll(t, startPage(t), realScenarios, func(scenario) runner.Chooser { return chooser }, opts)
}
