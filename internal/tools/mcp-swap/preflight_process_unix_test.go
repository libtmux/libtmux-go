//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreflightTimeoutStopsTheProcessGroup(t *testing.T) {
	entry := preflightHelperEntry(t, "timeout")
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	t.Setenv("MCP_SWAP_PREFLIGHT_HEARTBEAT", heartbeat)

	started := time.Now()
	reason := preflightWithin(entry, 500*time.Millisecond)
	if !strings.Contains(reason, "deadline") {
		t.Fatalf("preflight timeout = %q", reason)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("preflight timeout took %s", elapsed)
	}
	assertHeartbeatStopped(t, heartbeat, "timeout")
}

func TestPreflightSuccessStopsTheProcessGroup(t *testing.T) {
	for _, scenario := range []string{"long-lived-tree", "success-parent-exits-tree"} {
		t.Run(scenario, func(t *testing.T) {
			entry := preflightHelperEntry(t, scenario)
			heartbeat := filepath.Join(t.TempDir(), "heartbeat")
			t.Setenv("MCP_SWAP_PREFLIGHT_HEARTBEAT", heartbeat)
			if reason := preflightWithin(entry, time.Second); reason != "" {
				t.Fatalf("preflight failed: %s", reason)
			}
			assertHeartbeatStopped(t, heartbeat, "success")
		})
	}
}

func TestPreflightOversizeStopsTheProcessGroup(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			entry := preflightHelperEntry(t, "oversized-"+stream+"-tree")
			heartbeat := filepath.Join(t.TempDir(), "heartbeat")
			t.Setenv("MCP_SWAP_PREFLIGHT_HEARTBEAT", heartbeat)
			reason := preflightWithin(entry, oversizeBudget)
			if !strings.Contains(reason, "exceeds") {
				t.Fatalf("preflight reason = %q, want stream limit", reason)
			}
			assertHeartbeatStopped(t, heartbeat, stream+" oversize")
		})
	}
}

func assertHeartbeatStopped(t *testing.T, heartbeat, boundary string) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("descendant kept running after %s: heartbeat grew from %d to %d",
			boundary, before.Size(), after.Size())
	}
}
