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

// TestSnapshotBrowser runs the example itself against a real tmux. The harness
// gives it a server on a socket path of its own and takes it away afterwards, so
// this reaches neither the socket the example uses when a reader runs it nor any
// tmux already on the machine.
func TestSnapshotBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t))
	})

	// One snapshot is walked down all three levels. Asserting a line from each
	// is what distinguishes a traversal that worked from one that listed
	// sessions and found no windows under them.
	for _, want := range []string{"session $", "window @", "pane %"} {
		if !strings.Contains(printed, want) {
			t.Errorf("printed %q, want it to contain %q", printed, want)
		}
	}
	if want := "libtmux-snapshot"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain the session it made, %q", printed, want)
	}
}
