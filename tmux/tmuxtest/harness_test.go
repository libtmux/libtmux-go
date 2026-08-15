//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestMainShortensBothTemporaryRoots(t *testing.T) {
	tmpRoot := os.Getenv("TMPDIR")
	if got := os.Getenv("GOTMPDIR"); got != tmpRoot {
		t.Fatalf("GOTMPDIR = %q, want TMPDIR %q", got, tmpRoot)
	}
	if !strings.HasPrefix(filepath.Base(tmpRoot), "ltg-") {
		t.Fatalf("temporary root = %q, want short suite root", tmpRoot)
	}
	if dir := t.TempDir(); !strings.HasPrefix(dir, tmpRoot+string(filepath.Separator)) {
		t.Fatalf("t.TempDir() = %q, outside suite root %q", dir, tmpRoot)
	}
}

func TestFailureGuidanceReportsSafeReproductionAndCleanup(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "owned-paths")
	secret := "private-fixture-value"
	command := exec.Command(os.Args[0], "-test.run=^TestFailureGuidanceHelper$")
	command.Env = append(
		os.Environ(),
		"LIBTMUX_FAILURE_HELPER=fail",
		"LIBTMUX_FAILURE_RECORD="+recordPath,
		"LIBTMUX_FAILURE_SECRET="+secret,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("failure helper exited successfully")
	}
	paths, readErr := os.ReadFile(recordPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	ownedPaths := strings.Split(strings.TrimSpace(string(paths)), "\n")
	if len(ownedPaths) != 2 {
		t.Fatalf("recorded paths = %q, want socket and config", ownedPaths)
	}
	for _, path := range ownedPaths {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("harness path remains after failed test: %v", statErr)
		}
	}

	text := string(output)
	wantCommand := "tmuxtest: reproduce: go test -run '^TestFailureGuidanceHelper$' ."
	if count := strings.Count(text, "tmuxtest: reproduce:"); count != 1 {
		t.Fatalf("reproduction guidance count = %d, want 1\n%s", count, text)
	}
	if !strings.Contains(text, wantCommand) {
		t.Fatalf("failure output omitted %q\n%s", wantCommand, text)
	}
	if !strings.Contains(text, "tmuxtest: harness-owned socket cleaned: yes") {
		t.Fatalf("failure output omitted cleanup status\n%s", text)
	}
	for _, forbidden := range append(ownedPaths, secret) {
		if strings.Contains(text, forbidden) {
			t.Fatalf("failure output exposed sensitive value %q\n%s", forbidden, text)
		}
	}
}

func TestFailureGuidanceStaysQuietAfterSuccess(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.v", "-test.run=^TestFailureGuidanceHelper$")
	command.Env = append(os.Environ(), "LIBTMUX_FAILURE_HELPER=success")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("success helper failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "tmuxtest:") {
		t.Fatalf("successful test emitted harness diagnostics\n%s", output)
	}
}

func TestHarnessSetupAndCleanupFailuresAreRedacted(t *testing.T) {
	modes := []struct {
		name       string
		wantStatus string
	}{
		{name: "resolution", wantStatus: "resolve tmux executable failed: not found"},
		{name: "startup", wantStatus: "create initial session failed: command failed"},
		{name: "cleanup", wantStatus: "harness-owned socket cleanup failed"},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			sentinel := "private-" + mode.name + "-value"
			fixtureRoot := filepath.Join(t.TempDir(), sentinel)
			if err := os.Mkdir(fixtureRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(fixtureRoot, "tmux-"+sentinel)
			recordPath := filepath.Join(t.TempDir(), "recorded-arguments")
			if mode.name != "resolution" {
				script := "#!/bin/sh\nprintf '%s\\n' \"$HARNESS_FAILURE_SENTINEL\"\n" +
					"printf '%s %s\\n' \"$HARNESS_FAILURE_SENTINEL\" \"$*\" >&2\n" +
					"printf '%s\\n' \"$*\" >> \"$HARNESS_FAILURE_RECORD\"\n" +
					"exit 41\n"
				if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			command := exec.Command(os.Args[0], "-test.run=^TestHarnessFailureRedactionHelper$")
			command.Env = append(
				os.Environ(),
				"LIBTMUX_REDACTION_HELPER="+mode.name,
				"LIBTMUX_REDACTION_BINARY="+binary,
				"LIBTMUX_REDACTION_RECORD="+recordPath,
				"LIBTMUX_REDACTION_SENTINEL="+sentinel,
			)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatal("redaction helper exited successfully")
			}
			text := string(output)
			if !strings.Contains(text, mode.wantStatus) {
				t.Fatalf("failure output omitted status %q\n%s", mode.wantStatus, text)
			}
			for _, forbidden := range []string{sentinel, fixtureRoot, binary, recordPath} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("failure output exposed sensitive value %q\n%s", forbidden, text)
				}
			}
			if arguments, readErr := os.ReadFile(recordPath); readErr == nil {
				for _, argument := range strings.Fields(string(arguments)) {
					if filepath.IsAbs(argument) && strings.Contains(text, argument) {
						t.Fatalf("failure output exposed tmux argument %q\n%s", argument, text)
					}
				}
			}
		})
	}
}

