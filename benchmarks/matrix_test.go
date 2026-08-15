package main

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// TestMatrixAnswersAgree is the claim the whole table rests on.
//
// Cost may differ between modes; the answer may not. A table whose rows answered
// the same query differently would be comparing two different things while
// presenting them as alternatives, so this asks every row the one question and
// fails if any of them disagrees.
//
// It measures once and asserts against that, rather than measuring a second
// time: the point is that these particular numbers came from runs that agreed.
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

	// The five build rows built the same window and are compared with each
	// other. The two snapshot rows read a server built differently, so they are
	// compared with each other instead.
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

// TestMatrixCostsDifferAsDocumented gates what each row is supposed to
// demonstrate, rather than leaving a reader to infer it from numbers that move
// between machines.
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

	// A control connection carries commands without starting anything, and is
	// an attached tmux client for as long as it is open. Both halves matter:
	// the second is why connecting is a choice rather than a default.
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

	// A snapshot over a bound transport skips the identity read that closes its
	// listing, because staying connected already proves what that read asks.
	// That is one fewer command, and it is why the row is in the table.
	got := byMode["snapshot"].processes - byMode["snapshot, bound"].processes
	if want := 1; got != want {
		t.Errorf("InstanceBoundEngine saved %d tmux commands, want exactly %d (%d vs %d)",
			got, want, byMode["snapshot"].processes, byMode["snapshot, bound"].processes)
	}
}

// probedVersion reports the tmux the table was measured against.
func probedVersion(ctx context.Context, t *testing.T) tmux.Version {
	t.Helper()
	version, err := probeVersion(ctx)
	if err != nil {
		t.Fatalf("probeVersion() error = %v", err)
	}
	return version
}
