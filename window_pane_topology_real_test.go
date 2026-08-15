//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmux_test

import (
	"context"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.pane.Pane.select
// libtmux:parity libtmux.pane.Pane.select#parameter-branch:clear_mark:0cf0a1d8711c
// libtmux:parity libtmux.pane.Pane.select#parameter-branch:direction:9447fa04b73f
// libtmux:parity libtmux.pane.Pane.select#parameter-branch:disable_input:5b2b24acf003
// libtmux:parity libtmux.pane.Pane.select#parameter-branch:enable_input:e988f797bf57
// libtmux:parity libtmux.pane.Pane.select#parameter-branch:keep_zoom:23c6e462a56a
// libtmux:parity libtmux.pane.Pane.select#parameter-branch:last:05d2c0a40f77
// libtmux:parity libtmux.pane.Pane.select#parameter-branch:mark:36e602b4c9ef
// libtmux:parity libtmux.window.Window.last_pane
// libtmux:parity libtmux.window.Window.last_pane#parameter-branch:disable_input:5b2b24acf003
// libtmux:parity libtmux.window.Window.last_pane#parameter-branch:enable_input:e988f797bf57
// libtmux:parity libtmux.window.Window.last_pane#parameter-branch:keep_zoom:23c6e462a56a
// libtmux:parity libtmux.window.Window.rotate
// libtmux:parity libtmux.window.Window.rotate#parameter-branch:downward:0beae2e8c039
// libtmux:parity libtmux.window.Window.rotate#parameter-branch:keep_zoom:23c6e462a56a
// libtmux:parity libtmux.window.Window.rotate#parameter-branch:upward:ea13c5db4ddd
// libtmux:parity libtmux.window.Window.select_pane
// libtmux:parity libtmux.window.Window.select_pane#parameter-branch:target_pane:3be4cb81af8c
//
//libtmux:real-tmux
func TestWindowPaneSelectionAndRotationAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	window := snapshot.Windows()[0]
	first := window.Panes()[0]
	second, err := window.SplitPane(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}

	selected, err := window.SelectPane(ctx, tmux.WindowSelectPaneRequest{Target: second})
	if err != nil {
		t.Fatalf("SelectPane(target) error = %v", err)
	}
	assertExactRealPane(t, selected, window.SessionID(), window.ID(), second.ID())
	selected, err = window.SelectPane(ctx, tmux.WindowSelectPaneRequest{
		Direction: tmux.PaneSelectDirectionLast,
	})
	if err != nil {
		t.Fatalf("SelectPane(last) error = %v", err)
	}
	assertExactRealPane(t, selected, window.SessionID(), window.ID(), first.ID())

	second, err = second.Select(ctx, tmux.PaneSelectRequest{Mark: tmux.PaneMarkSet})
	if err != nil {
		t.Fatalf("Pane.Select(mark) error = %v", err)
	}
	if marked, _ := second.Marked(); !marked {
		t.Fatal("marked pane = false, want true")
	}
	second, err = second.Select(ctx, tmux.PaneSelectRequest{Input: tmux.PaneInputDisable})
	if err != nil {
		t.Fatalf("Pane.Select(disable) error = %v", err)
	}
	if inputOff, _ := second.InputOff(); !inputOff {
		t.Fatal("pane input off = false, want true")
	}
	second, err = second.Select(ctx, tmux.PaneSelectRequest{Mark: tmux.PaneMarkClear})
	if err != nil {
		t.Fatalf("Pane.Select(clear mark) error = %v", err)
	}
	second, err = second.Select(ctx, tmux.PaneSelectRequest{Input: tmux.PaneInputEnable})
	if err != nil {
		t.Fatalf("Pane.Select(enable) error = %v", err)
	}
	if inputOff, _ := second.InputOff(); inputOff {
		t.Fatal("pane input off = true, want false")
	}

	selected, err = window.LastPane(ctx, tmux.LastPaneRequest{})
	if err != nil {
		t.Fatalf("LastPane() error = %v", err)
	}
	assertExactRealPane(t, selected, window.SessionID(), window.ID(), second.ID())

	before := second.Index()
	rotated, err := window.Rotate(ctx, tmux.RotateWindowRequest{
		Direction: tmux.RotateWindowDown,
	})
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	var rotatedSecond tmux.Pane
	for _, pane := range rotated.Panes() {
		if pane.ID() == second.ID() {
			rotatedSecond = pane
			break
		}
	}
	if rotatedSecond.ID() == "" || rotatedSecond.Index() == before {
		t.Fatalf("rotated pane index = %d, want different from %d", rotatedSecond.Index(), before)
	}
}

