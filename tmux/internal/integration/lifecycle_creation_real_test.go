//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.session.Session.new_window
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:attach:58cb72758f2e
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:direction,start_directory,target_window,window_index,window_name,window_shell:ae7296cf6540
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:direction:9447fa04b73f
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:kill_existing:b33764a0fdfd
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:select_existing:c9a3682abcb6
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:start_directory:bef78f09efe5
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:target_window,window_index:3fc216416bbd
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:target_window:e1d6df638259
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:window_index:268a5a9f5c31
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:window_index:268a5a9f5c31:2
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:window_name:a8ac125187c4
// libtmux:parity libtmux.session.Session.new_window#parameter-branch:window_shell:dd6ca5846152
//
// libtmux:parity libtmux.window.Window.new_window
//
//libtmux:real-tmux
func TestExtendedCreationOptionsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}

	width, height := 101, 31
	marker := t.TempDir() + "/session-environment"
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name:        "phase6-size",
		Width:       width,
		Height:      height,
		Environment: map[string]string{"PHASE6_ENV": "session-value"},
		Command:     "printf '%s' \"$PHASE6_ENV\" > " + strconv.Quote(marker) + "; sleep 30",
	})
	if err != nil {
		t.Fatalf("NewSession(extended) error = %v", err)
	}
	minimum33, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	wantWidth, wantHeight := 101, 31
	if !version.AtLeast(minimum33) {
		// tmux 3.2a stores -x/-y in default-size but creates the first
		// detached window at its historical 80x24 default (23 pane rows).
		wantWidth, wantHeight = 80, 23
	}
	if got, _ := session.Formats().WindowWidth(); got != wantWidth {
		t.Fatalf("new session window width = %d, want %d on tmux %s", got, wantWidth, version)
	}
	if got, _ := session.Formats().WindowHeight(); got != wantHeight {
		t.Fatalf("new session window height = %d, want %d on tmux %s", got, wantHeight, version)
	}
	if got := waitForProcessFile(ctx, t, marker); got != "session-value" {
		t.Fatalf("new session environment marker = %q, want session-value", got)
	}

	session, err = mustRealSnapshot(t, server).SessionByID(session.ID())
	if err != nil {
		t.Fatalf("Snapshot().SessionByID() error = %v", err)
	}
	windows := relatedWindows(t, session)
	if len(windows) != 1 {
		t.Fatalf("new session windows = %d, want 1", len(windows))
	}
	windowMarker := t.TempDir() + "/window-environment"
	created, err := windows[0].NewWindow(ctx, tmux.NewWindowRequest{
		Name:        new("phase6-after"),
		Direction:   tmux.NewWindowDirectionAfter,
		Environment: map[string]string{"PHASE6_ENV": "window-value"},
		Command:     "printf '%s' \"$PHASE6_ENV\" > " + strconv.Quote(windowMarker) + "; sleep 30",
	})
	if err != nil {
		t.Fatalf("Window.NewWindow(extended) error = %v", err)
	}
	if created.SessionID() != session.ID() || created.Index() != windows[0].Index()+1 {
		t.Fatalf("Window.NewWindow() = %#v, want receiver session immediately after index %d", created, windows[0].Index())
	}
	if got := waitForProcessFile(ctx, t, windowMarker); got != "window-value" {
		t.Fatalf("new window environment marker = %q, want window-value", got)
	}

	t.Run("Window.NewWindow zero direction targets receiver", func(t *testing.T) {
		_, err := windows[0].NewWindow(ctx, tmux.NewWindowRequest{})
		var commandError *tmux.CommandError
		if !errors.As(err, &commandError) ||
			commandError.Subcommand != "new-window" || commandError.Result.ExitCode == 0 {
			t.Fatalf("Window.NewWindow(zero) error = %#v, want nonzero new-window CommandError", err)
		}
		unchanged, err := server.Window(ctx, windows[0].ID())
		if err != nil {
			t.Fatalf("lookup receiver after zero-direction error = %v", err)
		}
		if unchanged.Index() != windows[0].Index() {
			t.Fatalf("receiver index after zero-direction error = %d, want %d", unchanged.Index(), windows[0].Index())
		}

		replacement, err := windows[0].NewWindow(ctx, tmux.NewWindowRequest{
			Name:         new("phase6-replacement"),
			KillExisting: true,
			Command:      "sleep 30",
		})
		if err != nil {
			t.Fatalf("Window.NewWindow(KillExisting) error = %v", err)
		}
		if replacement.SessionID() != windows[0].SessionID() ||
			replacement.Index() != windows[0].Index() ||
			replacement.ID() == windows[0].ID() {
			t.Fatalf(
				"Window.NewWindow(KillExisting) = %#v, want replacement at %s index %d",
				replacement,
				windows[0].SessionID(),
				windows[0].Index(),
			)
		}
		if _, err := server.Window(ctx, windows[0].ID()); !errors.Is(err, tmux.ErrNotFound) {
			t.Fatalf("lookup replaced receiver error = %v, want ErrNotFound", err)
		}
	})
}

