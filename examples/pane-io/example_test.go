package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestPaneIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t))
	})

	exampletest.MatchesFile(t, printed, "output.txt")
}

// A shell that is slow to start prints its first prompt after the example has
// typed, which a fast machine never does by itself.
func TestPaneIOWhenTheShellStartsSlowly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		Config: exampletest.SlowShell(t),
	})
	printed := exampletest.Output(t, func() error { return run(ctx, server) })

	exampletest.MatchesFile(t, printed, "output.txt")
}

// A pane whose shell never answers still echoes what is typed. The example
// has to wait for an answer rather than for its own typing, so here it must
// run out of time instead of finishing.
func TestPaneIODoesNotTakeItsEchoForAnAnswer(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		Config: exampletest.MuteShell(t),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	printed, err := exampletest.OutputAndError(t, func() error { return run(ctx, server) })
	if !errors.Is(err, context.DeadlineExceeded) || printed != "" {
		t.Fatalf("run() printed %q and returned %v, want nothing printed and its time run out",
			printed, err)
	}
}
