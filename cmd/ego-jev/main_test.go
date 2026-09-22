package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func call(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestExitCodesForUsage(t *testing.T) {
	missingEnv := filepath.Join(t.TempDir(), "absent.env")
	cases := []struct {
		args []string
		code int
		out  string
	}{
		{nil, exitUsage, ""},
		{[]string{"version"}, exitDone, "dev"},
		{[]string{"help"}, exitDone, "Usage:"},
		{[]string{"run", "-h"}, exitDone, ""},
		{[]string{"doctor", "-h"}, exitDone, ""},
		{[]string{"nope"}, exitUsage, ""},
		{[]string{"run"}, exitUsage, ""},
		{[]string{"run", "--max-steps", "0", "goal"}, exitUsage, ""},
		{[]string{"run", "--min-p", "1.5", "goal"}, exitUsage, ""},
		{[]string{"run", "--value", "no-equals", "goal"}, exitUsage, ""},
		{[]string{"run", "--allow-risky", "(", "goal"}, exitUsage, ""},
		{[]string{"run", "--env-file", missingEnv, "goal"}, exitError, "TYPESAFE_API_KEY is missing"},
	}
	for _, tc := range cases {
		if tc.args != nil && tc.args[0] == "run" {
			t.Setenv("TYPESAFE_API_KEY", "")
		}
		code, stdout, _ := call(t, tc.args...)
		if code != tc.code || !strings.Contains(stdout, tc.out) {
			t.Errorf("%v: code=%d stdout=%q, want %d containing %q", tc.args, code, stdout, tc.code, tc.out)
		}
	}
}

func TestJSONModePrintsAResultEvenWhenSetupFails(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	code, stdout, _ := call(t, "run", "--json", "--env-file", filepath.Join(t.TempDir(), "absent.env"), "goal")
	var out output
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if code != exitError || out.Status != "error" || !strings.Contains(out.Reason, "TYPESAFE_API_KEY") {
		t.Fatalf("code=%d out=%+v", code, out)
	}
}

func TestParseRunSeedsFlagsFromDefaultOptions(t *testing.T) {
	f, goal, err := parseRun([]string{"--value", "Where from?=Zurich", "--allow-risky", "delete account", "Book it"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if goal != "Book it" || f.opts.MaxSteps != 25 || f.opts.Gates.MinProbability != 0.5 || f.opts.Model != "jev-latest" {
		t.Fatalf("opts = %+v goal=%q", f.opts, goal)
	}
	if f.opts.Values["Where from?"] != "Zurich" || len(f.opts.AllowRisky) != 1 || !f.opts.AllowRisky[0].MatchString("Delete Account") {
		t.Fatalf("values=%v allowRisky=%v", f.opts.Values, f.opts.AllowRisky)
	}
}
