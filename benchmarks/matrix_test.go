package main

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// TestMatrixAnswersAgree requires equivalent results from every measured lane.
//
//libtmux:real-tmux
func TestMatrixAnswersAgree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, err := measureAll(ctx)
	if err != nil {
		t.Fatalf("measureAll() error = %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("measured %d rows, want 7", len(rows))
	}
	t.Log(table(rows, probedVersion(ctx, t), describeMachine()))

	// Compare build and snapshot workloads within their own topology.
	builds, snapshots := rows[:5], rows[5:]
	for _, r := range builds[1:] {
		if r.answer != builds[0].answer {
			t.Errorf("%s answered %q, process answered %q",
				r.mode, r.answer, builds[0].answer)
		}
	}
	if snapshots[1].answer != snapshots[0].answer {
		t.Errorf("a bound snapshot answered %q, an unbound one %q",
			snapshots[1].answer, snapshots[0].answer)
	}
}

// TestMatrixCostsDifferAsDocumented guards the lane-specific cost claims.
//
//libtmux:real-tmux
func TestMatrixCostsDifferAsDocumented(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, err := measureAll(ctx)
	if err != nil {
		t.Fatalf("measureAll() error = %v", err)
	}
	byMode := map[string]row{}
	for _, r := range rows {
		byMode[r.mode] = r
	}

	// Control mode starts no subprocesses but appears as an attached client.
	if got := byMode["control mode"].processes; got != 0 {
		t.Errorf("control mode started %d tmux processes, want 0", got)
	}
	if byMode["control mode"].clients <= byMode["process"].clients {
		t.Errorf("a control connection did not appear as an extra client: %d vs %d",
			byMode["control mode"].clients, byMode["process"].clients)
	}

	// Chaining removes a tmux process per command it groups.
	if byMode["chained"].processes >= byMode["process"].processes {
		t.Errorf("chaining did not reduce tmux invocations: %d vs %d",
			byMode["chained"].processes, byMode["process"].processes)
	}

	// A bound transport skips the snapshot's closing identity read.
	got := byMode["snapshot"].processes - byMode["snapshot, bound"].processes
	if want := 1; got != want {
		t.Errorf("InstanceBoundEngine saved %d tmux commands, want exactly %d (%d vs %d)",
			got, want, byMode["snapshot"].processes, byMode["snapshot, bound"].processes)
	}
}

func probedVersion(ctx context.Context, t *testing.T) tmux.Version {
	t.Helper()
	version, err := probeVersion(ctx)
	if err != nil {
		t.Fatalf("probeVersion() error = %v", err)
	}
	return version
}
