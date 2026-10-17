package tmux_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// Naming the control client is part of acquisition, so it must not ask tmux
// with a command whose after-hook a user may have armed.
//
//libtmux:real-tmux
func TestControlAcquisitionFiresNoListClientsHook(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	tmuxtest.RequireVersion(ctx, t, server, tmux.MinimumConnectionVersion)
	tmuxtest.RequireVersion(ctx, t, server, "3.7")
	run := func(arguments ...string) string {
		t.Helper()
		result, err := server.Cmd(ctx, arguments...)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("%s = (%+v, %v)", strings.Join(arguments, " "), result, err)
		}
		return strings.Join(result.Stdout, "\n")
	}
	run("new-session", "-d", "-s", "anchor", "sleep 60")
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("Sessions() = (%v, %v)", sessions, err)
	}
	const marker = "@libtmux_hook_after_list_clients"
	run("set-hook", "-g", "after-list-clients", "set-option -g "+marker+" fired")
	connection, err := sessions[0].OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	defer func() { _ = connection.Close() }()

	if got := run("show-options", "-gqv", marker); got != "" {
		t.Errorf("control acquisition fired after-list-clients: %q", got)
	}
	// Control: the hook is live.
	run("list-clients")
	if got := run("show-options", "-gqv", marker); got != "fired" {
		t.Errorf("list-clients control did not fire the hook: %q", got)
	}
}
