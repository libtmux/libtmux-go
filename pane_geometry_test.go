package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.exc.PaneError
// libtmux:parity libtmux.pane.Pane.resize
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:adjustment:73daa10a3099
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:adjustment_direction:e4a3795db9a4
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:height,width:a104fc5529e1
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:height:3fde596c5d4b
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:height:555e479dfb49
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:mouse:61c1f4bb05bb
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:trim_below:39d89b9f4f73
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:width:483866c13c9b
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:width:84d6a1e76504
// libtmux:parity libtmux.pane.Pane.resize#parameter-branch:zoom:629cd868ae3d
func TestResizePaneBuildsOneModeOfLiteralArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  ResizePaneRequest
		wantArgs []string
	}{
		{
			name:     "zero adjustment is unset",
			request:  ResizePaneRequest{Adjustment: 0},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7"},
		},
		{
			name: "up",
			request: ResizePaneRequest{
				Direction: PaneResizeDirectionUp, Adjustment: 2,
			},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-U", "2"},
		},
		{
			name: "down",
			request: ResizePaneRequest{
				Direction: PaneResizeDirectionDown, Adjustment: 2,
			},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-D", "2"},
		},
		{
			name: "left",
			request: ResizePaneRequest{
				Direction: PaneResizeDirectionLeft, Adjustment: 2,
			},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-L", "2"},
		},
		{
			name: "right",
			request: ResizePaneRequest{
				Direction: PaneResizeDirectionRight, Adjustment: 2,
			},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-R", "2"},
		},
		{
			name:     "height in cells",
			request:  ResizePaneRequest{Height: PaneCells(10)},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-y10"},
		},
		{
			name:     "width by percentage",
			request:  ResizePaneRequest{Width: PanePercent(25)},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-x25%"},
		},
		{
			name: "height then width",
			request: ResizePaneRequest{
				Height: PanePercent(40), Width: PaneCells(80),
			},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-y40%", "-x80"},
		},
		{
			name:     "zero cells are explicit",
			request:  ResizePaneRequest{Width: PaneCells(0)},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-x0"},
		},
		{
			name:     "zoom",
			request:  ResizePaneRequest{Zoom: true},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-Z"},
		},
		{
			name:     "mouse",
			request:  ResizePaneRequest{Mouse: true},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-M"},
		},
		{
			name:     "trim below",
			request:  ResizePaneRequest{TrimBelow: true},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-T"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 7,
			}}}}
			_, err := paneWithExactTestTarget(serverWithRunner(runner)).Resize(
				context.Background(), test.request,
			)
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("Resize() error = %v, want ErrCommand", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

// libtmux:parity libtmux.exc.PaneAdjustmentDirectionRequiresAdjustment
// libtmux:parity libtmux.exc.RequiresDigitOrPercentage
// libtmux:parity libtmux.exc.RequiresDigitOrPercentage.__init__
func TestResizePaneRejectsInvalidModesBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request ResizePaneRequest
	}{
		{
			name: "unsupported direction",
			request: ResizePaneRequest{
				Direction: PaneResizeDirection(99), Adjustment: 1,
			},
		},
		{
			name:    "direction without adjustment",
			request: ResizePaneRequest{Direction: PaneResizeDirectionUp},
		},
		{
			name:    "adjustment without direction",
			request: ResizePaneRequest{Adjustment: 1},
		},
		{
			name: "zero adjustment",
			request: ResizePaneRequest{
				Direction: PaneResizeDirectionUp,
			},
		},
		{
			name: "negative adjustment",
			request: ResizePaneRequest{
				Direction: PaneResizeDirectionUp, Adjustment: -1,
			},
		},
		{name: "negative cells", request: ResizePaneRequest{Height: PaneCells(-1)}},
		{name: "negative percentage", request: ResizePaneRequest{Width: PanePercent(-1)}},
		{name: "percentage above 100", request: ResizePaneRequest{Width: PanePercent(101)}},
		{
			name: "direction and dimensions",
			request: ResizePaneRequest{
				Direction:  PaneResizeDirectionUp,
				Adjustment: 1,
				Height:     PaneCells(1),
			},
		},
		{
			name:    "dimensions and zoom",
			request: ResizePaneRequest{Height: PaneCells(1), Zoom: true},
		},
		{
			name:    "zoom and mouse",
			request: ResizePaneRequest{Zoom: true, Mouse: true},
		},
		{
			name: "trim and another mode",
			request: ResizePaneRequest{
				Direction:  PaneResizeDirectionLeft,
				Adjustment: 1,
				TrimBelow:  true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			_, err := paneWithExactTestTarget(serverWithRunner(runner)).Resize(
				context.Background(), test.request,
			)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("Resize() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestResizePaneReturnsRefreshedPane(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}
	responses = append(responses, lifecycleLookupResponses(
		t,
		version,
		"list-panes",
		map[string]string{
			"session_id": "$7", "window_id": "@8", "window_index": "2",
			"pane_id": "%7", "pane_index": "1", "pane_height": "10", "pane_width": "80",
		},
	)...)
	runner := &versionQueueRunner{responses: responses}
	pane, err := (Pane{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%7",
	}).Resize(context.Background(), ResizePaneRequest{
		Height: PaneCells(10), Width: PaneCells(80),
	})
	if err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	gotHeight, _ := pane.Height()
	gotWidth, _ := pane.Width()
	if gotHeight != 10 || gotWidth != 80 {
		t.Fatalf("Resize() size = %dx%d, want 80x10", gotWidth, gotHeight)
	}
}

func TestResizePaneReturnsReceiverWhenRefreshFails(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 0}},
		{err: context.Canceled},
	}}
	receiver := Pane{
		server:      serverWithRunner(runner).WithStrictErrors(),
		sessionID:   "$7",
		windowID:    "@8",
		windowIndex: 2,
		paneID:      "%7",
		paneIndex:   1,
	}
	pane, err := receiver.Resize(context.Background(), ResizePaneRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resize() error = %v, want context canceled refresh error", err)
	}
	if pane.sessionID != receiver.sessionID || pane.windowID != receiver.windowID ||
		pane.windowIndex != receiver.windowIndex || pane.paneID != receiver.paneID ||
		pane.paneIndex != receiver.paneIndex || pane.Server() != receiver.Server() {
		t.Fatalf("Resize() partial result = %#v, want receiver %#v", pane, receiver)
	}
}

