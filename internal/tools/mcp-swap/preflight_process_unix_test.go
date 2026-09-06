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
		t.Fatalf("descendant kept running after timeout: heartbeat grew from %d to %d",
			before.Size(), after.Size())
	}
}