func TestHarnessFailureRedactionHelper(t *testing.T) {
	mode := os.Getenv("LIBTMUX_REDACTION_HELPER")
	if mode == "" {
		return
	}
	sentinel := os.Getenv("LIBTMUX_REDACTION_SENTINEL")
	binary := os.Getenv("LIBTMUX_REDACTION_BINARY")
	if mode == "resolution" {
		t.Setenv("PATH", filepath.Dir(binary))
		tmuxtest.NewServer(context.Background(), t)
		return
	}
	initialSession := (*tmux.NewSessionRequest)(nil)
	if mode == "startup" {
		initialSession = &tmux.NewSessionRequest{Name: sentinel}
	}
	tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		Binary:         binary,
		Config:         []byte("set -g @private " + sentinel + "\n"),
		InitialSession: initialSession,
		ProcessEnvironment: []string{
			"HARNESS_FAILURE_RECORD=" + os.Getenv("LIBTMUX_REDACTION_RECORD"),
			"HARNESS_FAILURE_SENTINEL=" + sentinel,
			"PRIVATE_VALUE=" + sentinel,
		},
	})
	t.Fatal("intentional failure")
}

func TestFailureGuidanceHelper(t *testing.T) {
	mode := os.Getenv("LIBTMUX_FAILURE_HELPER")
	if mode == "" {
		return
	}
	secret := os.Getenv("LIBTMUX_FAILURE_SECRET")
	config := []byte("set -g @failure-guidance " + secret + "\n")
	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		Config:             config,
		ProcessEnvironment: []string{"PATH=" + os.Getenv("PATH"), "PRIVATE_VALUE=" + secret},
		InitialSession:     &tmux.NewSessionRequest{Name: "work"},
	})
	if recordPath := os.Getenv("LIBTMUX_FAILURE_RECORD"); recordPath != "" {
		paths := server.SocketPath() + "\n" + server.ConfigFile() + "\n"
		if err := os.WriteFile(recordPath, []byte(paths), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "fail" {
		t.Run("unsafe ; subtest", func(t *testing.T) {
			t.Fatal("intentional failure")
		})
	}
}

// libtmux:parity libtmux.pytest_plugin.TestServer
// libtmux:parity libtmux.pytest_plugin.server
//
//libtmux:real-tmux
func TestNewServerIssuesShortIsolatedSocketAndConfig(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	socketPath := server.SocketPath()
	if len([]byte(socketPath)) > 103 {
		t.Fatalf("socket path uses %d bytes, want at most 103", len([]byte(socketPath)))
	}
	if got := mustCmd(t, server, "display-message", "-p", "#{socket_path}"); got != socketPath {
		t.Fatalf("tmux socket path = %q, want %q", got, socketPath)
	}
	if configFile := server.ConfigFile(); filepath.Dir(configFile) != filepath.Dir(socketPath) {
		t.Fatalf("config file = %q, want file beside socket %q", configFile, socketPath)
	}
	if got := mustCmd(t, server, "display-message", "-p", "#{session_name}"); got != "work" {
		t.Fatalf("initial session = %q, want work", got)
	}
}

func TestCleanupRemovesSocketAfterNormalReturn(t *testing.T) {
	var socketPath string
	var pid int
	t.Run("lifetime", func(t *testing.T) {
		server := tmuxtest.NewServer(context.Background(), t)
		socketPath = server.SocketPath()
		pid = mustPID(t, server)
	})
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after cleanup: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("tmux server pid %d remains alive after cleanup", pid)
	}
}

