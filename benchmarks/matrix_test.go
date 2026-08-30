package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// TestProcessCounterForwardsAndCountsRealTmux catches a proxy that records an
// invocation without preserving the selected tmux executable's behavior.
func TestProcessCounterForwardsAndCountsRealTmux(t *testing.T) {
	counter, err := newProcessCounter(t.TempDir())
	if errors.Is(err, errProcessCounterUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("newProcessCounter() error = %v", err)
	}

	direct := exec.Command(counter.executable, "-V")
	direct.Env = cleanEnvironment()
	want, err := direct.CombinedOutput()
	if err != nil {
		t.Fatalf("real tmux -V error = %v; output = %q", err, want)
	}

	proxied := exec.Command(counter.proxy, "-V")
	proxied.Env = counter.environment(cleanEnvironment())
	got, err := proxied.CombinedOutput()
	if err != nil {
		t.Fatalf("proxied tmux -V error = %v; output = %q", err, got)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Errorf("proxied tmux -V = %q, want %q", got, want)
	}

	count, err := counter.total()
	if err != nil {
		t.Fatalf("total() error = %v", err)
	}
	if count != 1 {
		t.Errorf("proxy recorded %d invocations, want 1", count)
	}
	if err := counter.reset(); err != nil {
		t.Fatalf("reset() error = %v", err)
	}
	count, err = counter.total()
	if err != nil {
		t.Fatalf("total() after reset error = %v", err)
	}
	if count != 0 {
		t.Errorf("proxy recorded %d invocations after reset, want 0", count)
	}
}

func TestMinimumSupportedTmuxHasConnectionRows(t *testing.T) {
	version, err := tmux.ParseVersion(tmux.MinimumSupportedVersion)
	if err != nil {
		t.Fatalf("ParseVersion(%q) error = %v", tmux.MinimumSupportedVersion, err)
	}
	if !supportsOwnedConnections(version) {
		t.Fatalf("tmux %s omitted connection rows", version)
	}
}

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
	if got := byMode["process"].processes; got == 0 {
		t.Error("process path recorded no tmux invocations")
	}
	if got := byMode["chained"].processes; got == 0 {
		t.Error("chained path recorded no tmux invocations")
	}
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
	if errors.Is(err, errProcessCounterUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("probeVersion() error = %v", err)
	}
	return version
}