// libtmux:parity libtmux.pane.Pane.set_height
// libtmux:parity libtmux.pane.Pane.set_width
func TestPaneSetWidthAndHeightDelegateToCellResize(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		run      func(Pane) (Pane, error)
		wantArgs []string
	}{
		{
			name: "width",
			run: func(pane Pane) (Pane, error) {
				return pane.SetWidth(context.Background(), 80)
			},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-x80"},
		},
		{
			name: "height",
			run: func(pane Pane) (Pane, error) {
				return pane.SetHeight(context.Background(), 24)
			},
			wantArgs: []string{"resize-pane", "-t", "$5:0.%7", "-y24"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 7,
			}}}}
			_, err := test.run(paneWithExactTestTarget(serverWithRunner(runner)))
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("%s error = %v, want ErrCommand", test.name, err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

// libtmux:parity libtmux.pane.Pane.set_title
func TestSetTitleUsesLiteralTitleAndRefreshes(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}
	responses = append(responses, lifecycleLookupResponses(
		t,
		version,
		"list-panes",
		map[string]string{
			"session_id": "$7", "window_id": "@8", "window_index": "2",
			"pane_id": "%7", "pane_index": "1", "pane_title": "build;",
		},
	)...)
	runner := &versionQueueRunner{responses: responses}
	pane, err := paneWithExactTestTarget(serverWithRunner(runner)).SetTitle(
		context.Background(), "build;",
	)
	if err != nil {
		t.Fatalf("SetTitle() error = %v", err)
	}
	title, ok := pane.Title()
	if !ok || title != "build;" {
		t.Fatalf("SetTitle() title = %q, %t, want build;, true", title, ok)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(
		t,
		requests[0],
		[]string{"select-pane", "-t", "$5:0.%7", "-T", `build\;`},
	)
}

func TestSetTitleTreatsCompletedStderrAsCommandError(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"select failed"}, ExitCode: 1,
	}}}}
	_, err := paneWithExactTestTarget(serverWithRunner(runner)).SetTitle(
		context.Background(), "title",
	)
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("SetTitle() error = %v, want ErrCommand", err)
	}
	if calls := runner.callCount(); calls != 1 {
		t.Fatalf("runner calls = %d, want no refresh after command stderr", calls)
	}
}

func TestDocumentedPaneGeometryFailuresRedactPayloads(t *testing.T) {
	t.Parallel()

	const secret = "pane-geometry-secret"
	tests := []struct {
		name       string
		subcommand string
		invoke     func(Pane) error
	}{
		{name: "resize", subcommand: "resize-pane", invoke: func(pane Pane) error {
			_, err := pane.Resize(context.Background(), ResizePaneRequest{})
			return err
		}},
		{name: "title", subcommand: "select-pane", invoke: func(pane Pane) error {
			_, err := pane.SetTitle(context.Background(), secret)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Command:  []string{"tmux", test.subcommand, secret},
				Stdout:   []string{"stdout " + secret},
				Stderr:   []string{"stderr " + secret},
				ExitCode: 7,
			}}}}
			err := test.invoke(paneWithExactTestTarget(serverWithRunner(runner)))
			assertExitOnlyCommandErrorRedacts(t, err, test.subcommand, 7, secret)
		})
	}
}

func paneWithExactTestTarget(server Server) Pane {
	return Pane{server: server, sessionID: "$5", windowID: "@6", paneID: "%7"}
}

func TestPaneGeometryRejectsInvalidTargetBeforeExecution(t *testing.T) {
	t.Parallel()

	for _, run := range []func(Pane) error{
		func(pane Pane) error {
			_, err := pane.Resize(context.Background(), ResizePaneRequest{})
			return err
		},
		func(pane Pane) error {
			_, err := pane.SetTitle(context.Background(), "title")
			return err
		},
	} {
		runner := &versionQueueRunner{}
		err := run(Pane{
			server: serverWithRunner(runner), sessionID: "$1", windowID: "@2",
			paneID: "pane-name",
		})
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("geometry method error = %v, want ErrInvalidTarget", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0", calls)
		}
	}
}
