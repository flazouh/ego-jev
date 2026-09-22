package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/flazouh/ego-jev/internal/config"
	"github.com/flazouh/ego-jev/internal/policy"
	"github.com/flazouh/ego-jev/internal/runner"
	"github.com/flazouh/ego-jev/internal/typesafe"
)

func doctorCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	envFile := fs.String("env-file", config.DefaultEnvFile(), "dotenv file with API keys")
	binary := fs.String("ego-browser", "ego-browser", "ego-browser executable")
	model := fs.String("model", runner.DefaultOptions().Model, "TypeSafe model to ping")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return exitDone
	} else if err != nil {
		return exitUsage
	}
	ok := true
	check := func(name string, err error, detail string) {
		if err != nil {
			ok = false
			fmt.Fprintf(stdout, "FAIL  %s: %v\n", name, err)
			return
		}
		fmt.Fprintf(stdout, "ok    %s %s\n", name, detail)
	}

	path, err := exec.LookPath(*binary)
	check("ego-browser", err, path)
	keys, err := config.Load(*envFile)
	check("env file", err, *envFile)
	if apiKey := keys.Get("TYPESAFE_API_KEY"); apiKey == "" {
		check("TYPESAFE_API_KEY", errors.New("not set"), "")
	} else {
		check("TYPESAFE_API_KEY", nil, "set")
		ping := policy.Request{Model: *model, State: "The sky is blue.", Questions: map[string]policy.Question{
			"ping": {Type: "noul", Instructions: "Is the sky described as blue?"},
		}}
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		started := time.Now()
		resp, err := typesafe.New(apiKey).Choose(pctx, ping)
		cancel()
		check("TypeSafe API", err, fmt.Sprintf("%s answered in %dms", resp.Model, time.Since(started).Milliseconds()))
	}
	if keys.Get("OPENROUTER_API_KEY") == "" {
		fmt.Fprintln(stdout, "warn  OPENROUTER_API_KEY not set: fields without a --value or quoted goal text will stop with needs_text")
	} else {
		fmt.Fprintln(stdout, "ok    OPENROUTER_API_KEY set")
	}
	if !ok {
		return exitError
	}
	return exitDone
}
