package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.constants.RESIZE_ADJUSTMENT_DIRECTION_FLAG_MAP
// libtmux:parity libtmux.constants.ResizeAdjustmentDirection
// libtmux:parity libtmux.constants.ResizeAdjustmentDirection.Down
// libtmux:parity libtmux.constants.ResizeAdjustmentDirection.Left
// libtmux:parity libtmux.constants.ResizeAdjustmentDirection.Right
// libtmux:parity libtmux.constants.ResizeAdjustmentDirection.Up
// libtmux:parity libtmux.exc.WindowError
// libtmux:parity libtmux.window.Window.resize
// libtmux:parity libtmux.window.Window.resize#parameter-branch:adjustment:73daa10a3099
// libtmux:parity libtmux.window.Window.resize#parameter-branch:adjustment_direction:e4a3795db9a4
// libtmux:parity libtmux.window.Window.resize#parameter-branch:expand,shrink:d624172d273f
// libtmux:parity libtmux.window.Window.resize#parameter-branch:expand:163401d681e1
// libtmux:parity libtmux.window.Window.resize#parameter-branch:height,width:a104fc5529e1
// libtmux:parity libtmux.window.Window.resize#parameter-branch:height:3fde596c5d4b
// libtmux:parity libtmux.window.Window.resize#parameter-branch:shrink:fd4b5f4dd805
// libtmux:parity libtmux.window.Window.resize#parameter-branch:width:84d6a1e76504
func TestResizeWindowBuildsOneModeOfLiteralArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  ResizeWindowRequest
		wantArgs []string
	}{
		{
			name:     "zero numeric values are unset",
			request:  ResizeWindowRequest{Adjustment: 0, Height: 0, Width: 0},
			wantArgs: []string{"resize-window", "-t", "$7:0"},
		},
		{
			name: "up",
			request: ResizeWindowRequest{
				Direction: WindowResizeDirectionUp, Adjustment: 2,
			},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-U", "2"},
		},
		{
			name: "down",
			request: ResizeWindowRequest{
				Direction: WindowResizeDirectionDown, Adjustment: 2,
			},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-D", "2"},
		},
		{
			name: "left",
			request: ResizeWindowRequest{
				Direction: WindowResizeDirectionLeft, Adjustment: 2,
			},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-L", "2"},
		},
		{
			name: "right",
			request: ResizeWindowRequest{
				Direction: WindowResizeDirectionRight, Adjustment: 2,
			},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-R", "2"},
		},
		{
			name:     "height",
			request:  ResizeWindowRequest{Height: 10},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-y10"},
		},
		{
			name:     "width",
			request:  ResizeWindowRequest{Width: 20},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-x20"},
		},
		{
			name:     "height and width",
			request:  ResizeWindowRequest{Height: 10, Width: 20},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-y10", "-x20"},
		},
		{
			name:     "expand",
			request:  ResizeWindowRequest{Expand: true},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-A"},
		},
		{
			name:     "shrink",
			request:  ResizeWindowRequest{Shrink: true},
			wantArgs: []string{"resize-window", "-t", "$7:0", "-a"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 7,
			}}}}
			_, err := (Window{
				server:    serverWithRunner(runner),
				sessionID: "$7",
				windowID:  "@8",
			}).Resize(context.Background(), test.request)
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("Resize() error = %v, want ErrCommand", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

// libtmux:parity libtmux.exc.AdjustmentDirectionRequiresAdjustment
// libtmux:parity libtmux.exc.AdjustmentDirectionRequiresAdjustment.__init__
// libtmux:parity libtmux.exc.WindowAdjustmentDirectionRequiresAdjustment
func TestResizeWindowRejectsInvalidModesBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request ResizeWindowRequest
	}{
		{
			name:    "unsupported direction",
			request: ResizeWindowRequest{Direction: WindowResizeDirection(99), Adjustment: 1},
		},
		{
			name:    "direction without adjustment",
			request: ResizeWindowRequest{Direction: WindowResizeDirectionUp},
		},
		{
			name:    "adjustment without direction",
			request: ResizeWindowRequest{Adjustment: 1},
		},
		{
			name: "zero adjustment",
			request: ResizeWindowRequest{
				Direction: WindowResizeDirectionUp,
			},
		},
		{
			name: "negative adjustment",
			request: ResizeWindowRequest{
				Direction: WindowResizeDirectionUp, Adjustment: -1,
			},
		},
		{name: "negative width", request: ResizeWindowRequest{Width: -1}},
		{
			name: "direction and dimensions",
			request: ResizeWindowRequest{
				Direction: WindowResizeDirectionUp, Adjustment: 1, Height: 1,
			},
		},
		{
			name:    "dimensions and expand",
			request: ResizeWindowRequest{Height: 1, Expand: true},
		},
		{
			name:    "expand and shrink",
			request: ResizeWindowRequest{Expand: true, Shrink: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			_, err := (Window{
				server:    serverWithRunner(runner),
				sessionID: "$7",
				windowID:  "@8",
			}).Resize(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("Resize() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestResizeReturnsRefreshedWindow(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}
	responses = append(responses, lifecycleLookupResponses(
		t,
		version,
		"list-windows",
		map[string]string{
			"session_id": "$7", "window_id": "@8", "window_index": "2",
			"window_height": "10", "window_width": "20",
		},
	)...)
	runner := &versionQueueRunner{responses: responses}
	window, err := (Window{
		server:    serverWithRunner(runner),
		sessionID: "$7",
		windowID:  "@8",
	}).Resize(context.Background(), ResizeWindowRequest{Height: 10, Width: 20})
	if err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	gotHeight, _ := window.Height()
	gotWidth, _ := window.Width()
	if gotHeight != 10 || gotWidth != 20 {
		t.Fatalf("Resize() size = %dx%d, want 20x10", gotWidth, gotHeight)
	}
}

func TestResizeReturnsReceiverWhenRefreshFails(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 0}},
		{err: context.Canceled},
	}}
	receiver := Window{
		server:      serverWithRunner(runner),
		sessionID:   "$7",
		windowID:    "@8",
		windowIndex: 2,
	}
	window, err := receiver.Resize(context.Background(), ResizeWindowRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resize() error = %v, want context canceled refresh error", err)
	}
	if window.sessionID != receiver.sessionID || window.windowID != receiver.windowID ||
		window.windowIndex != receiver.windowIndex || window.Server() != receiver.Server() {
		t.Fatalf("Resize() partial result = %#v, want receiver %#v", window, receiver)
	}
}
