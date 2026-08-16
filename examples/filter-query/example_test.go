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

// TestFilterQuery runs the example itself against a real tmux. The harness gives
// it a server on a socket path of its own and takes it away afterwards, so this
// reaches neither the socket the example uses when a reader runs it nor any
// tmux already on the machine.
func TestFilterQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t))
	})

	// The point of the example is that a filter tmux evaluates itself returns
	// the same answer a predicate applied in Go would. A filter that matched
	// nothing would still print a line, so the count is what is asserted.
	if want := "live matches: 1"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
	if want := "active panes:"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
}
