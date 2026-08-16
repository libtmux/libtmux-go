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

// TestPlannedBuild runs the example itself against a real tmux. The harness
// gives it a server on a socket path of its own and takes it away afterwards, so
// this reaches neither the socket the example uses when a reader runs it nor any
// tmux already on the machine.
//
// The example returns an error rather than printing a wrong answer when the
// plan does not group, when the split reports no pane, or when the read does not
// report the title the plan set, so those checks bite here without being
// repeated.
func TestPlannedBuild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t))
	})

	// A step that names the split cannot be rendered before the split has run,
	// and saying so is what makes a plan worth reading before it is sent.
	if want := "rendered when the split has reported its pane"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
	// The grouping is the reason the mode exists.
	if want := "tmux invocation carrying steps"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
	// The last step read back the title an earlier step set on a pane that did
	// not exist when either was recorded.
	if want := `stdout ["editor"]`; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}
}
