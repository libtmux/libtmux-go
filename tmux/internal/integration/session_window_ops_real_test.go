//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.session.Session.last_window
// libtmux:parity libtmux.session.Session.next_window
// libtmux:parity libtmux.session.Session.previous_window
// libtmux:parity libtmux.session.Session.select_window
//
//libtmux:real-tmux
func TestSessionWindowNavigationAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	session := snapshot.Sessions()[0]
	first := relatedWindows(t, session)[0]
	second, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("nav-second"), Attach: true,
	})
	if err != nil {
		t.Fatalf("NewWindow(second) error = %v", err)
	}
	third, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("nav-third"), Attach: true,
	})
	if err != nil {
		t.Fatalf("NewWindow(third) error = %v", err)
	}

	active, err := session.LastWindow(ctx)
	if err != nil {
		t.Fatalf("LastWindow() error = %v", err)
	}
	assertRealActiveWindow(t, active, session.ID(), second.ID())
	active, err = session.NextWindow(ctx)
	if err != nil {
		t.Fatalf("NextWindow() error = %v", err)
	}
	assertRealActiveWindow(t, active, session.ID(), third.ID())
	active, err = session.PreviousWindow(ctx)
	if err != nil {
		t.Fatalf("PreviousWindow() error = %v", err)
	}
	assertRealActiveWindow(t, active, session.ID(), second.ID())
	active, err = session.SelectWindow(ctx, tmux.SelectWindowRequest{WindowID: first.ID()})
	if err != nil {
		t.Fatalf("SelectWindow(WindowID) error = %v", err)
	}
	assertRealActiveWindow(t, active, session.ID(), first.ID())
	thirdIndex := third.Index()
	active, err = session.SelectWindow(ctx, tmux.SelectWindowRequest{Index: &thirdIndex})
	if err != nil {
		t.Fatalf("SelectWindow(Index) error = %v", err)
	}
	assertRealActiveWindow(t, active, session.ID(), third.ID())
}

