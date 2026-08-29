package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestConnectionOwnsTerminalControlLanes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	connection, err := sessions[0].OpenControl(ctx, tmux.ConnectionOptions{Lanes: 2})
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	if connection.Lanes() != 2 {
		t.Fatalf("Lanes() = %d, want 2", connection.Lanes())
	}

	renamed, err := connection.Session().Rename(ctx, "connected")
	if err != nil {
		t.Fatalf("connected Rename() error = %v", err)
	}
	if _, err := connection.Server().ShowBufferBytes(ctx, nil); !errors.Is(
		err,
		tmux.ErrConnectionRequiresProcess,
	) {
		t.Fatalf("ShowBufferBytes() error = %v, want process refusal", err)
	}
	closeCtx, cancelClose := context.WithCancel(context.Background())
	cancelClose()
	if err := connection.CloseContext(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext(canceled) error = %v, want context canceled", err)
	}
	if _, err := renamed.Refresh(ctx); !errors.Is(err, tmux.ErrControlClosed) {
		t.Fatalf("bound Refresh() error = %v, want ErrControlClosed", err)
	}
	if err := connection.CloseContext(ctx); err != nil {
		t.Fatalf("resumed CloseContext() error = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() after shutdown error = %v", err)
	}

	result, err := server.Cmd(ctx, "display-message", "-p", "still-alive")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("unbound Cmd() = (%#v, %v), want live daemon", result, err)
	}
}

//libtmux:real-tmux
func TestConnectionAtomicallyRejectsDaemonReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	stale := sessions[0]
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
	const attachedMarker = "@libtmux-stale-control-attached"
	result, err = server.Cmd(
		ctx,
		"set-hook",
		"-g",
		"client-attached",
		"set-option -g "+attachedMarker+" reached",
	)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("install replacement attach hook = (%#v, %v)", result, err)
	}

	connection, err := stale.OpenControl(ctx, tmux.ConnectionOptions{})
	if connection != nil {
		_ = connection.Close()
	}
	if !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("stale OpenControl() = (%#v, %v), want ErrDaemonReplaced", connection, err)
	}
	marker, markerErr := server.Cmd(ctx, "show-options", "-gqv", attachedMarker)
	if markerErr != nil || len(marker.Stdout) != 0 {
		t.Fatalf("replacement attach marker = (%#v, %v), want absent", marker, markerErr)
	}
	replacement, err = replacement.Refresh(ctx)
	if err != nil {
		t.Fatalf("refresh replacement: %v", err)
	}
	if name, ok := replacement.Name(); !ok || name != "replacement" {
		t.Fatalf("replacement name = (%q, %t), want (replacement, true)", name, ok)
	}
}