//libtmux:real-tmux
func TestSelectExistingWindowIdentityAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	name := "phase6-existing"
	existing, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{Name: &name})
	if err != nil {
		t.Fatalf("NewWindow(existing) error = %v", err)
	}
	before := len(mustRealSnapshot(t, server).Windows())
	selected, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{
		Name: &name, SelectExisting: true,
	})
	if err != nil {
		t.Fatalf("NewWindow(SelectExisting) error = %v", err)
	}
	if selected.SessionID() != sessions[0].ID() || selected.ID() != existing.ID() {
		t.Fatalf("selected window = %#v, want exact existing %s:%s", selected, sessions[0].ID(), existing.ID())
	}
	if after := len(mustRealSnapshot(t, server).Windows()); after != before {
		t.Fatalf("window count after SelectExisting = %d, want %d", after, before)
	}

	index := 50
	indexed, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{
		Name: &name, Index: &index, SelectExisting: true,
	})
	if err != nil {
		t.Fatalf("NewWindow(indexed SelectExisting) error = %v", err)
	}
	if indexed.ID() == existing.ID() || indexed.Index() != index {
		t.Fatalf("indexed SelectExisting window = %#v, want a new window at index %d", indexed, index)
	}
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	// tmux 3.8 extended -S to match an exact -t target, not only a window
	// name (cmd-new-window.c, 3.7c to 3.8-rc: "if idx != -1: select
	// winlink_find_by_index(idx)" ahead of the name-matching fallback).
	// existing.NewWindow's target is always exact and always already
	// occupied by existing itself, so from that version on tmux selects
	// existing instead of creating a duplicate; before it, the exact-target
	// form never matched -S at all and always created one.
	minimum38, err := tmux.ParseVersion("3.8")
	if err != nil {
		t.Fatal(err)
	}
	exact, err := existing.NewWindow(ctx, tmux.NewWindowRequest{
		Name: &name, Direction: tmux.NewWindowDirectionAfter, SelectExisting: true,
	})
	if err != nil {
		t.Fatalf("Window.NewWindow(SelectExisting) error = %v", err)
	}
	wantCount := before + 2
	if version.AtLeast(minimum38) {
		if exact.ID() != existing.ID() {
			t.Fatalf(
				"exact-target SelectExisting window on tmux %s = %s, want the receiver %s",
				version, exact.ID(), existing.ID(),
			)
		}
		wantCount = before + 1
	} else if exact.ID() == existing.ID() {
		t.Fatalf(
			"exact-target SelectExisting window on tmux %s = %s, want a newly printed identity",
			version, exact.ID(),
		)
	}
	if after := len(mustRealSnapshot(t, server).Windows()); after != wantCount {
		t.Fatalf("window count after explicit SelectExisting targets on tmux %s = %d, want %d",
			version, after, wantCount)
	}
	minimum34, err := tmux.ParseVersion("3.4")
	if err != nil {
		t.Fatal(err)
	}
	formatName := "#{session_name}"
	storedFormatName := formatName
	if version.AtLeast(minimum34) {
		storedFormatName, _ = sessions[0].Name()
	} else {
		// tmux 3.2a compares the raw -n operand for -S, although spawn expands
		// that operand when it creates a window. Escape the creation operand
		// so the stored name exercises the raw selection rule.
		storedFormatName = "##{session_name}"
	}
	formatted, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{Name: &storedFormatName})
	if err != nil {
		t.Fatalf("NewWindow(formatted selection target) error = %v", err)
	}
	selected, err = sessions[0].NewWindow(ctx, tmux.NewWindowRequest{
		Name: &formatName, SelectExisting: true,
	})
	if err != nil {
		t.Fatalf("NewWindow(formatted SelectExisting) error = %v", err)
	}
	if selected.ID() != formatted.ID() {
		t.Fatalf("formatted selected window = %s, want %s on tmux %s", selected.ID(), formatted.ID(), version)
	}

	if _, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{Name: &name}); err != nil {
		t.Fatalf("NewWindow(duplicate name) error = %v", err)
	}
	_, err = sessions[0].NewWindow(ctx, tmux.NewWindowRequest{
		Name: &name, SelectExisting: true,
	})
	var commandError *tmux.CommandError
	if !errors.As(err, &commandError) || commandError.Subcommand != "new-window" {
		t.Fatalf("duplicate SelectExisting error = %#v, want new-window CommandError", err)
	}
}

