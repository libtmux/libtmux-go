package tmux_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// tmux sends a control client the output of every pane in its session. An
// observation keeps only its own pane's, so a busy sibling cannot fill its
// queue and end it, however long the observation is held between waits.
//
//libtmux:real-tmux
func TestPaneObservationKeepsOnlyItsPanesOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{})
	quiet, err := session.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	window := tmuxtest.NewWindow(ctx, t, session, tmux.NewWindowRequest{})
	busy, err := window.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tmuxtest.WaitForShellReady(ctx, t, quiet)
	tmuxtest.WaitForShellReady(ctx, t, busy)

	observation, err := quiet.OpenObservation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observation.Close() }()

	// The sibling is seen to have written before the observed pane writes,
	// so its output reaches every client of the session first.
	tmuxtest.Type(ctx, t, busy, "echo sibling-$((1+1))")
	tmuxtest.WaitForText(ctx, t, busy, "sibling-2")
	tmuxtest.Type(ctx, t, quiet, "echo observed-$((1+1))")

	var seen strings.Builder
	for !strings.Contains(seen.String(), "observed-2") {
		notification, err := observation.NextNotification(ctx)
		if err != nil {
			t.Fatal(err)
		}
		pane, output, ok := notification.Output()
		if !ok {
			continue
		}
		if pane != quiet.ID() {
			t.Fatalf("the observation of %s received output from %s: %q", quiet.ID(), pane, output)
		}
		seen.Write(output)
	}
}
