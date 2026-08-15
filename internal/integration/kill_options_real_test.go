//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.pane.Pane.kill
// libtmux:parity libtmux.pane.Pane.kill#parameter-branch:all_except:6a69b96ddb2d
// libtmux:parity libtmux.pane.Pane.kill#parameter-branch:all_except:6a69b96ddb2d:2
// libtmux:parity libtmux.window.Window.kill
// libtmux:parity libtmux.window.Window.kill#parameter-branch:all_except:6a69b96ddb2d
// libtmux:parity libtmux.window.Window.kill#parameter-branch:all_except:6a69b96ddb2d:2
//
//libtmux:real-tmux
func TestKillOthersAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "kill-others"})
	keep := tmuxtest.NewWindow(ctx, t, session, tmux.NewWindowRequest{})
	_ = tmuxtest.NewWindow(ctx, t, session, tmux.NewWindowRequest{})
	_ = tmuxtest.NewWindow(ctx, t, session, tmux.NewWindowRequest{})
	if err := keep.KillOthers(ctx); err != nil {
		t.Fatalf("Window.KillOthers() error = %v", err)
	}
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() after Window.KillOthers error = %v", err)
	}
	snapshotSession, err := snapshot.SessionByID(session.ID())
	if err != nil {
		t.Fatalf("SessionByID() after Window.KillOthers error = %v", err)
	}
	windows := snapshotSession.Windows()
	if len(windows) != 1 || windows[0].ID() != keep.ID() {
		t.Fatalf("windows after KillOthers = %#v, want only %s", windows, keep.ID())
	}

	first, err := keep.SplitPane(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatalf("first SplitPane() error = %v", err)
	}
	if _, err := keep.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		t.Fatalf("second SplitPane() error = %v", err)
	}
	if err := first.KillOthers(ctx); err != nil {
		t.Fatalf("Pane.KillOthers() error = %v", err)
	}
	snapshot, err = server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() after Pane.KillOthers error = %v", err)
	}
	snapshotWindow, ok := killOptionsWindow(snapshot, session.ID(), keep.ID())
	if !ok {
		t.Fatalf("window %s:%s missing after Pane.KillOthers", session.ID(), keep.ID())
	}
	panes := snapshotWindow.Panes()
	if len(panes) != 1 || panes[0].ID() != first.ID() {
		t.Fatalf("panes after KillOthers = %#v, want only %s", panes, first.ID())
	}
}

func killOptionsWindow(snapshot tmux.Snapshot, sessionID tmux.SessionID, windowID tmux.WindowID) (tmux.Window, bool) {
	for _, window := range snapshot.WindowsByID(windowID) {
		if window.SessionID() == sessionID {
			return window, true
		}
	}
	return tmux.Window{}, false
}

// libtmux:parity libtmux.session.Session.__exit__
// libtmux:parity libtmux.session.Session.kill#parameter-branch:group:7704a4e4922f
// libtmux:parity libtmux.session.Session.kill#version-branch:tmux-version:c6a18af85027
// libtmux:parity libtmux.session.Session.kill#warning:8abad26b205f
//
// libtmux:parity libtmux.session.Session.kill
//
//libtmux:real-tmux
func TestSessionKillGroupCompatibilityAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	base := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "kill-group-base"})
	result, err := server.Cmd(
		ctx,
		"new-session", "-d", "-t", base.ID().String(), "-s", "kill-group-peer",
		"-P", "-F#{session_id}",
	)
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 {
		t.Fatalf("create grouped peer = (%#v, %v), want one session id", result, err)
	}
	peerID := tmux.SessionID(result.Stdout[0])
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	minimum, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}
	if err := base.KillWith(ctx, tmux.SessionKillRequest{Group: true}); err != nil {
		t.Fatalf("KillWith(Group) error = %v", err)
	}
	if _, err := server.Session(ctx, base.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("base lookup error = %v, want ErrSnapshotNotFound", err)
	}
	_, peerErr := server.Session(ctx, peerID)
	if version.AtLeast(minimum) {
		if !errors.Is(peerErr, tmux.ErrSnapshotNotFound) {
			t.Fatalf("grouped peer lookup error = %v, want ErrSnapshotNotFound", peerErr)
		}
	} else if peerErr != nil {
		t.Fatalf("grouped peer should survive omitted -g on tmux %s: %v", version, peerErr)
	}
}

