//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.pane.Pane.choose_buffer
// libtmux:parity libtmux.pane.Pane.choose_client
// libtmux:parity libtmux.pane.Pane.choose_tree
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:filter_expression:741088570e6e
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:format_string:7284ef554e76
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:reverse:72696e193812
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:sessions_collapsed:dc36af30b6c2
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:sort_order:bc74294d9429
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:windows_collapsed:063263fb151d
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:zoom:629cd868ae3d
// libtmux:parity libtmux.pane.Pane.clock_mode
// libtmux:parity libtmux.pane.Pane.customize_mode
// libtmux:parity libtmux.pane.Pane.display_panes
// libtmux:parity libtmux.pane.Pane.display_panes#parameter-branch:duration:4675d6e179b9
// libtmux:parity libtmux.pane.Pane.display_panes#parameter-branch:no_select:8c2e5e8989eb
// libtmux:parity libtmux.pane.Pane.find_window
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:case_insensitive:cf484fe9e540
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:match_content:d19faeac104a
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:match_name:d259fbe84150
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:match_title:c066c53a42c5
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:regex:eb6f0b4408a2
//
//libtmux:real-tmux
func TestPaneModesAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	control := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	_ = control
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	pane := panes[0]
	target := pane.SessionID().String() + ":" + pane.WindowID().String() + "." + pane.ID().String()

	bufferName := "go-mode-buffer"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{
		Data: "mode data", Name: &bufferName,
	}); err != nil {
		t.Fatalf("SetBuffer() error = %v", err)
	}
	windowName, _ := pane.Formats().WindowName()
	entries := []struct {
		name  string
		enter func() error
	}{
		{name: "copy", enter: func() error {
			return pane.CopyMode(ctx, tmux.CopyModeRequest{})
		}},
		{name: "clock", enter: func() error { return pane.ClockMode(ctx) }},
		{name: "choose-buffer", enter: func() error { return pane.ChooseBuffer(ctx) }},
		{name: "choose-client", enter: func() error { return pane.ChooseClient(ctx) }},
		{name: "choose-tree", enter: func() error {
			return pane.ChooseTree(ctx, tmux.ChooseTreeRequest{Sort: tmux.TreeSortName})
		}},
		{name: "customize", enter: func() error { return pane.CustomizeMode(ctx) }},
		{name: "find-window", enter: func() error {
			return pane.FindWindow(ctx, tmux.FindWindowRequest{Match: windowName})
		}},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			if err := entry.enter(); err != nil {
				t.Fatalf("enter mode error = %v", err)
			}
			waitRealPaneMode(ctx, t, server, target, "1")
			if entry.name == "clock" {
				requireRealPaneModeCommand(ctx, t, server, "send-keys", "-t", target, "q")
			} else if err := pane.CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
				t.Fatalf("cancel mode error = %v", err)
			}
			waitRealPaneMode(ctx, t, server, target, "0")
		})
	}
	if err := pane.FindWindow(ctx, tmux.FindWindowRequest{Match: "-go-missing"}); err != nil {
		t.Fatalf("FindWindow(leading dash) error = %v", err)
	}
	if err := pane.CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		t.Fatalf("cancel leading-dash search mode error = %v", err)
	}

	duration := 1
	if err := pane.DisplayPanes(ctx, tmux.DisplayPanesRequest{
		Duration: &duration, NoSelect: true,
	}); err != nil {
		t.Fatalf("DisplayPanes() error = %v", err)
	}
}

//libtmux:real-tmux
func TestCopyModeCancelTakesPrecedenceAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if !version.AtLeast(mustPaneModeVersion(t, "3.5")) {
		t.Skip("PageDown requires tmux 3.5")
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	pane := panes[0]
	target := pane.SessionID().String() + ":" + pane.WindowID().String() + "." + pane.ID().String()
	if err := pane.CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
		t.Fatalf("CopyMode() error = %v", err)
	}
	waitRealPaneMode(ctx, t, server, target, "1")

	sourcePane := pane.ID()
	if err := pane.CopyMode(ctx, tmux.CopyModeRequest{
		ScrollUp:     true,
		ExitOnBottom: true,
		MouseDrag:    true,
		PageDown:     true,
		SourcePane:   sourcePane,
		Cancel:       true,
	}); err != nil {
		t.Fatalf("CopyMode(combined Cancel) error = %v", err)
	}
	waitRealPaneMode(ctx, t, server, target, "0")
}

