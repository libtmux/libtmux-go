//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.window.Window.respawn
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:kill:c73eb1e87efe
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:shell:613b2dd997a5
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:start_directory:d91549582997
//
//libtmux:real-tmux
func TestWindowRespawnAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, beta, alphaWindow, betaWindow := newLinkedProcessWindow(ctx, t, server, "window-respawn")
	if err := beta.SetEnvironment(ctx, "RESPAWN_CONTEXT", "beta", tmux.SetEnvironmentOptions{}); err != nil {
		t.Fatalf("SetEnvironment() error = %v", err)
	}
	if _, err := alphaWindow.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight,
		Command:   "sleep 30",
	}); err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	alphaWindow = processWindowView(ctx, t, server, alphaWindow.SessionID(), alphaWindow.ID())
	betaWindow = processWindowView(ctx, t, server, beta.ID(), betaWindow.ID())
	before := alphaWindow.Panes()
	if len(before) != 2 {
		t.Fatalf("panes before Respawn() = %#v, want two", before)
	}

	startDirectory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(startDirectory, "respawn-context")
	command := "printf '%s|%s|%s' \"$RESPAWN_CONTEXT\" \"$REQUEST_VALUE\" \"$PWD\" > " +
		strconv.Quote(outputPath) + "; sleep 30"
	respawned, err := betaWindow.Respawn(ctx, tmux.RespawnRequest{
		Command:        &command,
		StartDirectory: &startDirectory,
		Environment:    map[string]string{"REQUEST_VALUE": "request"},
		Kill:           true,
	})
	if err != nil {
		t.Fatalf("Respawn() error = %v", err)
	}
	if respawned.ID() != betaWindow.ID() {
		t.Fatalf("Respawn() WindowID = %s, want stable %s", respawned.ID(), betaWindow.ID())
	}
	if got := waitForProcessFile(ctx, t, outputPath); got != "beta|request|"+startDirectory {
		t.Fatalf("respawn context = %q, want beta|request|%s", got, startDirectory)
	}

	alphaWindow = processWindowView(ctx, t, server, alphaWindow.SessionID(), alphaWindow.ID())
	betaWindow = processWindowView(ctx, t, server, beta.ID(), betaWindow.ID())
	if panes := alphaWindow.Panes(); len(panes) != 1 || panes[0].ID() != before[0].ID() {
		t.Fatalf("alpha panes after Respawn() = %#v, want retained first pane %s", panes, before[0].ID())
	}
	if panes := betaWindow.Panes(); len(panes) != 1 || panes[0].ID() != before[0].ID() {
		t.Fatalf("beta panes after Respawn() = %#v, want same linked physical pane", panes)
	}
}

func newLinkedProcessWindow(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	prefix string,
) (tmux.Session, tmux.Session, tmux.Window, tmux.Window) {
	t.Helper()
	alpha, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: prefix + "-alpha", Command: "sleep 30",
	})
	if err != nil {
		t.Fatalf("NewSession(alpha) error = %v", err)
	}
	beta, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: prefix + "-beta", Command: "sleep 30",
	})
	if err != nil {
		t.Fatalf("NewSession(beta) error = %v", err)
	}
	alphaWindow := soleProcessWindow(ctx, t, server, alpha.ID())
	index := 5
	if err := alphaWindow.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: beta.ID(),
		TargetIndex:   &index,
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	betaWindow := processWindowView(ctx, t, server, beta.ID(), alphaWindow.ID())
	return alpha, beta, alphaWindow, betaWindow
}

func soleProcessWindow(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	sessionID tmux.SessionID,
) tmux.Window {
	t.Helper()
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	session, err := snapshot.SessionByID(sessionID)
	if err != nil {
		t.Fatalf("SessionByID(%s) error = %v", sessionID, err)
	}
	windows := session.Windows()
	if len(windows) != 1 {
		t.Fatalf("session %s windows = %#v, want one", sessionID, windows)
	}
	return windows[0]
}

func processWindowView(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	sessionID tmux.SessionID,
	windowID tmux.WindowID,
) tmux.Window {
	t.Helper()
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	session, err := snapshot.SessionByID(sessionID)
	if err != nil {
		t.Fatalf("SessionByID(%s) error = %v", sessionID, err)
	}
	for _, window := range session.Windows() {
		if window.ID() == windowID {
			return window
		}
	}
	t.Fatalf("session %s has no window %s", sessionID, windowID)
	return tmux.Window{}
}

func waitForProcessFile(ctx context.Context, t *testing.T, path string) string {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) != 0 {
			return string(data)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("file %s was not populated before deadline: %v", filepath.Base(path), ctx.Err())
		case <-ticker.C:
		}
	}
}