// libtmux:parity libtmux.window.Window.next_layout
// libtmux:parity libtmux.window.Window.previous_layout
// libtmux:parity libtmux.window.Window.resize
// libtmux:parity libtmux.window.Window.resize#parameter-branch:adjustment:73daa10a3099
// libtmux:parity libtmux.window.Window.resize#parameter-branch:adjustment_direction:e4a3795db9a4
// libtmux:parity libtmux.window.Window.resize#parameter-branch:expand,shrink:d624172d273f
// libtmux:parity libtmux.window.Window.resize#parameter-branch:expand:163401d681e1
// libtmux:parity libtmux.window.Window.resize#parameter-branch:height,width:a104fc5529e1
// libtmux:parity libtmux.window.Window.resize#parameter-branch:height:3fde596c5d4b
// libtmux:parity libtmux.window.Window.resize#parameter-branch:shrink:fd4b5f4dd805
// libtmux:parity libtmux.window.Window.resize#parameter-branch:width:84d6a1e76504
// libtmux:parity libtmux.window.Window.select_layout
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:layout,next_layout,previous_layout,spread:0fa948c7463a
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:layout:2a9f9e28d66a
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:next_layout:e5241b7cc5f9
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:previous_layout:146fb5ba152c
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:spread:ef0ecf84f4e8
//
//libtmux:real-tmux
func TestWindowLayoutAndResizeAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	window := snapshot.Windows()[0]
	height := 30
	width := 100
	window, err := window.Resize(ctx, tmux.ResizeWindowRequest{Height: height, Width: width})
	if err != nil {
		t.Fatalf("Resize(dimensions) error = %v", err)
	}
	assertRealWindowSize(t, window, 100, 30)

	adjustment := 2
	window, err = window.Resize(ctx, tmux.ResizeWindowRequest{
		Direction: tmux.WindowResizeDirectionDown, Adjustment: adjustment,
	})
	if err != nil {
		t.Fatalf("Resize(down) error = %v", err)
	}
	assertRealWindowSize(t, window, 100, 32)
	window, err = window.Resize(ctx, tmux.ResizeWindowRequest{
		Direction: tmux.WindowResizeDirectionUp, Adjustment: adjustment,
	})
	if err != nil {
		t.Fatalf("Resize(up) error = %v", err)
	}
	assertRealWindowSize(t, window, 100, 30)
	window, err = window.Resize(ctx, tmux.ResizeWindowRequest{
		Direction: tmux.WindowResizeDirectionRight, Adjustment: adjustment,
	})
	if err != nil {
		t.Fatalf("Resize(right) error = %v", err)
	}
	assertRealWindowSize(t, window, 102, 30)
	window, err = window.Resize(ctx, tmux.ResizeWindowRequest{
		Direction: tmux.WindowResizeDirectionLeft, Adjustment: adjustment,
	})
	if err != nil {
		t.Fatalf("Resize(left) error = %v", err)
	}
	assertRealWindowSize(t, window, 100, 30)
	if _, err := window.Resize(ctx, tmux.ResizeWindowRequest{Expand: true}); err != nil {
		t.Fatalf("Resize(Expand) error = %v", err)
	}
	if _, err := window.Resize(ctx, tmux.ResizeWindowRequest{Shrink: true}); err != nil {
		t.Fatalf("Resize(Shrink) error = %v", err)
	}
	window, err = window.Resize(ctx, tmux.ResizeWindowRequest{Height: height, Width: width})
	if err != nil {
		t.Fatalf("Resize(reset dimensions) error = %v", err)
	}
	if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}

	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Layout: "even-horizontal"}); err != nil {
		t.Fatalf("SelectLayout(named) error = %v", err)
	}
	before := realWindowLayout(ctx, t, server, window.ID())
	if err := window.NextLayout(ctx); err != nil {
		t.Fatalf("NextLayout() error = %v", err)
	}
	after := realWindowLayout(ctx, t, server, window.ID())
	if before == after {
		t.Fatalf("NextLayout() layout = %q, want change from %q", after, before)
	}
	if err := window.PreviousLayout(ctx); err != nil {
		t.Fatalf("PreviousLayout() error = %v", err)
	}
	if restored := realWindowLayout(ctx, t, server, window.ID()); restored != before {
		t.Fatalf("PreviousLayout() layout = %q, want %q", restored, before)
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Next: true}); err != nil {
		t.Fatalf("SelectLayout(Next) error = %v", err)
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Previous: true}); err != nil {
		t.Fatalf("SelectLayout(Previous) error = %v", err)
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{Spread: true}); err != nil {
		t.Fatalf("SelectLayout(Spread) error = %v", err)
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{}); err != nil {
		t.Fatalf("SelectLayout(zero) error = %v", err)
	}
	// A name tmux does not know is refused before it is sent, because tmux 3.3a
	// answers one by exiting and taking every session on the socket with it.
	if err := window.SelectLayout(
		ctx, tmux.SelectLayoutRequest{Layout: "not-a-layout"},
	); !errors.Is(err, tmux.ErrInvalidServerCommandRequest) {
		t.Fatalf("SelectLayout(invalid) error = %v, want ErrInvalidServerCommandRequest", err)
	}
	// A layout string tmux cannot apply still reaches tmux and is refused
	// there, which is the shape this package cannot check for the caller.
	if err := window.SelectLayout(
		ctx, tmux.SelectLayoutRequest{Layout: "bb62,80x24,0,0,0"},
	); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("SelectLayout(unapplicable layout string) error = %v, want ErrCommand", err)
	}
}

