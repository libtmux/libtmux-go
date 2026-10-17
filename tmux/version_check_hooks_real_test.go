package tmux_test

import (
	"context"
	"strings"
	"testing"
	"time"

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