//libtmux:real-tmux
func TestCopyModeCancelWithStaleSourceLeavesModeActiveAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	pane := panes[0]
	target := pane.SessionID().String() + ":" + pane.WindowID().String() + "." + pane.ID().String()
	if err := pane.CopyMode(ctx, tmux.CopyModeRequest{}); err != nil {
		t.Fatalf("CopyMode() error = %v", err)
	}
	waitRealPaneMode(ctx, t, server, target, "1")

	staleSource := tmux.PaneID("%999999")
	err = pane.CopyMode(ctx, tmux.CopyModeRequest{
		SourcePane: staleSource,
		Cancel:     true,
	})
	var commandError *tmux.CommandError
	if !errors.As(err, &commandError) || commandError.Subcommand != "copy-mode" {
		t.Fatalf("CopyMode(Cancel, stale SourcePane) error = %#v, want copy-mode CommandError", err)
	}
	waitRealPaneMode(ctx, t, server, target, "1")
	if err := pane.CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		t.Fatalf("cleanup CopyMode(Cancel) error = %v", err)
	}
	waitRealPaneMode(ctx, t, server, target, "0")
}

// libtmux:parity libtmux.pane.Pane.copy_mode
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:cancel:d480d98efb7a
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:exit_on_bottom:da23cc28e1b5
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:mouse_drag:767a4ddd3b1d
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:page_down:62888fd473ff
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:scroll_up:64208af261d8
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:source_pane:423c0a9156e7
// libtmux:parity libtmux.pane.Pane.copy_mode#version-branch:tmux-version:820df7664d30
// libtmux:parity libtmux.pane.Pane.copy_mode#warning:07225fecc4d2
//
//libtmux:real-tmux
func TestCopyModeUsesLinkedReceiverAndSourcePaneAgainstRealTmux(t *testing.T) {
	base := tmuxtest.NewServer(context.Background(), t)
	warnings := make([]tmux.Warning, 0, 1)
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: base.ProcessEnvironment(),
		WarningHandler: func(warning tmux.Warning) {
			warnings = append(warnings, warning)
		},
	}).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snapshot := mustRealSnapshot(t, server)
	firstSession := snapshot.Sessions()[0]
	shared := firstSession.Windows()[0]
	secondSession, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "go-mode-linked-target",
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := shared.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: secondSession.ID(),
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	canonical, err := server.Window(ctx, shared.ID())
	if err != nil {
		t.Fatalf("Window() error = %v", err)
	}
	var canonicalSession, receiverSession tmux.Session
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
	tmuxtest.NewControlMode(context.Background(), t, server, canonicalSession)

	snapshot = mustRealSnapshot(t, server)
	receiver := exactRealWindow(t, snapshot, receiverSession.ID(), shared.ID())
	targetPanes := receiver.Panes()
	if len(targetPanes) != 1 {
		t.Fatalf("receiver panes = %#v, want one", targetPanes)
	}
	targetPane := targetPanes[0]
	sourcePane, err := receiver.SplitPane(ctx, tmux.SplitPaneRequest{Command: "cat"})
	if err != nil {
		t.Fatalf("SplitPane(source) error = %v", err)
	}
	seedRealModePane(
		ctx,
		t,
		server,
		targetPane.ID(),
		"go-mode-target-ready",
		"printf go-mode-target-final",
	)
	seedRealModePane(
		ctx,
		t,
		server,
		sourcePane.ID(),
		"go-mode-source-ready",
		strings.Join([]string{
			"i=1",
			"while [ $i -le 80 ]",
			"do printf 'go-mode-source-%03d\\n' \"$i\"",
			"i=$((i+1))",
			"done",
			"printf go-mode-source-final",
		}, "; "),
	)

	const hookMarker = "@go-mode-linked-context"
	if err := receiverSession.SetHook(
		ctx,
		"after-copy-mode",
		"set-option -t "+receiverSession.ID().String()+" "+hookMarker+" receiver",
	); err != nil {
		t.Fatalf("receiver SetHook(after-copy-mode) error = %v", err)
	}
	if err := canonicalSession.SetHook(
		ctx,
		"after-copy-mode",
		"set-option -t "+canonicalSession.ID().String()+" "+hookMarker+" canonical",
	); err != nil {
		t.Fatalf("canonical SetHook(after-copy-mode) error = %v", err)
	}

	sourcePaneID := sourcePane.ID()
	if err := targetPane.CopyMode(ctx, tmux.CopyModeRequest{SourcePane: sourcePaneID}); err != nil {
		t.Fatalf("CopyMode(SourcePane) error = %v", err)
	}
	target := targetPane.SessionID().String() + ":" + targetPane.WindowID().String() +
		"." + targetPane.ID().String()
	waitRealPaneMode(ctx, t, server, target, "1")
	value, present, err := receiverSession.RawOption(ctx, hookMarker)
	if err != nil {
		t.Fatalf("receiver RawOption(hook marker) error = %v", err)
	}
	if !present || value != "receiver" {
		t.Fatalf("receiver hook marker = (%q, %t), want receiver", value, present)
	}
	value, present, err = canonicalSession.RawOption(ctx, hookMarker)
	if err != nil {
		t.Fatalf("canonical RawOption(hook marker) error = %v", err)
	}
	if present {
		t.Fatalf("canonical hook marker = (%q, true), want absent", value)
	}

	requireRealPaneModeCommand(ctx, t, server, "send-keys", "-X", "-t", target, "select-line")
	requireRealPaneModeCommand(
		ctx,
		t,
		server,
		"send-keys",
		"-X",
		"-t",
		target,
		"copy-selection-and-cancel",
	)
	waitRealPaneMode(ctx, t, server, target, "0")
	bufferResult, bufferErr := server.Cmd(ctx, "show-buffer")
	requireRealPaneModeCommandSuccess(t, "show-buffer", bufferResult, bufferErr)
	copied := strings.TrimSpace(strings.Join(bufferResult.Stdout, "\n"))
	if !strings.Contains(copied, "go-mode-source-final") ||
		strings.Contains(copied, "go-mode-target-final") {
		t.Fatalf("copied line = %q, want source-pane marker only", copied)
	}

	if err := targetPane.CopyMode(ctx, tmux.CopyModeRequest{
		ScrollUp:   true,
		SourcePane: sourcePaneID,
	}); err != nil {
		t.Fatalf("CopyMode(ScrollUp, SourcePane) error = %v", err)
	}
	waitRealPaneMode(ctx, t, server, target, "1")
	beforePageDown := realPaneScrollPosition(ctx, t, server, target)
	if beforePageDown <= 0 {
		t.Fatalf("scroll position before PageDown = %d, want positive", beforePageDown)
	}
	warnings = nil
	if err := targetPane.CopyMode(ctx, tmux.CopyModeRequest{PageDown: true}); err != nil {
		t.Fatalf("CopyMode(PageDown) error = %v", err)
	}
	afterPageDown := realPaneScrollPosition(ctx, t, server, target)
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version35 := mustPaneModeVersion(t, "3.5")
	if version.AtLeast(version35) {
		if afterPageDown >= beforePageDown {
			t.Fatalf(
				"scroll position after PageDown = %d, want less than %d",
				afterPageDown,
				beforePageDown,
			)
		}
		if len(warnings) != 0 {
			t.Fatalf("CopyMode(PageDown) warnings = %#v, want none", warnings)
		}
	} else {
		if afterPageDown != beforePageDown {
			t.Fatalf(
				"scroll position after unsupported PageDown = %d, want %d",
				afterPageDown,
				beforePageDown,
			)
		}
		if len(warnings) != 1 || warnings[0].Feature != "page_down" {
			t.Fatalf("CopyMode(PageDown) warnings = %#v, want page_down warning", warnings)
		}
	}
	if err := targetPane.CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		t.Fatalf("CopyMode(Cancel) error = %v", err)
	}
	waitRealPaneMode(ctx, t, server, target, "0")
}

