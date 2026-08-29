package tmux_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestMaterializedSessionRejectsAliasedDaemonReplacementMarker(t *testing.T) {
	const (
		aliasSideEffect      = "@libtmux_guard_alias_side_effect"
		oldReplacementMarker = "__libtmux_daemon_replaced_1__"
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	result, err := server.Cmd(ctx, "new-session", "-d", "-s", "work")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("start original daemon = (%#v, %v), want exit 0", result, err)
	}
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessions := snapshot.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("snapshot sessions = %d, want 1", len(sessions))
	}
	stale := sessions[0]

	result, err = server.Cmd(ctx, "kill-server")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("kill-server = (%#v, %v), want exit 0", result, err)
	}
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		alive, aliveErr := server.IsAlive(ctx)
		return !alive, aliveErr
	}); err != nil {
		t.Fatalf("wait for original daemon exit: %v", err)
	}

	replacement, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "replacement"})
	if err != nil {
		t.Fatalf("start replacement daemon: %v", err)
	}
	result, err = server.Cmd(
		ctx,
		"set-option", "-s", "--", "command-alias[100]",
		oldReplacementMarker+"=set-option -g "+aliasSideEffect+" yes",
	)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("install replacement alias = (%#v, %v), want exit 0", result, err)
	}
	if _, err := stale.Rename(ctx, "must-not-reach-replacement"); !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("stale Rename() error = %v, want ErrDaemonReplaced", err)
	}
	if value, ok, err := server.RawOption(ctx, aliasSideEffect); err != nil {
		t.Fatalf("read replacement alias side effect: %v", err)
	} else if ok {
		t.Fatalf("replacement alias side effect = %q, want absent", value)
	}

	replacement, err = replacement.Refresh(ctx)
	if err != nil {
		t.Fatalf("refresh replacement session: %v", err)
	}
	if name, ok := replacement.Name(); !ok || name != "replacement" {
		t.Fatalf("replacement name = (%q, %t), want (replacement, true)", name, ok)
	}
}
