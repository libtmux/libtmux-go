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
func TestMaterializedSessionRejectsDaemonReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := tmuxtest.NewServer(ctx, t)
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessions := snapshot.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("snapshot sessions = %d, want 1", len(sessions))
	}
	stale, err := sessions[0].Rename(ctx, "before-replacement")
	if err != nil {
		t.Fatalf("guarded rename on original daemon: %v", err)
	}

	result, err := server.Cmd(ctx, "kill-server")
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
	if _, err := stale.Rename(ctx, "must-not-reach-replacement"); !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("stale Rename() error = %v, want ErrDaemonReplaced", err)
	}

	replacement, err = replacement.Refresh(ctx)
	if err != nil {
		t.Fatalf("refresh replacement session: %v", err)
	}
	if name, ok := replacement.Name(); !ok || name != "replacement" {
		t.Fatalf("replacement name = (%q, %t), want (replacement, true)", name, ok)
	}
}
