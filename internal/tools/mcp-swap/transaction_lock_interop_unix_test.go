//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransactionLockUsesTheSharedCrossPortPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	path, err := transactionLockPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "libtmux-mcp-dev", "swap", "state.lock")
	if path != want {
		t.Fatalf("transaction lock = %q, want %q", path, want)
	}
}

func TestTransactionLockInteroperatesWithPythonRecordLocks(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	lockPath := isolatedLockPath(t)
	lock, err := acquireTransactionLock()
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = lock.close()
		}
	})
	assertPythonLockBlocked(t, python, lockPath)
	if err := lock.validate(); err != nil {
		t.Fatal(err)
	}
	assertPythonLockBlocked(t, python, lockPath)
	if err := lock.close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	assertPythonLockAvailable(t, python, lockPath)
}

func TestReadingAConfigAliasRetainsTheDescriptorUntilUnlock(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	lockPath := isolatedLockPath(t)
	lock, err := acquireTransactionLock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.close(); err != nil {
			t.Error(err)
		}
	}()
	alias := filepath.Join(t.TempDir(), "config.json")
	if err := os.Link(lockPath, alias); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readBoundFile(alias); err == nil ||
		!strings.Contains(err.Error(), "active state lock") {
		t.Fatalf("alias read error = %v, want active-lock rejection", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	assertPythonLockBlocked(t, python, lockPath)
}

func TestServerPreflightRunsBeforeTheTransactionLock(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable")
	}
	lockPath := isolatedLockPath(t)
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(preflightHelperEnvironment, "lock-available")
	t.Setenv("MCP_SWAP_PREFLIGHT_LOCK", lockPath)
	target := jsonPreflightClient(t, "outside-lock", `{"mcpServers":{}}`)
	if err := usePreparedLocal([]client{target}, preflightTestPlan(), false, true); err != nil {
		t.Fatal(err)
	}
}

func assertPythonLockBlocked(t *testing.T, python, path string) {
	t.Helper()
	command := exec.Command(python, "-c", `
import fcntl, os, sys
fd = os.open(sys.argv[1], os.O_RDWR)
try:
    fcntl.lockf(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
except BlockingIOError:
    raise SystemExit(73)
raise SystemExit(0)
`, path)
	err := command.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("Python contender error = %v, want blocked exit 73", err)
	}
}

func assertPythonLockAvailable(t *testing.T, python, path string) {
	t.Helper()
	command := exec.Command(python, "-c", `
import fcntl, os, sys
fd = os.open(sys.argv[1], os.O_RDWR)
fcntl.lockf(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
`, path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Python contender after unlock: %v: %s", err, output)
	}
}
