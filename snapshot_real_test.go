//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmux_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.server.Server.formatter_prefix
//
// libtmux:parity libtmux.formats.PANE_FORMATS
//
// libtmux:parity libtmux.window.Window.linked_sessions
//
//libtmux:real-tmux
func TestSnapshotMatchesLinkedRealTmuxGraph(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	sharedName := "dev|equals=percent% slash\\ space␞-開発"
	paneTitle := "line1\nline2|equals=percent% slash\\ space␞-開発"
	mustRealCommand(t, server, "rename-window", "-t", "work:0", sharedName)
	mustRealCommand(t, server, "select-pane", "-t", "work:0.0", "-T", paneTitle)
	sharedName = strings.Join(mustRealCommand(
		t, server, "display-message", "-p", "-t", "work:0", "#{window_name}",
	).Stdout, "\n")
	paneTitle = strings.Join(mustRealCommand(
		t, server, "display-message", "-p", "-t", "work:0.0", "#{pane_title}",
	).Stdout, "\n")
	mustRealCommand(t, server, "new-session", "-d", "-s", "beta", "-n", "own")
	mustRealCommand(t, server, "link-window", "-s", "work:0", "-t", "beta:7")
	controlClient := startRealControlClient(t, server, "beta")

	snapshot := snapshotWithAttachedClient(t, server, controlClient)
	if got := len(snapshot.Sessions()); got != 2 {
		t.Fatalf("len(Sessions()) = %d, want 2", got)
	}
	if got := len(snapshot.Windows()); got != 3 {
		t.Fatalf("len(Windows()) = %d, want 3 winlinks", got)
	}
	if got := len(snapshot.Panes()); got != 3 {
		t.Fatalf("len(Panes()) = %d, want 3 pane views", got)
	}
	if got := len(snapshot.Clients()); got != 1 {
		t.Fatalf("len(Clients()) = %d, want 1 attached client", got)
	}
	client := snapshot.Clients()[0]
	attachedSession, ok := client.AttachedSession()
	if !ok {
		t.Fatal("AttachedSession() did not resolve the real client's session")
	}
	attachedWindow, ok := client.AttachedWindow()
	if !ok {
		t.Fatal("AttachedWindow() did not resolve the real client's winlink")
	}
	attachedPane, ok := client.AttachedPane()
	if !ok {
		t.Fatal("AttachedPane() did not resolve the real client's pane view")
	}
	if sessionID, ok := client.Formats().SessionID(); !ok || sessionID != attachedSession.ID() {
		t.Fatalf("Client.Formats().SessionID() = (%q, %t), attached session = %q", sessionID, ok, attachedSession.ID())
	}
	if windowID, ok := client.Formats().WindowID(); !ok || windowID != attachedWindow.ID() {
		t.Fatalf("Client.Formats().WindowID() = (%q, %t), attached window = %q", windowID, ok, attachedWindow.ID())
	}
	if paneID, ok := client.Formats().PaneID(); !ok || paneID != attachedPane.ID() {
		t.Fatalf("Client.Formats().PaneID() = (%q, %t), attached pane = %q", paneID, ok, attachedPane.ID())
	}

	var sharedWindows []tmux.Window
	var windowNames []string
	for _, window := range snapshot.Windows() {
		name, ok := window.Name()
		if ok {
			windowNames = append(windowNames, name)
		}
		if ok && name == sharedName {
			sharedWindows = append(sharedWindows, window)
		}
	}
	if len(sharedWindows) != 2 {
		t.Fatalf("shared window views = %#v among names %#v, want 2", sharedWindows, windowNames)
	}
	if sharedWindows[0].ID() != sharedWindows[1].ID() {
		t.Fatalf("linked window IDs differ: %s and %s", sharedWindows[0].ID(), sharedWindows[1].ID())
	}
	if sharedWindows[0].SessionID() == sharedWindows[1].SessionID() {
		t.Fatalf("linked window session IDs are both %s", sharedWindows[0].SessionID())
	}
	firstPanes := sharedWindows[0].Panes()
	secondPanes := sharedWindows[1].Panes()
	if len(firstPanes) != 1 || len(secondPanes) != 1 || firstPanes[0].ID() != secondPanes[0].ID() {
		t.Fatalf("linked pane views = (%#v, %#v), want one shared pane ID", firstPanes, secondPanes)
	}
	if title, ok := firstPanes[0].Title(); !ok || title != paneTitle {
		t.Fatalf("Title() = (%q, %t), want (%q, true)", title, ok, paneTitle)
	}
	if alternate, ok := firstPanes[0].AlternateOn(); !ok {
		t.Fatalf("AlternateOn() = (%t, %t), want a present boolean", alternate, ok)
	}
	if mouseUTF8, ok := firstPanes[0].MouseUTF8Flag(); !ok {
		t.Fatalf("MouseUTF8Flag() = (%t, %t), want a present boolean", mouseUTF8, ok)
	}
}

func mustRealCommand(t *testing.T, server tmux.Server, arguments ...string) tmux.CommandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := server.Cmd(ctx, arguments...)
	if err != nil {
		t.Fatalf("tmux %v: %v", arguments, err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("tmux %v exited %d: %s", arguments, result.ExitCode, fmt.Sprint(result.Stderr))
	}
	return result
}

func startRealControlClient(t *testing.T, server tmux.Server, target string) *tmuxtest.ControlMode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessions, err := server.WithStrictErrors().Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	for _, session := range sessions {
		name, ok := session.Name()
		if ok && name == target {
			return tmuxtest.NewControlMode(context.Background(), t, server, session)
		}
	}
	t.Fatalf("session %q not found in %#v", target, sessions)
	return nil
}

func snapshotWithAttachedClient(
	t *testing.T,
	server tmux.Server,
	client *tmuxtest.ControlMode,
) tmux.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		snapshot, err := server.Snapshot(ctx)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if len(snapshot.Clients()) != 0 {
			return snapshot
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for attached control client %q: %v", client.ClientName(), ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