// libtmux:parity libtmux.session.Session.kill
// libtmux:parity libtmux.session.Session.kill#parameter-branch:all_except:6a69b96ddb2d
// libtmux:parity libtmux.session.Session.kill#parameter-branch:all_except:6a69b96ddb2d:2
//
//libtmux:real-tmux
func TestSessionKillAllExceptAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	keeper := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "kill-all-except-keeper"},
	)
	first := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "kill-all-except-first"},
	)
	second := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "kill-all-except-second"},
	)
	if err := keeper.KillWith(ctx, tmux.SessionKillRequest{AllExcept: true}); err != nil {
		t.Fatalf("KillWith(AllExcept) error = %v", err)
	}
	if _, err := server.Session(ctx, keeper.ID()); err != nil {
		t.Fatalf("keeper session lookup error = %v", err)
	}
	for _, killed := range []tmux.SessionID{first.ID(), second.ID()} {
		if _, err := server.Session(ctx, killed); !errors.Is(err, tmux.ErrSnapshotNotFound) {
			t.Fatalf("killed session %s lookup error = %v, want ErrSnapshotNotFound", killed, err)
		}
	}
}

// libtmux:parity libtmux.session.Session.kill#parameter-branch:clear:a18e02300191
//
//libtmux:real-tmux
func TestSessionClearAlertsLeavesSessionAndCurrentWindowAliveAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "clear-alerts"},
	)
	alertWindowIDValue, ok := session.Formats().WindowID()
	if !ok {
		t.Fatal("clear-alerts session has no initial window")
	}
	alertWindowID := alertWindowIDValue
	current := tmuxtest.NewWindow(ctx, t, session, tmux.NewWindowRequest{
		Name: killWindowName("current"), Attach: true,
	})
	mustRealCommand(t, server, "set-option", "-t", session.ID().String(), "activity-action", "other")
	mustRealCommand(t, server, "set-window-option", "-t", alertWindowID.String(), "monitor-activity", "on")
	setupSnapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() for ClearAlerts setup error = %v", err)
	}
	alertWindow, ok := killOptionsWindow(setupSnapshot, session.ID(), alertWindowID)
	if !ok || len(alertWindow.Panes()) != 1 {
		t.Fatalf("alert window panes = %#v, want one pane", alertWindow.Panes())
	}
	activityCommand := "printf clear-alerts-activity"
	if err := alertWindow.Panes()[0].SendKeys(ctx, tmux.SendKeysRequest{Command: &activityCommand}); err != nil {
		t.Fatalf("SendKeys(activity) error = %v", err)
	}
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		window, err := server.Window(ctx, alertWindowID)
		if err != nil {
			return false, err
		}
		activity, present := window.ActivityFlag()
		return present && activity, nil
	}); err != nil {
		t.Fatalf("wait for activity alert: %v", err)
	}
	beforeSnapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() before ClearAlerts error = %v", err)
	}
	before, err := beforeSnapshot.SessionByID(session.ID())
	if err != nil {
		t.Fatalf("SessionByID() before ClearAlerts error = %v", err)
	}
	beforeWindowID, ok := before.Formats().WindowID()
	if !ok || beforeWindowID != current.ID() {
		t.Fatalf("current window before ClearAlerts = %q, want %s", beforeWindowID, current.ID())
	}
	if activity := killOptionsActivityFlag(t, before, alertWindowID); !activity {
		t.Fatal("activity flag before ClearAlerts = false, want true")
	}

	if err := session.KillWith(ctx, tmux.SessionKillRequest{ClearAlerts: true}); err != nil {
		t.Fatalf("KillWith(ClearAlerts) error = %v", err)
	}
	afterSnapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() after ClearAlerts error = %v", err)
	}
	after, err := afterSnapshot.SessionByID(session.ID())
	if err != nil {
		t.Fatalf("SessionByID() after ClearAlerts error = %v", err)
	}
	afterWindowID, ok := after.Formats().WindowID()
	if !ok || afterWindowID != current.ID() {
		t.Fatalf("current window after ClearAlerts = %q, want %s", afterWindowID, current.ID())
	}
	if len(after.Windows()) != len(before.Windows()) {
		t.Fatalf("windows after ClearAlerts = %d, want %d", len(after.Windows()), len(before.Windows()))
	}
	if activity := killOptionsActivityFlag(t, after, alertWindowID); activity {
		t.Fatal("activity flag after ClearAlerts = true, want false")
	}
}

func killOptionsActivityFlag(t *testing.T, session tmux.Session, windowID tmux.WindowID) bool {
	t.Helper()
	for _, window := range session.Windows() {
		if window.ID() == windowID {
			activity, _ := window.ActivityFlag()
			return activity
		}
	}
	t.Fatalf("window %s is absent from session %s", windowID, session.ID())
	return false
}

