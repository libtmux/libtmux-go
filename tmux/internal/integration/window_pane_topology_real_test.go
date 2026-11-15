//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestWindowPaneSelectionAndRotationAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	window := snapshot.Windows()[0]
	first := relatedPanes(t, window)[0]
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
	relatedPanes := relatedPanes(t, rotated)
	for _, pane := range relatedPanes {
		if pane.ID() == second.ID() {
			rotatedSecond = pane
			break
		}
	}
	if rotatedSecond.ID() == "" || rotatedSecond.Index() == before {
		t.Fatalf("rotated pane index = %d, want different from %d", rotatedSecond.Index(), before)
	}
}

func TestPaneTopologyAndLinkedContextAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	session := snapshot.Sessions()[0]
	sourceWindow := snapshot.Windows()[0]
	sourceBase := relatedPanes(t, sourceWindow)[0]
	source, err := sourceWindow.SplitPane(ctx, tmux.SplitPaneRequest{})
	if err != nil {
		t.Fatalf("SplitPane(source) error = %v", err)
	}
	destinationWindow, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("destination"),
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
	destinationPanes := relatedPanes(t, destinationWindow)
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
		Name: new("swap-target"),
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
	linkedPane := relatedPanes(t, linked)[0]
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
