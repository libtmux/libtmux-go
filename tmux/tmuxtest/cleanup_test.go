//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestScrubTmuxEnvironmentRemovesTargetingVariables(t *testing.T) {
	t.Parallel()

	environment := []string{
		"PATH=/bin",
		"TMUX=/tmp/foreign,123,0",
		"TMUX_PANE=%7",
		"TMUX_TMPDIR=/tmp/foreign",
		"TERM=screen",
	}
	want := []string{"PATH=/bin", "TERM=screen"}
	if got := scrubTmuxEnvironment(environment); !slices.Equal(got, want) {
		t.Fatalf("scrubTmuxEnvironment() = %#v, want %#v", got, want)
	}
}

func TestCleanupFailureRemainsRegisteredForSuiteRetry(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "unstarted-socket")
	record := &serverRecord{
		server: tmux.NewServer(tmux.ServerOptions{
			Binary: filepath.Join(t.TempDir(), "missing-tmux"),
		}),
		socketPath: socketPath,
	}
	registerServer(record)
	t.Cleanup(func() { unregisterServer(socketPath) })

	if err := cleanupAndUnregister(record); err == nil {
		t.Fatal("cleanupAndUnregister() error = nil, want cleanup failure")
	}

	suite.Lock()
	_, remainsRegistered := suite.records[socketPath]
	suite.Unlock()
	if !remainsRegistered {
		t.Fatal("failed cleanup record was unregistered")
	}
}

func TestFailedCleanupPreservesSocketForSuiteRetry(t *testing.T) {
	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBTMUX_TEST_REAL_TMUX", realBinary)
	proxyPath := filepath.Join(t.TempDir(), "tmux-proxy")
	proxy := []byte("#!/bin/sh\n" +
		"for argument do\n" +
		"  if [ \"$argument\" = kill-server ]; then exit 71; fi\n" +
		"done\n" +
		"exec \"$LIBTMUX_TEST_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}

	var record *serverRecord
	t.Run("failed per-test cleanup", func(t *testing.T) {
		_, record = newServer(context.Background(), t, proxyPath, false, true)
		if err := cleanupAndUnregister(record); err == nil {
			t.Fatal("cleanupAndUnregister() error = nil, want injected failure")
		}
	})
	if record == nil {
		t.Fatal("server record was not created")
	}
	realServer := tmux.NewServer(tmux.ServerOptions{
		Binary:             realBinary,
		SocketPath:         record.socketPath,
		ConfigFile:         record.configFile,
		ProcessEnvironment: scrubTmuxEnvironment(os.Environ()),
	})
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			record.server = realServer
			_ = cleanupAndUnregister(record)
		}
	})

	if _, err := os.Stat(record.socketPath); err != nil {
		t.Fatalf("socket was removed before suite retry: %v", err)
	}
	if result := runCommand(context.Background(), realServer, "list-sessions"); result.ExitCode != 0 {
		t.Fatalf("preserved socket is unusable: %#v", result)
	}
	record.server = realServer
	if err := cleanupAndUnregister(record); err != nil {
		t.Fatalf("suite-style cleanup retry: %v", err)
	}
	cleaned = true
	if _, err := os.Stat(record.tempDir); !os.IsNotExist(err) {
		t.Fatalf("server directory remains after verified cleanup: %v", err)
	}
}

func TestCleanupDiscoversMissingRecordedPID(t *testing.T) {
	_, record := newServer(context.Background(), t, "", false, true)
	recordedPID := record.pid
	record.pid = 0
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			_ = cleanupAndUnregister(record)
		}
	})

	if err := cleanupAndUnregister(record); err != nil {
		t.Fatalf("cleanup with discovered process identity: %v", err)
	}
	cleaned = true
	if processAlive(recordedPID) {
		t.Fatalf("discovered tmux server pid %d remained alive", recordedPID)
	}
	if _, err := os.Stat(record.tempDir); !os.IsNotExist(err) {
		t.Fatalf("server directory remains after discovered cleanup: %v", err)
	}
}

