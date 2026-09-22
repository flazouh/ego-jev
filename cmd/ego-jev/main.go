// Command ego-jev drives an ego-browser Page toward a goal with TypeSafe Jev choosing each step.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/flazouh/ego-jev/internal/config"
)

var version = "dev"

// Exit codes. Handoff means the run stopped cleanly and the caller should take over at the current page.
const (
	exitDone    = 0
	exitError   = 1
	exitUsage   = 2
	exitHandoff = 3
)

const usage = `ego-jev drives an ego-browser Page with TypeSafe Jev choosing each step.

Usage:
  ego-jev run [flags] "goal"   run a goal on an ego task space (ego-jev run -h for flags)
  ego-jev doctor [flags]       check ego-browser, keys, and the TypeSafe API
  ego-jev version

Exit codes: 0 done, 3 handed back (review, unsure, blocked, stuck, needs_text, max_steps), 1 error, 2 usage.
Keys: TYPESAFE_API_KEY (required) and OPENROUTER_API_KEY (optional text model), from the environment or
%s.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, config.DefaultEnvFile())
		return exitUsage
	}
	switch args[0] {
	case "run":
		return runCommand(ctx, args[1:], stdout, stderr)
	case "doctor":
		return doctorCommand(ctx, args[1:], stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, version)
		return exitDone
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, usage, config.DefaultEnvFile())
		return exitDone
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n"+usage, args[0], config.DefaultEnvFile())
	return exitUsage
}
