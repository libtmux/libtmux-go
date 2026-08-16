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

// TestControlModeSubscribe runs the example itself against a real tmux. The
// harness gives it a server on a socket path of its own and takes it away
// afterwards, so this reaches neither the socket the example uses when a reader
// runs it nor any tmux already on the machine.
func TestControlModeSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t))
	})

	// The example only returns without an error once it has seen the rename it
	// caused, so reaching here already proves the stream delivered. What the
	// output adds is that it was heard as a notification rather than inferred.
	if want := "heard the rename"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
	if want := "notification:"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to name the notifications it read", printed)
	}
}