func TestCleanupTracksReplacementDaemonOnOwnedSocket(t *testing.T) {
	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBTMUX_TEST_REAL_TMUX", realBinary)
	proxyPath := filepath.Join(t.TempDir(), "tmux-kill-proxy")
	proxy := []byte("#!/bin/sh\n" +
		"for argument do\n" +
		"  if [ \"$argument\" = kill-server ]; then exit 71; fi\n" +
		"done\n" +
		"exec \"$LIBTMUX_TEST_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}

	realServer, record := newServer(context.Background(), t, realBinary, false, true)
	firstPID := record.pid
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			record.server = realServer
			_ = cleanupAndUnregister(record)
		}
	})
	if result := runCommand(context.Background(), realServer, "kill-server"); result.ExitCode != 0 {
		t.Fatalf("kill first daemon: %#v", result)
	}
	if !waitForProcessDeath(firstPID, time.Now().Add(cleanupTimeout)) {
		t.Fatalf("first tmux server pid %d remained alive", firstPID)
	}
	if result := runCommand(context.Background(), realServer, "new-session", "-d", "-s", "replacement"); result.ExitCode != 0 {
		t.Fatalf("start replacement daemon: %#v", result)
	}
	replacementResult := runCommand(context.Background(), realServer, "display-message", "-p", "#{pid}")
	if err := commandFailure("display-message", replacementResult); err != nil {
		t.Fatal(err)
	}
	if len(replacementResult.Stdout) != 1 {
		t.Fatalf("replacement pid output = %#v", replacementResult.Stdout)
	}
	replacementPID, err := strconv.Atoi(replacementResult.Stdout[0])
	if err != nil {
		t.Fatal(err)
	}
	if replacementPID == firstPID {
		t.Fatalf("replacement reused first pid %d; test cannot distinguish daemons", firstPID)
	}

	record.server = tmux.NewServer(tmux.ServerOptions{
		Binary:             proxyPath,
		SocketPath:         record.socketPath,
		ConfigFile:         record.configFile,
		ProcessEnvironment: scrubTmuxEnvironment(os.Environ()),
	})
	if err := cleanupAndUnregister(record); err == nil {
		t.Fatal("cleanupAndUnregister() error = nil, want injected replacement kill failure")
	}
	if record.pid != replacementPID {
		t.Fatalf("cleanup tracked pid %d, want replacement pid %d", record.pid, replacementPID)
	}
	if !processAlive(replacementPID) {
		t.Fatalf("replacement pid %d died despite injected kill failure", replacementPID)
	}
	if _, err := os.Stat(record.tempDir); err != nil {
		t.Fatalf("replacement cleanup removed owned artifacts: %v", err)
	}
	suite.Lock()
	_, remainsRegistered := suite.records[record.socketPath]
	suite.Unlock()
	if !remainsRegistered {
		t.Fatal("replacement cleanup failure unregistered owned socket")
	}

	record.server = realServer
	if err := cleanupAndUnregister(record); err != nil {
		t.Fatalf("replacement cleanup retry: %v", err)
	}
	cleaned = true
	if processAlive(replacementPID) {
		t.Fatalf("replacement pid %d remained alive after verified cleanup", replacementPID)
	}
	if _, err := os.Stat(record.tempDir); !os.IsNotExist(err) {
		t.Fatalf("server directory remains after replacement cleanup: %v", err)
	}
}

