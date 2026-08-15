package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/tmux-python/libtmux/golang/internal/tmuxcmd"
)

// libtmux:parity libtmux.window.Window.next_layout
// libtmux:parity libtmux.window.Window.previous_layout
// libtmux:parity libtmux.window.Window.select_layout
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:layout,next_layout,previous_layout,spread:0fa948c7463a
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:layout:2a9f9e28d66a
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:next_layout:e5241b7cc5f9
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:previous_layout:146fb5ba152c
// libtmux:parity libtmux.window.Window.select_layout#parameter-branch:spread:ef0ecf84f4e8
func TestWindowLayoutCommandsBuildLiteralArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantArgs []string
		invoke   func(Window) error
	}{
		{
			name:     "reapply last preset",
			wantArgs: []string{"select-layout", "-t", "$7:0"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{})
			},
		},
		{
			name:     "named layout",
			wantArgs: []string{"select-layout", "-t", "$7:0", "even-horizontal"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{
					Layout: "even-horizontal",
				})
			},
		},
		{
			name:     "spread",
			wantArgs: []string{"select-layout", "-t", "$7:0", "-E"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{Spread: true})
			},
		},
		{
			name:     "next flag",
			wantArgs: []string{"select-layout", "-t", "$7:0", "-n"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{Next: true})
			},
		},
		{
			name:     "previous flag",
			wantArgs: []string{"select-layout", "-t", "$7:0", "-p"},
			invoke: func(window Window) error {
				return window.SelectLayout(context.Background(), SelectLayoutRequest{Previous: true})
			},
		},
		{
			name:     "next command",
			wantArgs: []string{"next-layout", "-t", "$7:0"},
			invoke: func(window Window) error {
				return window.NextLayout(context.Background())
			},
		},
		{
			name:     "previous command",
			wantArgs: []string{"previous-layout", "-t", "$7:0"},
			invoke: func(window Window) error {
				return window.PreviousLayout(context.Background())
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
				server:    serverWithRunner(runner),
				sessionID: "$7",
				windowID:  "@8",
			})
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("layout operation error = %v, want ErrCommand", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

func TestSelectLayoutRejectsMultipleModesBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request SelectLayoutRequest
	}{
		{
			name:    "layout and spread",
			request: SelectLayoutRequest{Layout: "tiled", Spread: true},
		},
		{
			name:    "next and previous",
			request: SelectLayoutRequest{Next: true, Previous: true},
		},
		{
			name:    "spread and next",
			request: SelectLayoutRequest{Spread: true, Next: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := (Window{
				server:    serverWithRunner(runner),
				sessionID: "$7",
				windowID:  "@8",
			}).SelectLayout(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestSelectLayoutUsesLiteralCommandBoundary(t *testing.T) {
	t.Parallel()

	// A layout carrying a command separator is refused before it is sent. No
	// tmux layout contains one, and a name tmux does not recognise kills the
	// server on 3.3a, so this is the guard rather than the escape it used to
	// exercise. The escape itself is covered where values legitimately carry a
	// semicolon: see TestLiteralArgumentsAndRawSeparatorsAgainstRealTmux and
	// TestTransportsAgreeOnLiteralValues.
	t.Run("trailing separator", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{}
		err := (Window{
			server:    serverWithRunner(runner),
			sessionID: "$7",
			windowID:  "@8",
		}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: "custom;"})
		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0", calls)
		}
	})

	t.Run("NUL", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{}
		err := (Window{
			server:    serverWithRunner(runner),
			sessionID: "$7",
			windowID:  "@8",
		}).SelectLayout(context.Background(), SelectLayoutRequest{Layout: "bad\x00layout"})
		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("SelectLayout() error = %v, want ErrInvalidServerCommandRequest", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0", calls)
		}
	})
}
