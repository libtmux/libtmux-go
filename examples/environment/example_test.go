package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

// TestEnvironment runs the example itself against a real tmux. The harness gives
// it a server on a socket path of its own and takes it away afterwards, so this
// reaches neither the socket the example uses when a reader runs it nor any
// tmux already on the machine.
func TestEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t))
	})

	// "ready" is the value set into the session environment and read back out;
	// "true" is the pane rediscovered from a TMUX environment matching the pane
	// the example split. Either half alone would pass while the other was
	// broken, so both are asserted together in the line the example prints.
	if want := "ready true"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
}
