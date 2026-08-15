//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.session.Session.from_env
// libtmux:parity libtmux.session.Session.from_env#parameter-branch:env:496823e938f7
//
// libtmux:parity libtmux.pane.Pane.from_env
// libtmux:parity libtmux.window.Window.from_env
// libtmux:parity libtmux.window.Window.from_env#parameter-branch:env:d6354f13d1ea
//
//libtmux:real-tmux
func TestFromEnvDiscoversRealPaneAndContainingHierarchy(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	snapshot := mustRealSnapshot(t, server)
	wantPane := snapshot.Panes()[0]
	env := map[string]string{
		"TMUX":      fmt.Sprintf("%s,stale-pid,999", server.SocketPath()),
		"TMUX_PANE": wantPane.ID().String(),
	}

	discoveredServer, err := tmux.NewServerFromEnv(env)
	if err != nil {
		t.Fatalf("NewServerFromEnv() error = %v", err)
	}
	if discoveredServer.SocketPath() != server.SocketPath() {
		t.Fatalf("NewServerFromEnv().SocketPath() = %q, want %q", discoveredServer.SocketPath(), server.SocketPath())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pane, err := tmux.PaneFromEnv(ctx, env)
	if err != nil {
		t.Fatalf("PaneFromEnv() error = %v", err)
	}
	window, err := tmux.WindowFromEnv(ctx, env)
	if err != nil {
		t.Fatalf("WindowFromEnv() error = %v", err)
	}
	session, err := tmux.SessionFromEnv(ctx, env)
	if err != nil {
		t.Fatalf("SessionFromEnv() error = %v", err)
	}
	if pane.ID() != wantPane.ID() || pane.WindowID() != wantPane.WindowID() || pane.SessionID() != wantPane.SessionID() {
		t.Fatalf("PaneFromEnv() = %#v, want pane hierarchy %#v", pane, wantPane)
	}
	if window.ID() != wantPane.WindowID() || window.SessionID() != wantPane.SessionID() {
		t.Fatalf("WindowFromEnv() = %#v, want containing window", window)
	}
	if session.ID() != wantPane.SessionID() {
		t.Fatalf("SessionFromEnv().ID() = %s, want %s; stale TMUX session was used", session.ID(), wantPane.SessionID())
	}
}

//libtmux:real-tmux
func TestPaneFromEnvReportsMissingPaneOnLiveServer(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	result := mustRealCommand(t, server, "split-window", "-d", "-P", "-F", "#{pane_id}")
	if len(result.Stdout) != 1 {
		t.Fatalf("split-window stdout = %#v, want pane id", result.Stdout)
	}
	env := map[string]string{
		"TMUX":      fmt.Sprintf("%s,1,0", server.SocketPath()),
		"TMUX_PANE": result.Stdout[0],
	}
	mustRealCommand(t, server, "kill-pane", "-t", result.Stdout[0])

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := tmux.PaneFromEnv(ctx, env)
	if !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("PaneFromEnv() error = %v, want ErrSnapshotNotFound", err)
	}
	if errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("PaneFromEnv() error = %v, live-server absence must not be ErrCommand", err)
	}
}
