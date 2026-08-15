package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/tmux-python/libtmux/golang/internal/tmuxcmd"
)

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
func TestWindowLinkUnlinkMoveBuildExactWinlinkArguments(t *testing.T) {
	t.Parallel()

	index := 3
	tests := []struct {
		name       string
		subcommand string
		wantArgs   []string
		invoke     func(Window) error
	}{
		{
			name:       "link",
			subcommand: "link-window",
			wantArgs:   []string{"link-window", "-s", "$7:3", "-t", "$9"},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{TargetSession: "$9"})
			},
		},
		{
			name:       "link flags and index",
			subcommand: "link-window",
			wantArgs: []string{
				"link-window", "-k", "-a", "-d", "-s", "$7:3", "-t", "$9:3",
			},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{
					TargetSession: "$9",
					TargetIndex:   &index,
					KillExisting:  true,
					After:         true,
					Detach:        true,
				})
			},
		},
		{
			name:       "link before",
			subcommand: "link-window",
			wantArgs:   []string{"link-window", "-b", "-s", "$7:3", "-t", "$9"},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{
					TargetSession: "$9",
					Before:        true,
				})
			},
		},
		{
			name:       "unlink",
			subcommand: "unlink-window",
			wantArgs:   []string{"unlink-window", "-t", "$7:3"},
			invoke: func(window Window) error {
				return window.Unlink(context.Background(), UnlinkWindowRequest{})
			},
		},
		{
			name:       "unlink and kill last",
			subcommand: "unlink-window",
			wantArgs:   []string{"unlink-window", "-k", "-t", "$7:3"},
			invoke: func(window Window) error {
				return window.Unlink(
					context.Background(),
					UnlinkWindowRequest{KillIfLast: true},
				)
			},
		},
		{
			name:       "move default",
			subcommand: "move-window",
			wantArgs:   []string{"move-window", "-s", "$7:3", "-t", "$7:"},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{})
				return err
			},
		},
		{
			name:       "move flags and index",
			subcommand: "move-window",
			wantArgs: []string{
				"move-window", "-a", "-d", "-k", "-s", "$7:3", "-t", "$9:3",
			},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{
					TargetSession: "$9",
					TargetIndex:   &index,
					After:         true,
					NoSelect:      true,
					KillTarget:    true,
				})
				return err
			},
		},
		{
			name:       "move before",
			subcommand: "move-window",
			wantArgs:   []string{"move-window", "-b", "-s", "$7:3", "-t", "$9:"},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{
					TargetSession: "$9",
					Before:        true,
				})
				return err
			},
		},
		{
			name:       "renumber",
			subcommand: "move-window",
			wantArgs:   []string{"move-window", "-r", "-s", "$7:3", "-t", "$9"},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{
					TargetSession: "$9",
					Renumber:      true,
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 7,
			}}}}
			err := test.invoke(Window{
				server:      serverWithRunner(runner),
				sessionID:   "$7",
				windowID:    "@8",
				windowIndex: 3,
			})
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("%s error = %v, want ErrCommand", test.subcommand, err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

func TestWindowLinkMoveValidationPrecedesExecution(t *testing.T) {
	t.Parallel()

	negative := -1
	zero := 0
	tests := []struct {
		name   string
		window Window
		invoke func(Window) error
		want   error
	}{
		{
			name:   "link missing source session",
			window: Window{windowID: "@8"},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{TargetSession: "$9"})
			},
			want: ErrMissingTarget,
		},
		{
			name:   "link missing destination session",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{})
			},
			want: ErrMissingTarget,
		},
		{
			name:   "link malformed destination session",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{TargetSession: "9"})
			},
			want: ErrInvalidTarget,
		},
		{
			name:   "link negative index",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{
					TargetSession: "$9",
					TargetIndex:   &negative,
				})
			},
			want: ErrInvalidServerCommandRequest,
		},
		{
			name:   "link after and before",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{
					TargetSession: "$9",
					After:         true,
					Before:        true,
				})
			},
			want: ErrInvalidServerCommandRequest,
		},
		{
			name:   "move negative index",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{TargetIndex: &negative})
				return err
			},
			want: ErrInvalidServerCommandRequest,
		},
		{
			name:   "move after and before",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{
					After: true, Before: true,
				})
				return err
			},
			want: ErrInvalidServerCommandRequest,
		},
		{
			name:   "renumber with index",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{
					TargetIndex: &zero,
					Renumber:    true,
				})
				return err
			},
			want: ErrInvalidServerCommandRequest,
		},
		{
			name:   "renumber with modifier",
			window: Window{sessionID: "$7", windowID: "@8"},
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{
					NoSelect: true,
					Renumber: true,
				})
				return err
			},
			want: ErrInvalidServerCommandRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			test.window.server = serverWithRunner(runner)
			err := test.invoke(test.window)
			if !errors.Is(err, test.want) {
				t.Fatalf("operation error = %v, want %v", err, test.want)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestWindowLinkMoveRedactUnrepresentableTargetSession(t *testing.T) {
	t.Parallel()

	const secret = "private-destination-session"
	tests := []struct {
		name   string
		invoke func(Window) error
	}{
		{
			name: "link",
			invoke: func(window Window) error {
				return window.Link(context.Background(), LinkWindowRequest{
					TargetSession: SessionID("$9\x00" + secret),
				})
			},
		},
		{
			name: "move",
			invoke: func(window Window) error {
				_, err := window.Move(context.Background(), MoveWindowRequest{
					TargetSession: SessionID("$9\x00" + secret),
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.invoke(Window{
				server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
			})
			if !errors.Is(err, ErrInvalidServerCommandRequest) || errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("window operation error = %v, want only ErrInvalidServerCommandRequest", err)
			}
			var requestError *ServerCommandRequestError
			if !errors.As(err, &requestError) || requestError.Value != "[redacted]" {
				t.Fatalf("window operation error = %#v, want redacted request error", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("window operation runner calls = %d, want 0", calls)
			}
			assertErrorGraphRedacts(t, err, secret)
		})
	}
}

func TestMoveReturnsRefreshedWindow(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	index := 4
	responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}
	responses = append(responses, lifecycleLookupResponses(
		t,
		version,
		"list-windows",
		map[string]string{
			"session_id": "$9", "window_id": "@8", "window_index": "4",
		},
	)...)
	runner := &versionQueueRunner{responses: responses}
	window, err := (Window{
		server:    serverWithRunner(runner),
		sessionID: "$7",
		windowID:  "@8",
	}).Move(context.Background(), MoveWindowRequest{
		TargetSession: "$9",
		TargetIndex:   &index,
	})
	if err != nil {
		t.Fatalf("Move() error = %v", err)
	}
	if window.sessionID != "$9" || window.windowID != "@8" || window.windowIndex != 4 {
		t.Fatalf("Move() window = %s:%s:%d, want $9:@8:4", window.sessionID, window.windowID, window.windowIndex)
	}
}

func TestMoveReturnsReceiverWhenRefreshFails(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 0}},
		{err: context.Canceled},
	}}
	receiver := Window{
		server:      serverWithRunner(runner).WithStrictErrors(),
		sessionID:   "$7",
		windowID:    "@8",
		windowIndex: 2,
	}
	window, err := receiver.Move(context.Background(), MoveWindowRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Move() error = %v, want context canceled refresh error", err)
	}
	if window.sessionID != receiver.sessionID || window.windowID != receiver.windowID ||
		window.windowIndex != receiver.windowIndex || window.Server() != receiver.Server() {
		t.Fatalf("Move() partial result = %#v, want receiver %#v", window, receiver)
	}
}
