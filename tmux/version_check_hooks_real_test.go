package tmux_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// The version check runs before every command on an unbound handle, so a probe that
// fires a user's hook has effects before any refusal.
//
//libtmux:real-tmux
func TestVersionCheckFiresNoUserHooks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	if result, err := server.Cmd(ctx, "new-session", "-d", "-s", "anchor", "sleep 300"); err != nil || result.ExitCode != 0 {
		t.Fatalf("start daemon = (%#v, %v), want exit 0", result, err)
	}
	const (
		afterDisplayMessage = "@libtmux_hook_after_display_message"
		commandError        = "@libtmux_hook_command_error"
	)
	cmd := func(arguments ...string) (string, int) {
		t.Helper()
		result, err := server.Cmd(ctx, arguments...)
		if err != nil {
			t.Fatalf("%s: %v", strings.Join(arguments, " "), err)
		}
		return strings.Join(result.Stdout, "\n"), result.ExitCode
	}
	fired := func(option string) string {
		t.Helper()
		value, exitCode := cmd("show-options", "-gqv", option)
		if exitCode != 0 {
			t.Fatalf("show-options %s exit = %d", option, exitCode)
		}
		return value
	}

	if _, exitCode := cmd("set-hook", "-g", "after-display-message", "set-option -g "+afterDisplayMessage+" fired"); exitCode != 0 {
		t.Fatalf("after-display-message hook must exist on every supported release, exit = %d", exitCode)
	}
	// command-error exists from tmux 3.5; older releases refuse the hook.
	_, commandErrorExit := cmd("set-hook", "-g", "command-error", "set-option -g "+commandError+" fired")
	hasCommandError := commandErrorExit == 0

	// Every command below runs the version check first: each makes the handle probe
	// the daemon again, and none of them is display-message or fails.
	for range 3 {
		cmd("list-sessions")
	}
	// A mirrored layout needs the daemon version, which layout validation
	// reads without dispatching anything. Releases before 3.5 refuse it.
	if err := server.ValidateLayouts(ctx, func(yield func(string, int) bool) {
		yield("main-horizontal-mirrored", 1)
	}); err != nil && !errors.Is(err, tmux.ErrVersionTooLow) {
		t.Fatalf("ValidateLayouts() = %v", err)
	}
	if got := fired(afterDisplayMessage); got != "" {
		t.Errorf("version check fired after-display-message: %q", got)
	}
	if got := fired(commandError); got != "" {
		t.Errorf("version check fired command-error: %q", got)
	}

	// Controls: the hooks are live, and the probe's absence of effects is not
	// merely because they cannot fire.
	cmd("display-message", "-p", "control")
	if got := fired(afterDisplayMessage); got != "fired" {
		t.Errorf("display-message control did not fire after-display-message: %q", got)
	}
	if hasCommandError {
		if _, exitCode := cmd("kill-session", "-t", "=libtmux-missing"); exitCode == 0 {
			t.Fatal("kill-session of a missing session unexpectedly succeeded")
		}
		if got := fired(commandError); got != "fired" {
			t.Errorf("failing-command control did not fire command-error: %q", got)
		}
	}
}

// The probe reads the socket path back through tmux's own parser, so bytes the
// parser treats specially must survive on every release.
//
//libtmux:real-tmux
func TestVersionCheckRecoversUnusualSocketPaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	executable := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{}).Executable()
	for _, name := range []string{"a b", "a'b", `a"b`, "a;b", "a#b", "a$b", "a~b", "a|b", "a=b", `a\b`, "a{b}", "a}{b", "a,b", "a''b"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			server, err := tmux.NewServer(tmux.ServerOptions{
				Binary:     executable,
				SocketPath: filepath.Join(root, name),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = server.Cmd(context.Background(), "kill-server") })
			if _, err := server.Cmd(ctx, "new-session", "-d", "-s", "anchor", "sleep 60"); err != nil {
				t.Fatalf("start daemon: %v", err)
			}
			// A path recovered wrongly would fail the replaced-daemon fence.
			session, err := server.Cmd(ctx, "list-sessions", "-F", "#{session_name}")
			if err != nil || session.ExitCode != 0 || strings.Join(session.Stdout, "") != "anchor" {
				t.Fatalf("list-sessions = (%#v, %v)", session, err)
			}
		})
	}
}

// An unbound handle reuses its checked daemon, so a daemon that is replaced or
// gone must be noticed by the fence and the next command must still start or
// find the new one.
//
//libtmux:real-tmux
func TestVersionCheckCacheSurvivesDaemonReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	run := func(arguments ...string) string {
		t.Helper()
		result, err := server.Cmd(ctx, arguments...)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("%s = (%+v, %v)", strings.Join(arguments, " "), result, err)
		}
		return strings.Join(result.Stdout, "\n")
	}
	run("new-session", "-d", "-s", "first", "sleep 60")
	run("list-sessions")
	run("kill-server")
	waitForSocketRelease(ctx, t, server)
	// The cached identity now names a dead daemon: new-session must start a
	// fresh one rather than fail on the -N client.
	run("new-session", "-d", "-s", "second", "sleep 60")
	if got := run("list-sessions", "-F", "#{session_name}"); got != "second" {
		t.Fatalf("sessions after replacement = %q, want only second", got)
	}
}

// waitForSocketRelease waits until a killed daemon's socket stops accepting
// connections. kill-server returns before the daemon is gone, and a pane's
// child can hold the listening socket a little longer, so a client that
// connects in that window is reset with "server exited unexpectedly".
func waitForSocketRelease(ctx context.Context, t *testing.T, server tmux.Server) {
	t.Helper()
	for {
		output, err := exec.CommandContext(ctx, server.Executable(), "-S", server.SocketPath(), "list-sessions").CombinedOutput()
		if err != nil && !strings.Contains(string(output), "server exited unexpectedly") {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("the killed daemon's socket still accepts connections: %q", output)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