func TestServersUseIndependentConfigFiles(t *testing.T) {
	first := tmuxtest.NewServer(context.Background(), t)
	if err := os.WriteFile(first.ConfigFile(), []byte("set -g @config_leak yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := tmuxtest.NewServer(context.Background(), t)
	if second.ConfigFile() == first.ConfigFile() {
		t.Fatalf("servers share config file %q", first.ConfigFile())
	}
	result := mustResult(t, second, "show-option", "-gv", "@config_leak")
	if slices.Contains(result.Stdout, "yes") {
		t.Fatalf("second server loaded first config value %#v", result.Stdout)
	}
}

func TestNewServerPinsResolvedTmuxBinary(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	t.Setenv("PATH", t.TempDir())

	if got := mustCmd(t, server, "display-message", "-p", "#{session_name}"); got != "work" {
		t.Fatalf("initial session after PATH change = %q, want work", got)
	}
}

func TestCleanupRemovesSocketAfterServerCrash(t *testing.T) {
	var socketPath string
	t.Run("lifetime", func(t *testing.T) {
		server := tmuxtest.NewServer(context.Background(), t)
		socketPath = server.SocketPath()
		pid, err := strconv.Atoi(mustCmd(t, server, "display-message", "-p", "#{pid}"))
		if err != nil {
			t.Fatal(err)
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		if err := process.Kill(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	})
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket remains after cleanup: %v", err)
	}
}

func TestChildEnvironmentIgnoresInheritedTmuxPane(t *testing.T) {
	t.Setenv("TMUX", "/tmp/foreign,424242,7")
	t.Setenv("TMUX_PANE", "%999999")
	t.Setenv("TMUX_TMPDIR", "/tmp/foreign-tmux")

	server := tmuxtest.NewServer(context.Background(), t)
	mustCmd(t, server, "new-session", "-d", "-s", "other")
	baseline := mustCmd(t, server, "display-message", "-p", "#{pane_id}")
	work := mustCmd(t, server, "display-message", "-p", "-t", "work:", "#{pane_id}")
	other := mustCmd(t, server, "display-message", "-p", "-t", "other:", "#{pane_id}")
	injected := work
	if injected == baseline {
		injected = other
	}
	if injected == baseline {
		t.Fatalf("could not find non-default pane: baseline=%q work=%q other=%q", baseline, work, other)
	}
	t.Setenv("TMUX_PANE", injected)
	contaminated := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         server.SocketPath(),
		ConfigFile:         server.ConfigFile(),
		ProcessEnvironment: os.Environ(),
	})
	if got := mustCmd(t, contaminated, "display-message", "-p", "#{pane_id}"); got != injected {
		t.Fatalf("un-scrubbed control pane = %q, want inherited pane %q", got, injected)
	}
	if got := mustCmd(t, server, "display-message", "-p", "#{pane_id}"); got != baseline {
		t.Fatalf("default pane = %q after inherited pane %q, want %q", got, injected, baseline)
	}
}

func TestServersAreParallelIsolated(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[string]string)
	for i := range 16 {
		name := fmt.Sprintf("server-%02d", i)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := tmuxtest.NewServer(context.Background(), t)
			mustCmd(t, server, "set-option", "-g", "@owner", name)
			if got := mustCmd(t, server, "show-option", "-gv", "@owner"); got != name {
				t.Fatalf("@owner = %q, want %q", got, name)
			}

			mu.Lock()
			defer mu.Unlock()
			if previous, ok := seen[server.SocketPath()]; ok {
				t.Fatalf("socket %q reused by %s and %s", server.SocketPath(), previous, name)
			}
			seen[server.SocketPath()] = name
		})
	}
}

func mustCmd(t *testing.T, server tmux.Server, args ...string) string {
	t.Helper()
	result := mustResult(t, server, args...)
	if result.ExitCode != 0 {
		t.Fatalf("tmux %s exited %d: %s", strings.Join(args, " "), result.ExitCode, strings.Join(result.Stderr, "\n"))
	}
	if len(result.Stdout) == 0 {
		return ""
	}
	return result.Stdout[0]
}

func mustResult(t *testing.T, server tmux.Server, args ...string) tmux.CommandResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := server.Cmd(ctx, args...)
	if err != nil {
		t.Fatalf("tmux %s: %v", strings.Join(args, " "), err)
	}
	return result
}

func mustPID(t *testing.T, server tmux.Server) int {
	t.Helper()
	pid, err := strconv.Atoi(mustCmd(t, server, "display-message", "-p", "#{pid}"))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