// libtmux:parity libtmux.pane.Pane.new_pane
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:active_border_style:8d2921e88b8f
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:attach:58cb72758f2e
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:empty:523641206739
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:height:584748e889a5
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:inactive_border_style:8d96621af6a2
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:keep:e4b8e377c591
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:message:4387413839d7
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:shell:613b2dd997a5
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:start_directory:bef78f09efe5
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:style:2fb8c408bf6c
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:target:3fc216416bbd
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:width:c4a3db243018
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:x:c2891f2208b1
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:y:0cf048966732
// libtmux:parity libtmux.pane.Pane.new_pane#parameter-branch:zoom:629cd868ae3d
// libtmux:parity libtmux.pane.Pane.new_pane#version-branch:tmux-version:9dfb8df17e6f
// libtmux:parity libtmux.pane.Pane.split
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:active_border_style,inactive_border_style,keep,message,style:002b9dbf15c8
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:attach:58cb72758f2e
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:direction,percentage,shell,size,start_directory,target:ba24d263cea7
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:direction,percentage,shell,size,start_directory,target:ef0222e93963
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:direction:1e6c737950b2
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:empty:523641206739
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:full_window_split:b63e74e2d163
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:keep:e4b8e377c591
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:percentage,size:6782b0981e74
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:percentage:714e6fb7d801
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:shell:613b2dd997a5
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:size:af48b30c8b98
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:start_directory:bef78f09efe5
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:target:3fc216416bbd
// libtmux:parity libtmux.pane.Pane.split#parameter-branch:zoom:629cd868ae3d
// libtmux:parity libtmux.pane.Pane.split#version-branch:tmux-version:c6a18af85027
// libtmux:parity libtmux.pane.Pane.split#version-branch:tmux-version:c6a18af85027:2
// libtmux:parity libtmux.pane.Pane.split#warning:39e08b1dd04b
// libtmux:parity libtmux.pane.Pane.split#warning:c85dca20c257
// libtmux:parity libtmux.window.Window.new_pane
//
//libtmux:real-tmux
func TestLinkedSplitAndFloatingPaneContextAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	hostWindow := relatedWindows(t, sessions[0])[0]
	guest, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "phase6-guest"})
	if err != nil {
		t.Fatalf("NewSession(guest) error = %v", err)
	}
	if err := hostWindow.Link(ctx, tmux.LinkWindowRequest{
		TargetSession: guest.ID(),
		Detach:        true,
	}); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	linked := exactRealWindow(t, mustRealSnapshot(t, server), guest.ID(), hostWindow.ID())
	panes := relatedPanes(t, linked)
	if len(panes) != 1 {
		t.Fatalf("linked panes = %d, want 1", len(panes))
	}
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	minimum, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}

	// A split naming a pane style, and one asking for an empty pane, need tmux
	// 3.7. Below it the request is refused rather than carried out without
	// them, which would put a shell in the pane the caller asked to leave
	// empty. Both refusals are checked here; the linked-context assertions
	// below them need a split that ran.
	style := "bg=blue"
	styled := tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight,
		Command:   "sleep 30",
		Style:     &style,
	}
	if !version.AtLeast(minimum) {
		if _, err := panes[0].Split(ctx, styled); !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("Pane.Split(Style) on tmux %s error = %v, want ErrVersionTooLow", version, err)
		}
		_, err := linked.SplitPane(ctx, tmux.SplitPaneRequest{Empty: true})
		if !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("Window.SplitPane(Empty) on tmux %s error = %v, want ErrVersionTooLow", version, err)
		}
		return
	}

	created, err := panes[0].Split(ctx, styled)
	if err != nil {
		t.Fatalf("Pane.Split() error = %v", err)
	}
	if created.SessionID() != guest.ID() || created.WindowID() != linked.ID() {
		t.Fatalf("Pane.Split() = %#v, want linked guest context", created)
	}
	empty, err := linked.SplitPane(ctx, tmux.SplitPaneRequest{Empty: true})
	if err != nil {
		t.Fatalf("Window.SplitPane(Empty) error = %v", err)
	}
	if empty.SessionID() != guest.ID() || empty.WindowID() != linked.ID() {
		t.Fatalf("Window.SplitPane(Empty) = %#v, want linked guest context", empty)
	}

	floating, err := linked.NewPane(ctx, tmux.NewPaneRequest{
		Width: tmux.Ptr(20), Height: tmux.Ptr(6), Command: "sleep 30",
	})
	if !version.AtLeast(minimum) {
		if !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("Window.NewPane() error = %v, want ErrVersionTooLow on tmux %s", err, version)
		}
		return
	}
	if err != nil {
		t.Fatalf("Window.NewPane() error = %v", err)
	}
	if floating.SessionID() != guest.ID() || floating.WindowID() != linked.ID() {
		t.Fatalf("Window.NewPane() = %#v, want linked guest context", floating)
	}
	if flag, _ := floating.FloatingFlag(); !flag {
		t.Fatal("floating pane flag = false, want true")
	}
}