func TestCleanupRetryResumesAfterPartialArtifactRemoval(t *testing.T) {
	_, record := newServer(context.Background(), t, "", false, true)
	blocker := filepath.Join(record.tempDir, "unexpected-artifact")
	if err := os.WriteFile(blocker, []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			_ = os.Remove(blocker)
			_ = cleanupAndUnregister(record)
		}
	})

	if err := cleanupAndUnregister(record); err == nil {
		t.Fatal("cleanupAndUnregister() error = nil, want nonempty directory failure")
	}
	if !record.daemonStopped {
		t.Fatal("cleanup did not retain verified-dead phase")
	}
	if _, err := os.Stat(record.socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket remains after verified daemon death: %v", err)
	}
	if _, err := os.Stat(record.configFile); !os.IsNotExist(err) {
		t.Fatalf("config remains after partial artifact removal: %v", err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := cleanupAndUnregister(record); err != nil {
		t.Fatalf("artifact-only cleanup retry: %v", err)
	}
	cleaned = true
	if _, err := os.Stat(record.tempDir); !os.IsNotExist(err) {
		t.Fatalf("server directory remains after artifact-only retry: %v", err)
	}
}

// TestRetryCleanupWaitsBetweenAttempts covers the wait rather than the retry.
// What cleanup contends with is a server partway through shutting down, and
// asking again inside the same moment gets the same answer -- which is how two
// attempts came to report one failure twice on a loaded machine.
func TestRetryCleanupWaitsBetweenAttempts(t *testing.T) {
	const gap = 20 * time.Millisecond
	attempts := 0
	started := time.Now()
	err := retryCleanup(3, gap, func() error {
		attempts++
		if attempts < 3 {
			return errInvalidHarnessState
		}
		return nil
	})
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("retryCleanup() error = %v, want the third attempt to succeed", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if want := 2 * gap; elapsed < want {
		t.Errorf("two retries took %v, want at least %v spent waiting between them",
			elapsed, want)
	}
}

func TestRetryCleanupReportsEveryFailureWhenNoneSucceed(t *testing.T) {
	attempts := 0
	err := retryCleanup(2, time.Millisecond, func() error {
		attempts++
		return errInvalidHarnessState
	})
	if err == nil {
		t.Fatal("retryCleanup() error = nil, want the failures it collected")
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestCleanupRetriesTransientKillFailure(t *testing.T) {
	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBTMUX_TEST_REAL_TMUX", realBinary)
	statePath := filepath.Join(t.TempDir(), "first-kill-failed")
	t.Setenv("LIBTMUX_TEST_PROXY_STATE", statePath)
	proxyPath := filepath.Join(t.TempDir(), "tmux-transient-proxy")
	proxy := []byte("#!/bin/sh\n" +
		"for argument do\n" +
		"  if [ \"$argument\" = kill-server ] && [ ! -e \"$LIBTMUX_TEST_PROXY_STATE\" ]; then\n" +
		"    : > \"$LIBTMUX_TEST_PROXY_STATE\"\n" +
		"    exit 71\n" +
		"  fi\n" +
		"done\n" +
		"exec \"$LIBTMUX_TEST_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}

	_, record := newServer(context.Background(), t, proxyPath, false, true)
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			record.server = tmux.NewServer(tmux.ServerOptions{
				Binary:             realBinary,
				SocketPath:         record.socketPath,
				ConfigFile:         record.configFile,
				ProcessEnvironment: scrubTmuxEnvironment(os.Environ()),
			})
			_ = cleanupAndUnregister(record)
		}
	})

	if err := cleanupWithRetries(record, 2); err != nil {
		t.Fatalf("cleanupWithRetries() error = %v", err)
	}
	cleaned = true
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("transient proxy did not fail its first cleanup: %v", err)
	}
	if processAlive(record.pid) {
		t.Fatalf("tmux server pid %d remained alive after cleanup retry", record.pid)
	}
	if _, err := os.Stat(record.tempDir); !os.IsNotExist(err) {
		t.Fatalf("server directory remains after cleanup retry: %v", err)
	}
}

func TestExplicitConfigSuppressesUserConfiguration(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, ".tmux.conf"),
		[]byte("set -g @libtmux_user_config loaded\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	control, _ := newServer(context.Background(), t, "", true, false)
	if result := runCommand(context.Background(), control, "show-option", "-gv", "@libtmux_user_config"); result.ExitCode != 0 || !slices.Equal(result.Stdout, []string{"loaded"}) {
		t.Fatalf("control server did not load user config: %#v", result)
	}

	isolated, _ := newServer(context.Background(), t, "", true, true)
	if result := runCommand(context.Background(), isolated, "show-option", "-gv", "@libtmux_user_config"); slices.Contains(result.Stdout, "loaded") {
		t.Fatalf("isolated server loaded user config: %#v", result)
	}
}