//libtmux:real-tmux
func TestWindowResizeUsesReceiverWinlinkAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	firstSession := snapshot.Sessions()[0]
	shared := relatedWindows(t, firstSession)[0]
	secondSession, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "resize-linked-target",
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	linkIndex := 7
	if err := shared.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: secondSession.ID(),
		TargetIndex:   &linkIndex,
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	snapshot = mustRealSnapshot(t, server)
	canonical, err := server.Window(ctx, shared.ID())
	if err != nil {
		t.Fatalf("Window() error = %v", err)
	}
	var receiverSession, canonicalSession tmux.Session
	switch canonical.SessionID() {
	case firstSession.ID():
		canonicalSession = firstSession
		receiverSession = secondSession
	case secondSession.ID():
		canonicalSession = secondSession
		receiverSession = firstSession
	default:
		t.Fatalf("canonical session = %s, want one linked session", canonical.SessionID())
	}
	if err := receiverSession.SetOption(
		ctx,
		"default-size",
		"80x24",
		tmux.SetOptionOptions{},
	); err != nil {
		t.Fatalf("receiver SetOption(default-size) error = %v", err)
	}
	if err := canonicalSession.SetOption(
		ctx,
		"default-size",
		"120x40",
		tmux.SetOptionOptions{},
	); err != nil {
		t.Fatalf("canonical SetOption(default-size) error = %v", err)
	}
	receiver := exactRealWindow(
		t,
		snapshot,
		receiverSession.ID(),
		shared.ID(),
	)

	height := 20
	width := 60
	operations := []struct {
		name    string
		request tmux.ResizeWindowRequest
	}{
		{name: "expand", request: tmux.ResizeWindowRequest{Expand: true}},
		{name: "shrink", request: tmux.ResizeWindowRequest{Shrink: true}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if _, err := receiver.Resize(ctx, tmux.ResizeWindowRequest{
				Height: height,
				Width:  width,
			}); err != nil {
				t.Fatalf("Resize(reset) error = %v", err)
			}
			resized, err := receiver.Resize(ctx, operation.request)
			if err != nil {
				t.Fatalf("Resize() error = %v", err)
			}
			assertRealWindowSize(t, resized, 80, 24)
		})
	}
}

//libtmux:real-tmux
func TestWindowSelectLayoutUsesReceiverWinlinkAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	firstSession := snapshot.Sessions()[0]
	shared := relatedWindows(t, firstSession)[0]
	if _, err := shared.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	secondSession, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "layout-linked-target",
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	linkIndex := 7
	if err := shared.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: secondSession.ID(),
		TargetIndex:   &linkIndex,
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	snapshot = mustRealSnapshot(t, server)
	canonical, err := server.Window(ctx, shared.ID())
	if err != nil {
		t.Fatalf("Window() error = %v", err)
	}
	var receiverSession, canonicalSession tmux.Session
	switch canonical.SessionID() {
	case firstSession.ID():
		canonicalSession = firstSession
		receiverSession = secondSession
	case secondSession.ID():
		canonicalSession = secondSession
		receiverSession = firstSession
	default:
		t.Fatalf("canonical session = %s, want one linked session", canonical.SessionID())
	}
	const marker = "@layout-hit"
	if err := receiverSession.SetHook(
		ctx,
		"after-select-layout",
		"set-option -t "+receiverSession.ID().String()+" "+marker+" receiver",
	); err != nil {
		t.Fatalf("receiver SetHook() error = %v", err)
	}
	if err := canonicalSession.SetHook(
		ctx,
		"after-select-layout",
		"set-option -t "+canonicalSession.ID().String()+" "+marker+" canonical",
	); err != nil {
		t.Fatalf("canonical SetHook() error = %v", err)
	}
	receiver := exactRealWindow(
		t,
		snapshot,
		receiverSession.ID(),
		shared.ID(),
	)
	if err := receiver.SelectLayout(ctx, tmux.SelectLayoutRequest{
		Layout: "even-horizontal",
	}); err != nil {
		t.Fatalf("SelectLayout() error = %v", err)
	}

	value, present, err := receiverSession.RawOption(ctx, marker)
	if err != nil {
		t.Fatalf("receiver RawOption() error = %v", err)
	}
	if !present || value != "receiver" {
		t.Fatalf("receiver marker = (%q, %t), want (receiver, true)", value, present)
	}
	value, present, err = canonicalSession.RawOption(ctx, marker)
	if err != nil {
		t.Fatalf("canonical RawOption() error = %v", err)
	}
	if present {
		t.Fatalf("canonical marker = (%q, true), want absent", value)
	}
}

