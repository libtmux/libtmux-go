package main

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// TestMatrixAnswersAgree requires equivalent results within each workload.
//
//libtmux:real-tmux
func TestMatrixAnswersAgree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	version := probedVersion(ctx, t)
	rows, err := measureAll(ctx, version)
	if err != nil {
		t.Fatalf("measureAll() error = %v", err)
	}
	wantRows := 2
	if supportsOwnedConnections(version) {
		wantRows = 5
	}
	if len(rows) != wantRows {
		t.Fatalf("measured %d rows, want %d on tmux %s", len(rows), wantRows, version)
	}
	t.Log(table(rows, version, describeMachine()))

	for _, r := range rows[1:] {
		if r.answer != rows[0].answer {
			t.Errorf("%s answered %q, process answered %q",
				r.mode, r.answer, rows[0].answer)
		}
	}
}

// TestMatrixCostsDifferAsDocumented guards the lane-specific cost claims.
//
//libtmux:real-tmux
func TestMatrixCostsDifferAsDocumented(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	version := probedVersion(ctx, t)
	rows, err := measureAll(ctx, version)
	if err != nil {
		t.Fatalf("measureAll() error = %v", err)
	}
	byMode := map[string]row{}
	for _, r := range rows {
		byMode[r.mode] = r
	}

	// Chaining removes a tmux process per command it groups.
	if byMode["chained"].processes >= byMode["process"].processes {
		t.Errorf("chaining did not reduce tmux invocations: %d vs %d",
			byMode["chained"].processes, byMode["process"].processes)
	}

	if !supportsOwnedConnections(version) {
		return
	}

	// A connection starts no subprocesses but appears as an attached client.
	if got := byMode["connection"].processes; got != 0 {
		t.Errorf("connection started %d tmux processes, want 0", got)
	}
	if byMode["connection"].clients <= byMode["process"].clients {
		t.Errorf("a control connection did not appear as an extra client: %d vs %d",
			byMode["connection"].clients, byMode["process"].clients)
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