// libtmux:parity libtmux.server.Server.kill_session
// libtmux:parity libtmux.session.Session.kill_window
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:3fc216416bbd
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:8f6d7a09cc34
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:c12246bcb583
//
//libtmux:real-tmux
func TestTargetedSessionAndWindowKillsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	targetSession := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "server-kill-target-long"},
	)
	if err := server.KillSession(ctx, "server-kill-target"); err != nil {
		t.Fatalf("KillSession(prefix) error = %v", err)
	}
	if _, err := server.Session(ctx, targetSession.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("target session lookup error = %v, want ErrSnapshotNotFound", err)
	}

	session := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "session-kill-window"},
	)
	initialWindow, ok := session.Formats().WindowID()
	if !ok {
		t.Fatal("new session has no active window projection")
	}
	initialWindowID := initialWindow
	index := 7
	indexed := tmuxtest.NewWindow(
		ctx, t, session, tmux.NewWindowRequest{Index: &index},
	)
	stringTarget := tmuxtest.NewWindow(
		ctx, t, session, tmux.NewWindowRequest{Name: killWindowName("string-target")},
	)
	keep := tmuxtest.NewWindow(
		ctx, t, session, tmux.NewWindowRequest{Name: killWindowName("survivor")},
	)

	if err := session.KillWindow(ctx, tmux.KillWindowRequest{Index: &index}); err != nil {
		t.Fatalf("KillWindow(Index) error = %v", err)
	}
	if _, err := server.Window(ctx, indexed.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("indexed window lookup error = %v, want ErrSnapshotNotFound", err)
	}

	name := "string-target"
	if err := session.KillWindow(ctx, tmux.KillWindowRequest{Target: &name}); err != nil {
		t.Fatalf("KillWindow(Target) error = %v", err)
	}
	if _, err := server.Window(ctx, stringTarget.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("string-target window lookup error = %v, want ErrSnapshotNotFound", err)
	}

	if err := session.KillWindow(ctx, tmux.KillWindowRequest{}); err != nil {
		t.Fatalf("KillWindow(current) error = %v", err)
	}
	if _, err := server.Window(ctx, initialWindowID); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("current window lookup error = %v, want ErrSnapshotNotFound", err)
	}
	if _, err := server.Window(ctx, keep.ID()); err != nil {
		t.Fatalf("survivor window lookup error = %v", err)
	}
}

func killWindowName(name string) *string { return &name }

//libtmux:real-tmux
func TestWindowKillOthersUsesLinkedReceiverSessionAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	alpha := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "kill-linked-alpha"},
	)
	shared := tmuxtest.NewWindow(
		ctx, t, alpha, tmux.NewWindowRequest{Name: killWindowName("shared")},
	)
	alphaExtra := tmuxtest.NewWindow(
		ctx, t, alpha, tmux.NewWindowRequest{Name: killWindowName("alpha-extra")},
	)
	beta := tmuxtest.NewSession(
		ctx, t, server, tmux.NewSessionRequest{Name: "kill-linked-beta"},
	)
	betaInitial, ok := beta.Formats().WindowID()
	if !ok {
		t.Fatal("beta session has no active window projection")
	}
	result, err := server.Cmd(
		ctx,
		"link-window", "-s", alpha.ID().String()+":"+shared.ID().String(),
		"-t", beta.ID().String()+":7",
	)
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("link shared window = (%#v, %v)", result, err)
	}

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() before linked KillOthers error = %v", err)
	}
	receiver, ok := killOptionsWindow(snapshot, beta.ID(), shared.ID())
	if !ok {
		t.Fatalf("linked receiver %s:%s is missing", beta.ID(), shared.ID())
	}
	if err := receiver.KillOthers(ctx); err != nil {
		t.Fatalf("linked Window.KillOthers() error = %v", err)
	}

	if _, err := server.Window(ctx, betaInitial); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("beta initial window lookup error = %v, want ErrSnapshotNotFound", err)
	}
	if _, err := server.Window(ctx, alphaExtra.ID()); err != nil {
		t.Fatalf("alpha extra window should survive beta KillOthers: %v", err)
	}
	snapshot, err = server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() after linked KillOthers error = %v", err)
	}
	betaSnapshot, err := snapshot.SessionByID(beta.ID())
	if err != nil {
		t.Fatalf("beta SessionByID() error = %v", err)
	}
	if windows := betaSnapshot.Windows(); len(windows) != 1 || windows[0].ID() != shared.ID() {
		t.Fatalf("beta windows after KillOthers = %#v, want only shared window", windows)
	}
}