// libtmux:parity libtmux.pane.Pane.reset
// libtmux:parity libtmux.window.Window.rename_window
// libtmux:parity libtmux.window.Window.select
// libtmux:parity libtmux.window.Window.split
//
//libtmux:real-tmux
func TestWindowHighLevelOperationsUseReceiverWinlinkAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	firstSession := snapshot.Sessions()[0]
	shared := relatedWindows(t, firstSession)[0]
	secondSession, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "typed-window-linked-target",
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	linkIndex := 7
	if err := shared.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: secondSession.ID(),
		TargetIndex:   &linkIndex,
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	canonical, err := server.Window(ctx, shared.ID())
	if err != nil {
		t.Fatalf("Window() error = %v", err)
	}
	var receiverSession, canonicalSession tmux.Session
	switch canonical.SessionID() {
	case firstSession.ID():
		canonicalSession = firstSession
		receiverSession = secondSession
	case secondSession.ID():
		canonicalSession = secondSession
		receiverSession = firstSession
	default:
		t.Fatalf("canonical session = %s, want one linked session", canonical.SessionID())
	}
	receiverBase, err := receiverSession.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("typed-window-receiver-base"), Attach: true,
	})
	if err != nil {
		t.Fatalf("receiver NewWindow() error = %v", err)
	}
	canonicalBase, err := canonicalSession.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("typed-window-canonical-base"), Attach: true,
	})
	if err != nil {
		t.Fatalf("canonical NewWindow() error = %v", err)
	}
	snapshot = mustRealSnapshot(t, server)
	receiver := exactRealWindow(
		t,
		snapshot,
		receiverSession.ID(),
		shared.ID(),
	)

	format := "#{session_id}"
	output, err := receiver.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Print: true, Format: &format,
	})
	if err != nil {
		t.Fatalf("DisplayMessage() error = %v", err)
	}
	if !slices.Equal(output, []string{receiverSession.ID().String()}) {
		t.Fatalf("DisplayMessage() = %#v, want receiver session", output)
	}

	const optionMarker = "@typed-window-option-context"
	if err := receiver.SetOption(
		ctx,
		optionMarker,
		"#{session_id}",
		tmux.SetOptionOptions{ExpandFormat: true},
	); err != nil {
		t.Fatalf("SetOption() error = %v", err)
	}
	value, present, err := receiver.RawOption(ctx, optionMarker)
	if err != nil {
		t.Fatalf("RawOption() error = %v", err)
	}
	if !present || value != receiverSession.ID().String() {
		t.Fatalf("window option = (%q, %t), want receiver session", value, present)
	}

	const runHookMarker = "@typed-window-run-hook-context"
	if err := receiver.SetHook(
		ctx,
		"pane-died",
		"set-option -g -F "+runHookMarker+" '#{session_id}'",
	); err != nil {
		t.Fatalf("SetHook() error = %v", err)
	}
	if err := receiver.RunHook(ctx, "pane-died"); err != nil {
		t.Fatalf("RunHook() error = %v", err)
	}
	value, present, err = server.GlobalSessionScope().RawOption(ctx, runHookMarker)
	if err != nil {
		t.Fatalf("GlobalSessionScope.RawOption() error = %v", err)
	}
	if !present || value != receiverSession.ID().String() {
		t.Fatalf("run-hook context = (%q, %t), want receiver session", value, present)
	}

	receiverPanes := relatedPanes(t, receiver)
	if len(receiverPanes) != 1 {
		t.Fatalf("receiver panes = %#v, want one", receiverPanes)
	}
	receiverPane := receiverPanes[0]
	paneOutput, err := receiverPane.DisplayMessage(ctx, tmux.PaneDisplayMessageRequest{
		DisplayMessageRequest: tmux.DisplayMessageRequest{Print: true, Format: &format},
	})
	if err != nil {
		t.Fatalf("Pane.DisplayMessage() error = %v", err)
	}
	if !slices.Equal(paneOutput, []string{receiverSession.ID().String()}) {
		t.Fatalf("Pane.DisplayMessage() = %#v, want receiver session", paneOutput)
	}

	const paneOptionMarker = "@typed-pane-option-context"
	if err := receiverPane.SetOption(
		ctx,
		paneOptionMarker,
		"#{session_id}",
		tmux.SetOptionOptions{ExpandFormat: true},
	); err != nil {
		t.Fatalf("Pane.SetOption() error = %v", err)
	}
	value, present, err = receiverPane.RawOption(ctx, paneOptionMarker)
	if err != nil {
		t.Fatalf("Pane.RawOption() error = %v", err)
	}
	if !present || value != receiverSession.ID().String() {
		t.Fatalf("pane option = (%q, %t), want receiver session", value, present)
	}

	const paneRunHookMarker = "@typed-pane-run-hook-context"
	if err := receiverPane.SetHook(
		ctx,
		"pane-mode-changed",
		"set-option -g -F "+paneRunHookMarker+" '#{session_id}'",
	); err != nil {
		t.Fatalf("Pane.SetHook() error = %v", err)
	}
	if err := receiverPane.RunHook(ctx, "pane-mode-changed"); err != nil {
		t.Fatalf("Pane.RunHook() error = %v", err)
	}
	value, present, err = server.GlobalSessionScope().RawOption(ctx, paneRunHookMarker)
	if err != nil {
		t.Fatalf("GlobalSessionScope.RawOption(pane hook) error = %v", err)
	}
	if !present || value != receiverSession.ID().String() {
		t.Fatalf("pane run-hook context = (%q, %t), want receiver session", value, present)
	}

	paneFilter := tmux.TmuxFilter(
		"#{==:#{session_id}," + receiverSession.ID().String() + "}",
	)
	searchedPanes, err := receiver.SearchPanes(ctx, &paneFilter)
	if err != nil {
		t.Fatalf("Window.SearchPanes() error = %v", err)
	}
	if len(searchedPanes) != len(receiverPanes) {
		t.Fatalf("Window.SearchPanes() = %#v, want %d receiver panes", searchedPanes, len(receiverPanes))
	}
	for _, pane := range searchedPanes {
		if pane.SessionID() != receiverSession.ID() || pane.WindowID() != shared.ID() {
			t.Fatalf("Window.SearchPanes() pane = %#v, want receiver winlink", pane)
		}
	}

	const resetMarker = "@typed-pane-reset-context"
	if err := receiverSession.SetHook(
		ctx,
		"after-send-keys",
		"set-option -t "+receiverSession.ID().String()+" "+resetMarker+" receiver",
	); err != nil {
		t.Fatalf("receiver SetHook(after-send-keys) error = %v", err)
	}
	if err := canonicalSession.SetHook(
		ctx,
		"after-send-keys",
		"set-option -t "+canonicalSession.ID().String()+" "+resetMarker+" canonical",
	); err != nil {
		t.Fatalf("canonical SetHook(after-send-keys) error = %v", err)
	}
	if err := receiverPane.Reset(ctx); err != nil {
		t.Fatalf("Pane.Reset() error = %v", err)
	}
	value, present, err = receiverSession.RawOption(ctx, resetMarker)
	if err != nil {
		t.Fatalf("receiver RawOption(reset marker) error = %v", err)
	}
	if !present || value != "receiver" {
		t.Fatalf("receiver reset marker = (%q, %t), want receiver", value, present)
	}
	value, present, err = canonicalSession.RawOption(ctx, resetMarker)
	if err != nil {
		t.Fatalf("canonical RawOption(reset marker) error = %v", err)
	}
	if present {
		t.Fatalf("canonical reset marker = (%q, true), want absent", value)
	}

	const renameMarker = "@typed-window-rename-context"
	if err := receiverSession.SetHook(
		ctx,
		"after-rename-window",
		"set-option -t "+receiverSession.ID().String()+" "+renameMarker+" receiver",
	); err != nil {
		t.Fatalf("receiver SetHook(after-rename-window) error = %v", err)
	}
	if err := canonicalSession.SetHook(
		ctx,
		"after-rename-window",
		"set-option -t "+canonicalSession.ID().String()+" "+renameMarker+" canonical",
	); err != nil {
		t.Fatalf("canonical SetHook(after-rename-window) error = %v", err)
	}
	if _, err := receiver.Rename(ctx, "typed-window-renamed"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	value, present, err = receiverSession.RawOption(ctx, renameMarker)
	if err != nil {
		t.Fatalf("receiver RawOption(rename marker) error = %v", err)
	}
	if !present || value != "receiver" {
		t.Fatalf("receiver rename marker = (%q, %t), want receiver", value, present)
	}
	value, present, err = canonicalSession.RawOption(ctx, renameMarker)
	if err != nil {
		t.Fatalf("canonical RawOption(rename marker) error = %v", err)
	}
	if present {
		t.Fatalf("canonical rename marker = (%q, true), want absent", value)
	}

	const splitMarker = "@typed-window-split-context"
	if err := receiverSession.SetHook(
		ctx,
		"after-split-window",
		"set-option -t "+receiverSession.ID().String()+" "+splitMarker+" receiver",
	); err != nil {
		t.Fatalf("receiver SetHook(after-split-window) error = %v", err)
	}
	if err := canonicalSession.SetHook(
		ctx,
		"after-split-window",
		"set-option -t "+canonicalSession.ID().String()+" "+splitMarker+" canonical",
	); err != nil {
		t.Fatalf("canonical SetHook(after-split-window) error = %v", err)
	}
	if _, err := receiver.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	value, present, err = receiverSession.RawOption(ctx, splitMarker)
	if err != nil {
		t.Fatalf("receiver RawOption(split marker) error = %v", err)
	}
	if !present || value != "receiver" {
		t.Fatalf("receiver split marker = (%q, %t), want receiver", value, present)
	}
	value, present, err = canonicalSession.RawOption(ctx, splitMarker)
	if err != nil {
		t.Fatalf("canonical RawOption(split marker) error = %v", err)
	}
	if present {
		t.Fatalf("canonical split marker = (%q, true), want absent", value)
	}

	if _, err := receiverSession.SelectWindow(ctx, tmux.SelectWindowRequest{
		WindowID: receiverBase.ID(),
	}); err != nil {
		t.Fatalf("receiver SelectWindow(base) error = %v", err)
	}
	if _, err := canonicalSession.SelectWindow(ctx, tmux.SelectWindowRequest{
		WindowID: canonicalBase.ID(),
	}); err != nil {
		t.Fatalf("canonical SelectWindow(base) error = %v", err)
	}
	if _, err := receiver.Select(ctx); err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	refreshedReceiver, err := receiverSession.Refresh(ctx)
	if err != nil {
		t.Fatalf("receiver Refresh() error = %v", err)
	}
	activeID, _ := refreshedReceiver.Formats().WindowID()
	if activeID != shared.ID() {
		t.Fatalf("receiver active window = %s, want %s", activeID, shared.ID())
	}
	refreshedCanonical, err := canonicalSession.Refresh(ctx)
	if err != nil {
		t.Fatalf("canonical Refresh() error = %v", err)
	}
	activeID, _ = refreshedCanonical.Formats().WindowID()
	if activeID != canonicalBase.ID() {
		t.Fatalf("canonical active window = %s, want %s", activeID, canonicalBase.ID())
	}
}

