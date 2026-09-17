package mcp_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// A marker in a command a caller just typed is on screen as the shell's echo
// of it, before the command runs. Reporting that as a match tells an agent the
// command finished when it has not started.
//
//libtmux:real-tmux
func TestWaitForTextSeparatesAnEchoFromOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "echo-trap"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	panes := snapshot.Panes()
	if len(panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(panes))
	}
	pane := panes[0]
	tmuxtest.WaitForShellReady(ctx, t, pane)

	// Typed without Enter, so the echo is all that reaches the screen and the
	// wait below cannot race the command running.
	const marker = "GO-ECHO-MARKER"
	typed := "echo " + marker
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &typed, Literal: true, SkipEnter: true,
	}); err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForScreen(ctx, t, pane, "the typed line echoed", func(screen []string) bool {
		return slices.ContainsFunc(screen, func(line string) bool {
			return strings.Contains(line, marker)
		})
	})

	var waited struct {
		Outcome        string `json:"outcome"`
		Found          bool   `json:"found"`
		MatchedAtEntry bool   `json:"matchedAtEntry"`
	}
	call(ctx, t, session, "wait_for_text", map[string]any{
		"pane_id":  pane.ID().String(),
		"patterns": []any{marker},
		"timeout":  5,
	}, &waited)
	if waited.Outcome != "alreadyOnScreen" || waited.Found || !waited.MatchedAtEntry {
		t.Fatalf("wait_for_text = %#v, want alreadyOnScreen with found false", waited)
	}
}
