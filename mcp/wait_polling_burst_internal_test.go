package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// A pane that prints more rows than fit on its screen between two reads
// leaves the polling wait with the last screen only, and the wait reports
// no miss. wait_for_text says so below tmux 3.7; this pins the behavior it
// describes, so a change to readDelta that closes or widens the gap fails
// here rather than leaving the tool text stale.
//
//libtmux:real-tmux
func TestPollingWaitMissesRowsThatScrollPastBetweenReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	withoutCallerEnvironment(t)
	_, _, panes := threePaneInputFixture(ctx, t)

	// pollBurst prints rows 1 to last in one command and returns the rows the
	// polling wait reported once it saw the last one.
	pollBurst := func(pane tmux.Pane, last string) []string {
		t.Helper()
		observation, err := openPollingObservation(ctx, pane)
		if err != nil {
			t.Fatal(err)
		}
		command := "seq 1 " + last
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
			t.Fatal(err)
		}
		waitCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		defer stop()
		text, err := observation.WaitFor(waitCtx, func(seen string) bool {
			return slices.Contains(strings.Split(seen, "\n"), last)
		})
		if err != nil {
			t.Fatalf("WaitFor() error = %v, want the wait to end on row %s", err, last)
		}
		return strings.Split(text.Text, "\n")
	}

	// Control: a burst that fits the screen loses nothing.
	if rows := pollBurst(panes[1], "5"); !slices.Contains(rows, "1") {
		t.Fatalf("WaitFor() rows = %q, want row 1 of a 5-row burst", rows)
	}

	rows := pollBurst(panes[0], "200")
	if slices.Contains(rows, "1") {
		t.Fatalf("WaitFor() kept row 1 of a 200-row burst (%d rows), want the earlier rows missed", len(rows))
	}
	if len(rows) >= 200 {
		t.Fatalf("WaitFor() returned %d rows, want fewer than the 200 printed", len(rows))
	}
}