// libtmux:parity libtmux.window.Window.link
// libtmux:parity libtmux.window.Window.link#parameter-branch:after:a740f436c48a
// libtmux:parity libtmux.window.Window.link#parameter-branch:before:5990b635f38a
// libtmux:parity libtmux.window.Window.link#parameter-branch:detach:3c117da5d4e8
// libtmux:parity libtmux.window.Window.link#parameter-branch:kill_existing:b33764a0fdfd
// libtmux:parity libtmux.window.Window.link#parameter-branch:target_index:f03376e081e2
// libtmux:parity libtmux.window.Window.link#parameter-branch:target_session:038cfaf9ec8b
// libtmux:parity libtmux.window.Window.move_window
// libtmux:parity libtmux.window.Window.move_window#parameter-branch:after:a740f436c48a
// libtmux:parity libtmux.window.Window.move_window#parameter-branch:before:5990b635f38a
// libtmux:parity libtmux.window.Window.move_window#parameter-branch:kill_target:fe59a815083c
// libtmux:parity libtmux.window.Window.move_window#parameter-branch:no_select:8c2e5e8989eb
// libtmux:parity libtmux.window.Window.move_window#parameter-branch:renumber:cb7da9c46286
// libtmux:parity libtmux.window.Window.unlink
// libtmux:parity libtmux.window.Window.unlink#parameter-branch:kill_if_last:5944d5de9da2
//
//libtmux:real-tmux
func TestWindowLinkUnlinkAndMoveAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	sourceSession := snapshot.Sessions()[0]
	shared := relatedWindows(t, sourceSession)[0]
	targetSession, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "window-ops-target", WindowName: "target-base",
	})
	if err != nil {
		t.Fatalf("NewSession(target) error = %v", err)
	}
	targetBaseID, _ := targetSession.Formats().WindowID()
	linkIndex := 7
	if err := shared.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: targetSession.ID(),
		TargetIndex:   &linkIndex,
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	snapshot = mustRealSnapshot(t, server)
	linked := exactRealWindow(t, snapshot, targetSession.ID(), shared.ID())
	if linked.Index() != linkIndex {
		t.Fatalf("linked index = %d, want %d", linked.Index(), linkIndex)
	}
	if err := linked.Unlink(ctx, tmux.UnlinkWindowRequest{}); err != nil {
		t.Fatalf("Unlink() error = %v", err)
	}
	if hasExactRealWindow(mustRealSnapshot(t, server), targetSession.ID(), shared.ID()) {
		t.Fatal("target session retained unlinked window")
	}
	if err := shared.Unlink(ctx, tmux.UnlinkWindowRequest{}); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("Unlink(sole link) error = %v, want ErrCommand", err)
	}

	moving, err := sourceSession.NewWindow(ctx, tmux.NewWindowRequest{Name: new("moving")})
	if err != nil {
		t.Fatalf("NewWindow(moving) error = %v", err)
	}
	moveIndex := 9
	moving, err = moving.Move(ctx, tmux.MoveWindowRequest{
		TargetSession: targetSession.ID(),
		TargetIndex:   &moveIndex,
		NoSelect:      true,
	})
	if err != nil {
		t.Fatalf("Move(cross-session) error = %v", err)
	}
	if moving.SessionID() != targetSession.ID() || moving.Index() != moveIndex {
		t.Fatalf("moved winlink = %s:%d, want %s:%d", moving.SessionID(), moving.Index(), targetSession.ID(), moveIndex)
	}
	refreshedTarget, err := targetSession.Refresh(ctx)
	if err != nil {
		t.Fatalf("target Refresh() error = %v", err)
	}
	activeID, _ := refreshedTarget.Formats().WindowID()
	if activeID != targetBaseID {
		t.Fatalf("NoSelect active window = %s, want %s", activeID, targetBaseID)
	}

	victimIndex := 15
	victim, err := sourceSession.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("move-victim"), Index: &victimIndex,
	})
	if err != nil {
		t.Fatalf("NewWindow(victim) error = %v", err)
	}
	killer, err := sourceSession.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("move-killer"),
	})
	if err != nil {
		t.Fatalf("NewWindow(killer) error = %v", err)
	}
	killer, err = killer.Move(ctx, tmux.MoveWindowRequest{
		TargetIndex: &victimIndex,
		KillTarget:  true,
	})
	if err != nil {
		t.Fatalf("Move(KillTarget) error = %v", err)
	}
	if killer.Index() != victimIndex {
		t.Fatalf("killer index = %d, want %d", killer.Index(), victimIndex)
	}
	if _, err := server.Window(ctx, victim.ID()); !errors.Is(err, tmux.ErrSnapshotNotFound) {
		t.Fatalf("victim lookup error = %v, want ErrSnapshotNotFound", err)
	}

	highIndex := 30
	high, err := sourceSession.NewWindow(ctx, tmux.NewWindowRequest{
		Name: new("renumber"), Index: &highIndex,
	})
	if err != nil {
		t.Fatalf("NewWindow(renumber) error = %v", err)
	}
	if _, err := high.Move(ctx, tmux.MoveWindowRequest{Renumber: true}); err != nil {
		t.Fatalf("Move(Renumber) error = %v", err)
	}
	snapshot = mustRealSnapshot(t, server)
	indices := make([]int, 0)
	relatedWindows := snapshot.Windows()
	for _, window := range relatedWindows {
		if window.SessionID() == sourceSession.ID() {
			indices = append(indices, window.Index())
		}
	}
	slices.Sort(indices)
	for index := 1; index < len(indices); index++ {
		if indices[index] != indices[index-1]+1 {
			t.Fatalf("renumbered indices = %#v, want contiguous", indices)
		}
	}
}