func seedRealModePane(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	paneID tmux.PaneID,
	lock string,
	body string,
) {
	t.Helper()
	requireRealPaneModeCommand(ctx, t, server, "wait-for", "-L", lock)
	script := body + "; tmux wait-for -U " + lock + "; exec cat"
	requireRealPaneModeCommand(ctx, t, server, "respawn-pane", "-k", "-t", paneID.String(), script)
	requireRealPaneModeCommand(ctx, t, server, "wait-for", "-L", lock)
	requireRealPaneModeCommand(ctx, t, server, "wait-for", "-U", lock)
}

func realPaneScrollPosition(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	target string,
) int {
	t.Helper()
	result, err := server.Cmd(ctx, "display-message", "-p", "-t", target, "#{scroll_position}")
	requireRealPaneModeCommandSuccess(t, "display-message scroll_position", result, err)
	if len(result.Stdout) != 1 {
		t.Fatalf("scroll_position stdout = %#v, want one row", result.Stdout)
	}
	position, err := strconv.Atoi(result.Stdout[0])
	if err != nil {
		t.Fatalf("parse scroll_position %q: %v", result.Stdout[0], err)
	}
	return position
}

func waitRealPaneMode(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	target string,
	want string,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := server.Cmd(
			ctx, "display-message", "-p", "-t", target, "#{pane_in_mode}",
		)
		if err == nil && result.ExitCode == 0 && len(result.Stderr) == 0 &&
			len(result.Stdout) == 1 && result.Stdout[0] == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane_in_mode did not become %s: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}

func requireRealPaneModeCommand(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	arguments ...string,
) {
	t.Helper()
	result, err := server.Cmd(ctx, arguments...)
	requireRealPaneModeCommandSuccess(t, strconv.Quote(arguments[0]), result, err)
}

func requireRealPaneModeCommandSuccess(
	t *testing.T,
	operation string,
	result tmux.CommandResult,
	err error,
) {
	t.Helper()
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf(
			"tmux %s = (exit %d, stderr %#v, %v)",
			operation, result.ExitCode, result.Stderr, err,
		)
	}
}