// TestLifecycleCommandsTreatLeadingDashCommandAsPositionalAgainstRealTmux
// proves the new-window, split-window, new-session, and respawn "--" guards
// against a live tmux. Each Command becomes the new pane's own argv
// (spawn.c), so #{pane_start_command} reads back the exact string tmux
// stored, dash and all - without the guard, tmux's own parser refuses the
// value as flags before creating anything, which the raw control reproduces.
//
// remain-on-exit keeps the pane around to read: /bin/sh -c rejects a "-c"
// operand that itself starts with "-" as an option to the shell binary
// itself, a limitation of the shell tmux execs and orthogonal to tmux's own
// argument parsing, so the process exits almost immediately either way.
//
//libtmux:real-tmux
func TestLifecycleCommandsTreatLeadingDashCommandAsPositionalAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := server.Cmd(ctx, "set-option", "-g", "remain-on-exit", "on"); err != nil {
		t.Fatal("set-option remain-on-exit error")
	}

	// "z" is not a flag letter for any of new-window, split-window,
	// new-session, or respawn-pane/window, so the unguarded control below is
	// refused as an unknown flag on every one rather than silently consumed
	// as some other flag's attached value.
	const command = "-zzz-not-a-flag"

	for _, test := range []struct {
		name string
		// raw reproduces the unguarded call directly against tmux and
		// returns its result.
		raw func(ctx context.Context, session tmux.Session) (tmux.CommandResult, error)
		// guarded performs the equivalent guarded call and returns the exact
		// target to read #{pane_start_command} back from.
		guarded func(ctx context.Context, session tmux.Session) (string, error)
	}{
		{
			name: "new-window",
			raw: func(ctx context.Context, session tmux.Session) (tmux.CommandResult, error) {
				return server.Cmd(ctx, "new-window", "-t", session.ID().String(), command)
			},
			guarded: func(ctx context.Context, session tmux.Session) (string, error) {
				window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Command: command})
				if err != nil {
					return "", err
				}
				return session.ID().String() + ":" + window.ID().String(), nil
			},
		},
		{
			name: "split-window",
			raw: func(ctx context.Context, session tmux.Session) (tmux.CommandResult, error) {
				panes, err := session.SearchPanes(ctx, nil)
				if err != nil || len(panes) != 1 {
					t.Fatalf("Session.SearchPanes() = (%#v, %v), want one pane", panes, err)
				}
				target := panes[0].SessionID().String() + ":" + panes[0].WindowID().String() +
					"." + panes[0].ID().String()
				return server.Cmd(ctx, "split-window", "-t", target, command)
			},
			guarded: func(ctx context.Context, session tmux.Session) (string, error) {
				panes, err := session.SearchPanes(ctx, nil)
				if err != nil || len(panes) != 1 {
					t.Fatalf("Session.SearchPanes() = (%#v, %v), want one pane", panes, err)
				}
				pane, err := panes[0].Split(ctx, tmux.SplitPaneRequest{Command: command})
				if err != nil {
					return "", err
				}
				return pane.SessionID().String() + ":" + pane.WindowID().String() +
					"." + pane.ID().String(), nil
			},
		},
		{
			name: "new-session",
			raw: func(ctx context.Context, _ tmux.Session) (tmux.CommandResult, error) {
				return server.Cmd(ctx, "new-session", "-d", command)
			},
			guarded: func(ctx context.Context, _ tmux.Session) (string, error) {
				created, err := server.NewSession(ctx, tmux.NewSessionRequest{Command: command})
				if err != nil {
					return "", err
				}
				return created.ID().String(), nil
			},
		},
		{
			name: "respawn-pane",
			raw: func(ctx context.Context, session tmux.Session) (tmux.CommandResult, error) {
				panes, err := session.SearchPanes(ctx, nil)
				if err != nil || len(panes) != 1 {
					t.Fatalf("Session.SearchPanes() = (%#v, %v), want one pane", panes, err)
				}
				target := panes[0].SessionID().String() + ":" + panes[0].WindowID().String() +
					"." + panes[0].ID().String()
				return server.Cmd(ctx, "respawn-pane", "-k", "-t", target, command)
			},
			guarded: func(ctx context.Context, session tmux.Session) (string, error) {
				panes, err := session.SearchPanes(ctx, nil)
				if err != nil || len(panes) != 1 {
					t.Fatalf("Session.SearchPanes() = (%#v, %v), want one pane", panes, err)
				}
				pane, err := panes[0].Respawn(ctx, tmux.RespawnRequest{
					Command: tmux.Ptr(command), Kill: true,
				})
				if err != nil {
					return "", err
				}
				return pane.SessionID().String() + ":" + pane.WindowID().String() +
					"." + pane.ID().String(), nil
			},
		},
		{
			name: "respawn-window",
			raw: func(ctx context.Context, session tmux.Session) (tmux.CommandResult, error) {
				windows, err := session.SearchWindows(ctx, nil)
				if err != nil || len(windows) != 1 {
					t.Fatalf("Session.SearchWindows() = (%#v, %v), want one window", windows, err)
				}
				target := windows[0].SessionID().String() + ":" + windows[0].ID().String()
				return server.Cmd(ctx, "respawn-window", "-k", "-t", target, command)
			},
			guarded: func(ctx context.Context, session tmux.Session) (string, error) {
				windows, err := session.SearchWindows(ctx, nil)
				if err != nil || len(windows) != 1 {
					t.Fatalf("Session.SearchWindows() = (%#v, %v), want one window", windows, err)
				}
				window, err := windows[0].Respawn(ctx, tmux.RespawnRequest{
					Command: tmux.Ptr(command), Kill: true,
				})
				if err != nil {
					return "", err
				}
				return window.SessionID().String() + ":" + window.ID().String(), nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := server.NewSession(ctx, tmux.NewSessionRequest{})
			if err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}

			raw, err := test.raw(ctx, session)
			if err != nil || raw.ExitCode == 0 {
				t.Fatalf("unguarded %s = (%#v, %v), want a parse failure", test.name, raw, err)
			}

			target, err := test.guarded(ctx, session)
			if err != nil {
				t.Fatalf("guarded %s error = %v", test.name, err)
			}
			result, err := server.Cmd(ctx, "display-message", "-p", "-t", target, "-F", "#{pane_start_command}")
			if err != nil || result.ExitCode != 0 || len(result.Stdout) != 1 {
				t.Fatalf("pane_start_command(%s) = (%#v, %v)", target, result, err)
			}
			if result.Stdout[0] != command {
				t.Fatalf("pane_start_command(%s) = %q, want %q", target, result.Stdout[0], command)
			}
		})
	}
}