//libtmux:real-tmux
func TestWindowUnlinkTargetsDuplicateWinlinkByIndexAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	shared := relatedWindows(t, snapshot.Sessions()[0])[0]
	target, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "duplicate-winlink-target"})
	if err != nil {
		t.Fatalf("NewSession(target) error = %v", err)
	}
	for _, index := range []int{7, 9} {
		if err := shared.Link(ctx, tmux.LinkWindowRequest{
			TargetSession: target.ID(), TargetIndex: &index, Detach: true,
		}); err != nil {
			t.Fatalf("Link(index %d) error = %v", index, err)
		}
	}
	selectedIndex := 9
	if _, err := target.SelectWindow(ctx, tmux.SelectWindowRequest{Index: &selectedIndex}); err != nil {
		t.Fatalf("SelectWindow(index 9) error = %v", err)
	}

	var indexSeven, indexNine tmux.Window
	for _, window := range mustRealSnapshot(t, server).WindowsByID(shared.ID()) {
		if window.SessionID() != target.ID() {
			continue
		}
		switch window.Index() {
		case 7:
			indexSeven = window
		case 9:
			indexNine = window
		}
	}
	if indexSeven.ID() == "" || indexNine.ID() == "" {
		t.Fatalf("materialized links = (%#v, %#v), want indexes 7 and 9", indexSeven, indexNine)
	}
	if err := indexSeven.Unlink(ctx, tmux.UnlinkWindowRequest{}); err != nil {
		t.Fatalf("Unlink(index 7) error = %v", err)
	}

	retainedNine := false
	for _, window := range mustRealSnapshot(t, server).WindowsByID(shared.ID()) {
		if window.SessionID() != target.ID() {
			continue
		}
		if window.Index() == 7 {
			t.Fatal("index 7 remains after Unlink(index 7)")
		}
		if window.Index() == 9 {
			retainedNine = true
		}
	}
	if !retainedNine {
		t.Fatal("index 9 was unlinked instead of index 7")
	}
}

