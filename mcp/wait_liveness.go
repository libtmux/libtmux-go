package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
)

// paneLiveness is the answer to whether a pane still exists and runs.
type paneLiveness int

const (
	paneLive paneLiveness = iota
	// paneEnded covers a pane that was destroyed, one whose program exited
	// while remain-on-exit kept it, and a server that is no longer there.
	paneEnded
)

// probePane asks tmux whether the pane is still alive. A wait asks it when it
// has lost its view of the pane, and once a second while it runs. It must not
// run the owner's hooks, and display-message fires after-display-message on
// every release, so the question goes through run-shell -C with a
// format-expanded word no command carries: tmux refuses the word and reports
// it with the formats expanded, and run-shell has no after hook. The answer is
// tmux's single reply; the caller's schedule never decides it. The pane's id
// is part of the report because a pane that is gone is not an error: the
// formats expand for another pane, or for none.
func probePane(ctx context.Context, pane tmux.Pane) (paneLiveness, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return paneLive, err
	}
	marker := "libtmux-pane-" + hex.EncodeToString(random) + "-"
	result, err := pane.Server().Cmd(
		ctx, "run-shell", "-C", "-t", pane.ID().String(), marker+"#{q:pane_id}-#{q:pane_dead}|",
	)
	if err != nil {
		if ctx.Err() != nil {
			return paneLive, ctx.Err()
		}
		if errors.Is(err, tmux.ErrNoServer) {
			return paneEnded, nil
		}
		return paneLive, err
	}
	for _, line := range result.Stderr {
		if report, ok := strings.CutPrefix(line, "unknown command: "+marker); ok {
			// A target that no longer exists is not an error to run-shell: it
			// runs against the current pane, or none, and the report names
			// whichever pane the formats expanded for.
			report, ok := strings.CutSuffix(report, "|")
			id, dead, found := strings.Cut(report, "-")
			if !ok || !found {
				return paneLive, fmt.Errorf("pane %s liveness report %q is unreadable", pane.ID(), report)
			}
			if id != pane.ID().String() || dead == "1" {
				return paneEnded, nil
			}
			return paneLive, nil
		}
		switch {
		case strings.HasPrefix(line, "can't find pane"),
			strings.HasPrefix(line, "no server running on "),
			strings.HasPrefix(line, "error connecting to "),
			line == "server exited unexpectedly",
			line == "lost server":
			return paneEnded, nil
		}
	}
	return paneLive, fmt.Errorf("pane %s liveness probe answered %q", pane.ID(), result.Stderr)
}