// libtmux:parity libtmux.pane.Pane.break_pane
// libtmux:parity libtmux.pane.Pane.break_pane#parameter-branch:detach:3c117da5d4e8
// libtmux:parity libtmux.pane.Pane.break_pane#parameter-branch:window_name:0a86cd63c46b
// libtmux:parity libtmux.pane.Pane.break_pane#version-branch:tmux-version:1386f8bb27d6
// libtmux:parity libtmux.pane.Pane.break_pane#version-branch:window_name:88c9ad7200b2
// libtmux:parity libtmux.pane.Pane.join
// libtmux:parity libtmux.pane.Pane.join#parameter-branch:before:5990b635f38a
// libtmux:parity libtmux.pane.Pane.join#parameter-branch:detach:3c117da5d4e8
// libtmux:parity libtmux.pane.Pane.join#parameter-branch:full_window:62388c4a7919
// libtmux:parity libtmux.pane.Pane.join#parameter-branch:size:af48b30c8b98
// libtmux:parity libtmux.pane.Pane.join#parameter-branch:target:53fdfe9b4804
// libtmux:parity libtmux.pane.Pane.join#parameter-branch:target:c91485f0a60d
// libtmux:parity libtmux.pane.Pane.join#parameter-branch:vertical:a9339036a786
// libtmux:parity libtmux.pane.Pane.move
// libtmux:parity libtmux.pane.Pane.move#parameter-branch:before:5990b635f38a
// libtmux:parity libtmux.pane.Pane.move#parameter-branch:detach:3c117da5d4e8
// libtmux:parity libtmux.pane.Pane.move#parameter-branch:full_window:62388c4a7919
// libtmux:parity libtmux.pane.Pane.move#parameter-branch:size:af48b30c8b98
// libtmux:parity libtmux.pane.Pane.move#parameter-branch:target:53fdfe9b4804
// libtmux:parity libtmux.pane.Pane.move#parameter-branch:target:c91485f0a60d
// libtmux:parity libtmux.pane.Pane.move#parameter-branch:vertical:a9339036a786
// libtmux:parity libtmux.pane.Pane.swap
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:detach:3c117da5d4e8
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:keep_zoom:23c6e462a56a
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:move_down,move_up,target:0f855a26299d
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:move_down,move_up,target:53172b056010
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:move_down,move_up:d5215a90100b
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:move_down:988ea1aca8e5
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:move_up:065020e6f746
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:target:3fc216416bbd
// libtmux:parity libtmux.pane.Pane.swap#parameter-branch:target:53fdfe9b4804
// libtmux:parity libtmux.window.Window.swap
// libtmux:parity libtmux.window.Window.swap#parameter-branch:detach:3c117da5d4e8
// libtmux:parity libtmux.window.Window.swap#parameter-branch:target:c91485f0a60d
// libtmux:parity libtmux.window.Window.swap#parameter-branch:target:c91485f0a60d:2
//
//libtmux:real-tmux
func TestPaneTopologyAndLinkedContextAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	session := snapshot.Sessions()[0]
	sourceWindow := snapshot.Windows()[0]
	sourceBase := sourceWindow.Panes()[0]
	source, err := sourceWindow.SplitPane(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatalf("SplitPane(source) error = %v", err)
	}
	destinationWindow, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: newWindowName("destination"),
	})
	if err != nil {
		t.Fatalf("NewWindow(destination) error = %v", err)
	}
	snapshot = mustRealSnapshot(t, server)
	for _, window := range snapshot.WindowsByID(destinationWindow.ID()) {
		if window.SessionID() == session.ID() {
			destinationWindow = window
			break
		}
	}
	destinationPanes := destinationWindow.Panes()
	if len(destinationPanes) != 1 {
		t.Fatalf("destination panes = %d, want 1", len(destinationPanes))
	}
	destinationPane := destinationPanes[0]

	source, err = source.Move(ctx, tmux.MovePaneRequest{
		TargetPane: destinationPane,
		Direction:  tmux.PaneDirectionRight,
	})
	if err != nil {
		t.Fatalf("Move() error = %v", err)
	}
	assertExactRealPane(t, source, session.ID(), destinationWindow.ID(), source.ID())
	source, err = source.Join(ctx, tmux.JoinPaneRequest{
		TargetPane: sourceBase,
		Direction:  tmux.PaneDirectionBelow,
		Attach:     true,
	})
	if err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	assertExactRealPane(t, source, session.ID(), sourceWindow.ID(), source.ID())

	swapped, err := source.Swap(ctx, tmux.SwapPaneRequest{Target: destinationPane})
	if err != nil {
		t.Fatalf("Pane.Swap() error = %v", err)
	}
	assertExactRealPane(
		t, swapped.Pane, session.ID(), destinationWindow.ID(), source.ID(),
	)
	assertExactRealPane(
		t, swapped.Target, session.ID(), sourceWindow.ID(), destinationPane.ID(),
	)

	broken, err := swapped.Pane.BreakPane(ctx, tmux.BreakPaneRequest{Name: "broken;"})
	if err != nil {
		t.Fatalf("BreakPane() error = %v", err)
	}
	if broken.SessionID() != session.ID() {
		t.Fatalf("broken session = %s, want %s", broken.SessionID(), session.ID())
	}
	if name, _ := broken.Name(); name != "broken;" {
		t.Fatalf("broken window name = %q, want %q", name, "broken;")
	}

	swapTarget, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: newWindowName("swap-target"),
	})
	if err != nil {
		t.Fatalf("NewWindow(swap target) error = %v", err)
	}
	windowSwap, err := broken.Swap(ctx, tmux.SwapWindowRequest{Target: swapTarget})
	if err != nil {
		t.Fatalf("Window.Swap() error = %v", err)
	}
	if windowSwap.Window.ID() != broken.ID() ||
		windowSwap.Window.Index() != swapTarget.Index() {
		t.Fatalf("swapped receiver = %#v, want %s at index %d", windowSwap.Window,
			broken.ID(), swapTarget.Index())
	}
	if windowSwap.Target.ID() != swapTarget.ID() ||
		windowSwap.Target.Index() != broken.Index() {
		t.Fatalf("swapped target = %#v, want %s at index %d", windowSwap.Target,
			swapTarget.ID(), broken.Index())
	}

	guest, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "linked-guest"})
	if err != nil {
		t.Fatalf("NewSession(guest) error = %v", err)
	}
	if err := windowSwap.Window.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: guest.ID(),
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	snapshot = mustRealSnapshot(t, server)
	var linked tmux.Window
	for _, window := range snapshot.WindowsByID(windowSwap.Window.ID()) {
		if window.SessionID() == guest.ID() {
			linked = window
			break
		}
	}
	if linked.ID() == "" {
		t.Fatalf("linked window %s missing from guest %s", windowSwap.Window.ID(), guest.ID())
	}
	linkedPane := linked.Panes()[0]
	selected, err := linked.SelectPane(ctx, tmux.WindowSelectPaneRequest{Target: linkedPane})
	if err != nil {
		t.Fatalf("linked SelectPane() error = %v", err)
	}
	assertExactRealPane(t, selected, guest.ID(), linked.ID(), linkedPane.ID())
}

func assertExactRealPane(
	t *testing.T,
	pane tmux.Pane,
	sessionID tmux.SessionID,
	windowID tmux.WindowID,
	paneID tmux.PaneID,
) {
	t.Helper()
	if pane.SessionID() != sessionID || pane.WindowID() != windowID || pane.ID() != paneID {
		t.Fatalf(
			"pane identity = %s:%s.%s, want %s:%s.%s",
			pane.SessionID(), pane.WindowID(), pane.ID(), sessionID, windowID, paneID,
		)
	}
}