//libtmux:real-tmux
func TestSessionLastWindowErrorAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session := mustRealSnapshot(t, server).Sessions()[0]

	if _, err := session.LastWindow(ctx); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("LastWindow() error = %v, want ErrCommand", err)
	}
}

func assertRealActiveWindow(
	t *testing.T,
	window tmux.Window,
	sessionID tmux.SessionID,
	windowID tmux.WindowID,
) {
	t.Helper()
	if window.SessionID() != sessionID || window.ID() != windowID {
		t.Fatalf("active winlink = %s:%s, want %s:%s", window.SessionID(), window.ID(), sessionID, windowID)
	}
	if active, _ := window.Active(); !active {
		t.Fatal("active winlink flag = false, want true")
	}
}

func assertRealWindowSize(t *testing.T, window tmux.Window, width, height int) {
	t.Helper()
	gotWidth, _ := window.Width()
	gotHeight, _ := window.Height()
	if gotWidth != width || gotHeight != height {
		t.Fatalf("window size = %dx%d, want %dx%d", gotWidth, gotHeight, width, height)
	}
}

func realWindowLayout(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	windowID tmux.WindowID,
) string {
	t.Helper()
	window, err := server.Window(ctx, windowID)
	if err != nil {
		t.Fatalf("Window() for layout error = %v", err)
	}
	layout, _ := window.Layout()
	return layout
}

func exactRealWindow(
	t *testing.T,
	snapshot tmux.Snapshot,
	sessionID tmux.SessionID,
	windowID tmux.WindowID,
) tmux.Window {
	t.Helper()
	for _, window := range snapshot.WindowsByID(windowID) {
		if window.SessionID() == sessionID {
			return window
		}
	}
	t.Fatalf("missing exact winlink %s:%s", sessionID, windowID)
	return tmux.Window{}
}

func hasExactRealWindow(
	snapshot tmux.Snapshot,
	sessionID tmux.SessionID,
	windowID tmux.WindowID,
) bool {
	for _, window := range snapshot.WindowsByID(windowID) {
		if window.SessionID() == sessionID {
			return true
		}
	}
	return false
}
