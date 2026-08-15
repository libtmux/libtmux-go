//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.pytest_plugin.session
// libtmux:parity libtmux.pytest_plugin.session#parameter-branch:server,session_params:3a530ffbbbed
// libtmux:parity libtmux.pytest_plugin.session#parameter-branch:server,session_params:ec162c3347ab
// libtmux:parity libtmux.pytest_plugin.session#parameter-branch:server:5fc231b86772
// libtmux:parity libtmux.pytest_plugin.session#parameter-branch:server:8ad3a8d74bbe
// libtmux:parity libtmux.test.constants.TEST_SESSION_PREFIX
// libtmux:parity libtmux.test.random.RandomStrSequence
// libtmux:parity libtmux.test.random.RandomStrSequence.__init__
// libtmux:parity libtmux.test.random.RandomStrSequence.__iter__
// libtmux:parity libtmux.test.random.RandomStrSequence.__next__
// libtmux:parity libtmux.test.random.get_test_session_name
// libtmux:parity libtmux.test.random.get_test_session_name#parameter-branch:prefix,server:8ad3a8d74bbe
// libtmux:parity libtmux.test.random.namer
// libtmux:parity libtmux.test.temporary.temp_session
// libtmux:parity libtmux.test.temporary.temp_session#parameter-branch:kwargs:9c91ecbfa909
//
//libtmux:real-tmux
func TestNewSessionGeneratesNameAndCleansRenamedStableID(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	var sessionID tmux.SessionID
	t.Run("temporary lifetime", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{})
		sessionID = session.ID()
		name, ok := session.Name()
		if !ok || !strings.HasPrefix(name, tmuxtest.TemporaryNamePrefix) {
			t.Fatalf("generated session name = %q, %t, want temporary prefix", name, ok)
		}
		if _, err := session.Rename(ctx, "renamed-temporary-session"); err != nil {
			t.Fatalf("Rename() error = %v", err)
		}
	})

	if sessionIDs(t, server)[sessionID] {
		t.Fatalf("temporary session %q remains after cleanup", sessionID)
	}
}

// libtmux:parity libtmux.test.random.get_test_window_name
// libtmux:parity libtmux.test.random.get_test_window_name#parameter-branch:prefix,session:12596eafc990
// libtmux:parity libtmux.test.random.get_test_window_name#parameter-branch:prefix:c6c5d89619fb
// libtmux:parity libtmux.test.temporary.temp_window
// libtmux:parity libtmux.test.temporary.temp_window#parameter-branch:args,kwargs,session:008448c3406a
// libtmux:parity libtmux.test.temporary.temp_window#parameter-branch:kwargs:84abc0994f12
//
//libtmux:real-tmux
func TestNewWindowGeneratesNameAndCleansMovedStableID(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	work := onlyControlSession(t, server)
	other := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "other"})
	var windowID tmux.WindowID
	t.Run("temporary lifetime", func(t *testing.T) {
		window := tmuxtest.NewWindow(ctx, t, work, tmux.NewWindowRequest{})
		windowID = window.ID()
		name, ok := window.Name()
		if !ok || !strings.HasPrefix(name, tmuxtest.TemporaryNamePrefix) {
			t.Fatalf("generated window name = %q, %t, want temporary prefix", name, ok)
		}
		moved, err := window.Move(ctx, tmux.MoveWindowRequest{TargetSession: other.ID()})
		if err != nil {
			t.Fatalf("Move() error = %v", err)
		}
		if moved.ID() != windowID || moved.SessionID() != other.ID() {
			t.Fatalf("moved window = %#v, want %s in %s", moved, windowID, other.ID())
		}
	})

	if slices.Contains(windowIDs(t, server), windowID) {
		t.Fatalf("temporary window %q remains after cleanup", windowID)
	}
}

//libtmux:real-tmux
func TestTemporaryResourceCleanupToleratesAlreadyMissingObjects(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	work := onlyControlSession(t, server)

	t.Run("session", func(t *testing.T) {
		session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "already-gone"})
		if err := session.Kill(ctx); err != nil {
			t.Fatalf("Kill() error = %v", err)
		}
	})
	t.Run("window", func(t *testing.T) {
		windowName := "already-gone"
		window := tmuxtest.NewWindow(ctx, t, work, tmux.NewWindowRequest{Name: &windowName})
		if err := window.Kill(ctx); err != nil {
			t.Fatalf("Kill() error = %v", err)
		}
	})
}

// libtmux:parity libtmux.test.temporary.temp_session#parameter-branch:kwargs,server:6b88852e945b
// libtmux:parity libtmux.test.temporary.temp_window#parameter-branch:args,kwargs,session:edd0f97da67b
// libtmux:parity libtmux.test.temporary.temp_window#parameter-branch:args,kwargs,session:f9e445ed642b
//
//libtmux:real-tmux
func TestTemporaryResourceHelpersPreserveExplicitNames(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	work := onlyControlSession(t, server)

	session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "explicit-session"})
	if name, ok := session.Name(); !ok || name != "explicit-session" {
		t.Fatalf("session name = %q, %t, want explicit-session", name, ok)
	}
	windowName := "explicit-window"
	window := tmuxtest.NewWindow(ctx, t, work, tmux.NewWindowRequest{Name: &windowName})
	if name, ok := window.Name(); !ok || name != "explicit-window" {
		t.Fatalf("window name = %q, %t, want explicit-window", name, ok)
	}
}

func sessionIDs(t *testing.T, server tmux.Server) map[tmux.SessionID]bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessions, err := server.WithStrictErrors().Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	ids := make(map[tmux.SessionID]bool, len(sessions))
	for _, session := range sessions {
		ids[session.ID()] = true
	}
	return ids
}

func windowIDs(t *testing.T, server tmux.Server) []tmux.WindowID {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	windows, err := server.WithStrictErrors().Windows(ctx)
	if err != nil {
		t.Fatalf("Windows() error = %v", err)
	}
	ids := make([]tmux.WindowID, 0, len(windows))
	for _, window := range windows {
		ids = append(ids, window.ID())
	}
	return ids
}
