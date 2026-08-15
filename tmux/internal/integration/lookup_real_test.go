//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.pane.Pane.from_pane_id
// libtmux:parity libtmux.window.Window.from_window_id
//
//libtmux:real-tmux
func TestLiveWindowAndPaneResolutionUsesTmuxCanonicalWinlink(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	initial := mustRealSnapshot(t, server)
	shared := initial.Windows()[0]
	sharedPane := shared.Panes()[0]

	mustRealCommand(t, server, "new-session", "-d", "-s", "beta", "-n", "own")
	mustRealCommand(t, server, "link-window", "-s", shared.ID().String(), "-t", "beta:7")
	mustRealCommand(t, server, "link-window", "-s", shared.ID().String(), "-t", "beta:9")
	mustRealCommand(t, server, "select-window", "-t", "beta:9")

	snapshot := mustRealSnapshot(t, server)
	if _, err := snapshot.WindowByID(shared.ID()); !errors.Is(err, tmux.ErrSnapshotAmbiguous) {
		t.Fatalf("linked snapshot WindowByID() error = %v, want ErrSnapshotAmbiguous", err)
	}
	if _, err := snapshot.PaneByID(sharedPane.ID()); !errors.Is(err, tmux.ErrSnapshotAmbiguous) {
		t.Fatalf("linked snapshot PaneByID() error = %v, want ErrSnapshotAmbiguous", err)
	}

	wantSession, wantIndex := realCanonicalWinlink(t, server, shared.ID().String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	window, err := server.Window(ctx, shared.ID())
	if err != nil {
		t.Fatalf("Window() error = %v", err)
	}
	if window.SessionID() != wantSession || window.Index() != wantIndex {
		t.Fatalf("Window() winlink = %s:%d, want tmux canonical %s:%d", window.SessionID(), window.Index(), wantSession, wantIndex)
	}
	pane, err := server.Pane(ctx, sharedPane.ID())
	if err != nil {
		t.Fatalf("Pane() error = %v", err)
	}
	if pane.SessionID() != wantSession || pane.WindowIndex() != wantIndex {
		t.Fatalf("Pane() winlink = %s:%d, want tmux canonical %s:%d", pane.SessionID(), pane.WindowIndex(), wantSession, wantIndex)
	}

	var staleWindow tmux.Window
	var stalePane tmux.Pane
	for _, candidate := range snapshot.WindowsByID(shared.ID()) {
		if candidate.SessionID() != wantSession || candidate.Index() != wantIndex {
			staleWindow = candidate
			panes := candidate.Panes()
			if len(panes) == 1 {
				stalePane = panes[0]
			}
			break
		}
	}
	refreshedWindow, err := staleWindow.Refresh(ctx)
	if err != nil {
		t.Fatalf("Window.Refresh() error = %v", err)
	}
	if refreshedWindow.SessionID() != wantSession || refreshedWindow.Index() != wantIndex {
		t.Fatalf("Window.Refresh() winlink = %s:%d, want %s:%d", refreshedWindow.SessionID(), refreshedWindow.Index(), wantSession, wantIndex)
	}
	refreshedPane, err := stalePane.Refresh(ctx)
	if err != nil {
		t.Fatalf("Pane.Refresh() error = %v", err)
	}
	if refreshedPane.SessionID() != wantSession || refreshedPane.WindowIndex() != wantIndex {
		t.Fatalf("Pane.Refresh() winlink = %s:%d, want %s:%d", refreshedPane.SessionID(), refreshedPane.WindowIndex(), wantSession, wantIndex)
	}
}

// libtmux:parity libtmux.pane.Pane.session
// libtmux:parity libtmux.pane.Pane.window
// libtmux:parity libtmux.session.Session.active_pane
// libtmux:parity libtmux.session.Session.active_window
// libtmux:parity libtmux.window.Window.active_pane
// libtmux:parity libtmux.window.Window.session
//
// libtmux:real-tmux
func TestLiveRelationshipResolversAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	initial := mustRealSnapshot(t, server)
	wantSession := initial.Sessions()[0]
	wantWindow, ok := wantSession.ActiveWindow()
	if !ok {
		t.Fatal("initial session has no active window")
	}
	wantPane, ok := wantWindow.ActivePane()
	if !ok {
		t.Fatal("initial window has no active pane")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pointSession, err := server.Session(ctx, wantSession.ID())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	activeWindow, err := pointSession.ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatalf("Session.ResolveActiveWindow() error = %v", err)
	}
	if activeWindow.SessionID() != wantWindow.SessionID() || activeWindow.ID() != wantWindow.ID() || activeWindow.Index() != wantWindow.Index() {
		t.Fatalf("Session.ResolveActiveWindow() = %#v, want %#v", activeWindow, wantWindow)
	}
	activePane, found, err := pointSession.ResolveActivePane(ctx)
	if err != nil {
		t.Fatalf("Session.ResolveActivePane() error = %v", err)
	}
	if !found || activePane.ID() != wantPane.ID() || activePane.WindowIndex() != wantPane.WindowIndex() {
		t.Fatalf("Session.ResolveActivePane() = (%#v, %t), want %#v", activePane, found, wantPane)
	}

	pointWindow, err := server.Window(ctx, wantWindow.ID())
	if err != nil {
		t.Fatalf("Window() error = %v", err)
	}
	parentSession, err := pointWindow.ResolveSession(ctx)
	if err != nil {
		t.Fatalf("Window.ResolveSession() error = %v", err)
	}
	if parentSession.ID() != pointWindow.SessionID() {
		t.Fatalf("Window.ResolveSession() = %#v, want %s", parentSession, pointWindow.SessionID())
	}
	windowPane, found, err := pointWindow.ResolveActivePane(ctx)
	if err != nil {
		t.Fatalf("Window.ResolveActivePane() error = %v", err)
	}
	if !found || windowPane.ID() != wantPane.ID() || windowPane.WindowIndex() != pointWindow.Index() {
		t.Fatalf("Window.ResolveActivePane() = (%#v, %t), want pane %s in index %d", windowPane, found, wantPane.ID(), pointWindow.Index())
	}

	pointPane, err := server.Pane(ctx, wantPane.ID())
	if err != nil {
		t.Fatalf("Pane() error = %v", err)
	}
	parentWindow, err := pointPane.ResolveWindow(ctx)
	if err != nil {
		t.Fatalf("Pane.ResolveWindow() error = %v", err)
	}
	if parentWindow.SessionID() != pointPane.SessionID() || parentWindow.ID() != pointPane.WindowID() || parentWindow.Index() != pointPane.WindowIndex() {
		t.Fatalf("Pane.ResolveWindow() = %#v, want exact point pane winlink %#v", parentWindow, pointPane)
	}
	paneSession, err := pointPane.ResolveSession(ctx)
	if err != nil {
		t.Fatalf("Pane.ResolveSession() error = %v", err)
	}
	if paneSession.ID() != pointPane.SessionID() {
		t.Fatalf("Pane.ResolveSession() = %#v, want %s", paneSession, pointPane.SessionID())
	}
}

func mustRealSnapshot(t *testing.T, server tmux.Server) tmux.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	return snapshot
}

func realCanonicalWinlink(t *testing.T, server tmux.Server, target string) (tmux.SessionID, int) {
	t.Helper()
	result := mustRealCommand(t, server, "display-message", "-t", target, "-p", "#{session_id}:#{window_index}")
	if len(result.Stdout) != 1 {
		t.Fatalf("canonical winlink output = %#v, want one line", result.Stdout)
	}
	session, indexText, found := strings.Cut(result.Stdout[0], ":")
	if !found {
		t.Fatalf("canonical winlink output = %q, want session:index", result.Stdout[0])
	}
	index, err := strconv.Atoi(indexText)
	if err != nil {
		t.Fatalf("canonical winlink index %q: %v", indexText, err)
	}
	if session == "" {
		t.Fatal("canonical winlink has empty session")
	}
	return tmux.SessionID(session), index
}
