//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// TestKeysThatRunACommandDoNotShiftTheReplies covers a control connection that
// answers the wrong question from then on.
//
// tmux writes a guard block for every command it runs on this client's behalf,
// not only the ones this client sent: keys delivered into a pane that is in a
// mode are looked up as bindings, and the command a binding runs gets a block
// of its own. tmux marks the difference in the guard's flags -- 1 for a command
// that arrived over the control channel, 0 for anything else -- and reading the
// stranger's block as a reply shifts every later reply by one, permanently. The
// caller after it reads somebody else's answer as its own, which is how a
// server identity probe came back holding a session listing.
//
//libtmux:real-tmux
func TestKeysThatRunACommandDoNotShiftTheReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server := tmuxtest.NewServer(ctx, t)
	session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{
		Name: "intruder", Width: 80, Height: 24,
	})
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("Panes() = (%d, %v)", len(panes), err)
	}
	pane := panes[0].ID().String()

	control, err := server.OpenControl(ctx, session)
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	defer func() { _ = control.Close() }()

	answer := func(marker string) string {
		t.Helper()
		result, err := control.Cmd(ctx, "display-message", "-p", marker)
		if err != nil {
			t.Fatalf("display-message %s: %v", marker, err)
		}
		return strings.TrimSpace(string(result.RawStdout))
	}
	if got := answer("BEFORE"); got != "BEFORE" {
		t.Fatalf("the connection answered %q before anything happened", got)
	}

	// A pane in a mode turns keys into commands, one lookup per key, and tmux
	// writes a guard block for each command it runs on this client's behalf.
	// A word of them rather than a single key, because the shift grows with
	// the number of blocks and a test that survives one may not survive seven.
	if _, err := control.Cmd(ctx, "copy-mode", "-t", pane); err != nil {
		t.Fatalf("copy-mode: %v", err)
	}
	if _, err := control.Cmd(ctx, "send-keys", "-t", pane, "echoes"); err != nil {
		t.Fatalf("send-keys: %v", err)
	}

	// Every reply from here has to be the answer to the question asked.
	for _, marker := range []string{"AFTER-ONE", "AFTER-TWO", "AFTER-THREE"} {
		if got := answer(marker); got != marker {
			t.Fatalf("asked for %s and the connection answered %q: the replies "+
				"have shifted and will not come back", marker, got)
		}
	}
}
