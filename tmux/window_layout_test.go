package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
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
			name:    "mirrored layout and spread",
			request: SelectLayoutRequest{Layout: "main-horizontal-mirrored", Spread: true},
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

	// Reject separators before tmux 3.3a can exit the server for an unknown layout.
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

func TestSelectLayoutRejectsMalformedTreesWithoutDispatch(t *testing.T) {
	for _, layout := range []string{
		"32d2,80x24,0,0{}",
		"4a17,80x24,0,0{39x24,0,0,0,40x24,40,0[]}",
		"12f1,80x24,0,0{39x24,0,0,0,40x24,40,0,1",
		"89d5,80x24,0,0{39x24,0,0,0,40x24,40,0,1]",
		"ffff,80x24,0,0,0",
	} {
		t.Run(layout, func(t *testing.T) {
			if _, err := selectLayoutArguments("$7:0", SelectLayoutRequest{Layout: layout}, Version{}); !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("unsafe custom layout rendered: %v", err)
			}
		})
	}
}

func TestSelectLayoutRequestValidateWithoutTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, request := range []SelectLayoutRequest{
		{},
		{Layout: "tiled"},
		{Layout: "main-horizontal-mirrored"},
		{Layout: "b25d,80x24,0,0,0"},
		{Next: true},
		{Previous: true},
		{Spread: true},
	} {
		if err := request.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", request, err)
		}
	}
	for _, request := range []SelectLayoutRequest{
		{Layout: "tiled\x00"},
		{Layout: "no-such-layout"},
		{Layout: "32d2,80x24,0,0{}"},
		{Layout: "ffff,80x24,0,0,0"},
		{Layout: "tiled", Next: true},
		{Next: true, Previous: true},
	} {
		if err := request.Validate(); !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Errorf("Validate(%+v) = %v, want invalid request", request, err)
		}
	}
}
