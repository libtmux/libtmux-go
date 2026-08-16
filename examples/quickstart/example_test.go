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

// TestQuickstart runs the example itself against a real tmux. The harness gives
// it a server on a socket path of its own and takes it away afterwards, so this
// reaches neither the socket the example uses when a reader runs it nor any
// tmux already on the machine.
func TestQuickstart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t))
	})

	// The example is only finished when the pane it built has echoed back what
	// was sent to it, which is what proves the keys arrived and the capture read
	// them rather than the pane merely existing.
	if want := "libtmux ready"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
}
