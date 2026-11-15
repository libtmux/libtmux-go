package tmux

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestRequireTopologyServerUsesFrozenSocketPath(t *testing.T) {
	t.Parallel()

	left := serverWithSocketSelection(t, t.TempDir(), ServerOptions{
		SocketPath: "relative.sock",
	})
	right := serverWithSocketSelection(t, t.TempDir(), ServerOptions{
		SocketPath: "relative.sock",
	})
	if err := requireTopologyServer(left, right); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("different effective paths error = %v, want ErrInvalidRequest", err)
	}

	root := t.TempDir()
	options := ServerOptions{
		SocketName:         "shared",
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
	}
	left = serverWithSocketSelection(t, filepath.Join(root, "left"), options)
	right = serverWithSocketSelection(t, filepath.Join(root, "right"), options)
	if err := requireTopologyServer(left, right); err != nil {
		t.Fatalf("same effective named path error = %v", err)
	}
}

func TestTopologyCommandsPreservePythonArgumentOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want []string
		run  func(Server) error
	}{
		{
			name: "window select pane",
			want: []string{"select-pane", "-t", "$1:@2", "-l", "-Z"},
			run: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SelectPane(
					context.Background(),
					WindowSelectPaneRequest{Direction: PaneSelectDirectionLast, KeepZoom: true},
				)
				return err
			},
		},
		{
			name: "pane select",
			want: []string{"select-pane", "-t", "$1:.%3", "-R", "-Z"},
			run: func(server Server) error {
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).Select(context.Background(), PaneSelectRequest{
					Direction: PaneSelectDirectionRight,
					KeepZoom:  true,
				})
				return err
			},
		},
		{
			name: "pane mark",
			want: []string{"select-pane", "-t", "$1:.%3", "-M"},
			run: func(server Server) error {
				_, err := exactTestPane(server).Select(
					context.Background(), PaneSelectRequest{Mark: PaneMarkClear},
				)
				return err
			},
		},
		{
			name: "pane input",
			want: []string{"select-pane", "-t", "$1:.%3", "-e"},
			run: func(server Server) error {
				_, err := exactTestPane(server).Select(
					context.Background(), PaneSelectRequest{Input: PaneInputEnable},
				)
				return err
			},
		},
		{
			name: "last pane",
			want: []string{"last-pane", "-t", "$1:@2", "-d"},
			run: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).LastPane(
					context.Background(),
					LastPaneRequest{Input: PaneInputDisable},
				)
				return err
			},
		},
		{
			name: "last pane keep zoom",
			want: []string{"last-pane", "-t", "$1:@2", "-Z"},
			run: func(server Server) error {
				_, err := exactTestWindow(server).LastPane(
					context.Background(), LastPaneRequest{KeepZoom: true},
				)
				return err
			},
		},
		{
			name: "rotate window",
			want: []string{"rotate-window", "-t", "$1:@2", "-D", "-Z"},
			run: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).Rotate(
					context.Background(),
					RotateWindowRequest{Direction: RotateWindowDown, KeepZoom: true},
				)
				return err
			},
		},
		{
			name: "swap window",
			want: []string{"swap-window", "-t", "$1:@2", "-d", "-s", "$4:@5"},
			run: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).Swap(
					context.Background(),
					SwapWindowRequest{Target: Window{
						server: server, sessionID: "$4", windowID: "@5",
					}, Detach: true},
				)
				return err
			},
		},
		{
			name: "move pane",
			want: []string{
				"move-pane", "-h", "-d", "-f", "-p40", "-b",
				"-s", "$1:.%3", "-t", "$4:.%6",
			},
			run: func(server Server) error {
				percentage := 40
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).Move(context.Background(), MovePaneRequest{
					TargetPane: Pane{
						server: server, sessionID: "$4", windowID: "@5", paneID: "%6",
					},
					Direction: PaneDirectionLeft, FullWindow: true, Percentage: &percentage,
				})
				return err
			},
		},
		{
			name: "join pane",
			want: []string{
				"join-pane", "-v", "-l12", "-s", "$1:.%3", "-t", "$4:@5",
			},
			run: func(server Server) error {
				size := 12
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).Join(context.Background(), JoinPaneRequest{
					TargetWindow: Window{server: server, sessionID: "$4", windowID: "@5"},
					Attach:       true,
					Size:         &size,
				})
				return err
			},
		},
		{
			name: "swap pane",
			want: []string{
				"swap-pane", "-t", "$1:.%3", "-d", "-Z", "-s", "$4:.%6",
			},
			run: func(server Server) error {
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).Swap(context.Background(), SwapPaneRequest{
					Target: Pane{
						server: server, sessionID: "$4", windowID: "@5", paneID: "%6",
					},
					Detach: true, KeepZoom: true,
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{ExitCode: 0}},
				{err: context.Canceled},
			}}
			err := test.run(serverWithRunner(runner))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want refresh context cancellation", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.want)
		})
	}
}

func TestTopologyValidationRunsBeforeExecution(t *testing.T) {
	t.Parallel()

	negative := -1
	overflow := 101
	tests := []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "window select missing choice",
			run: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SelectPane(
					context.Background(), WindowSelectPaneRequest{},
				)
				return err
			},
		},
		{
			name: "window select target and direction",
			run: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1", windowID: "@2"}).SelectPane(
					context.Background(), WindowSelectPaneRequest{
						Target:    Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"},
						Direction: PaneSelectDirectionLast,
					},
				)
				return err
			},
		},
		{
			name: "pane select invalid direction",
			run: func(server Server) error {
				_, err := exactTestPane(server).Select(context.Background(), PaneSelectRequest{
					Direction: PaneSelectDirection(99),
				})
				return err
			},
		},
		{
			name: "pane select mark with input",
			run: func(server Server) error {
				_, err := exactTestPane(server).Select(context.Background(), PaneSelectRequest{
					Mark: PaneMarkSet, Input: PaneInputDisable,
				})
				return err
			},
		},
		{
			name: "pane select input with keep zoom",
			run: func(server Server) error {
				_, err := exactTestPane(server).Select(context.Background(), PaneSelectRequest{
					Input: PaneInputDisable, KeepZoom: true,
				})
				return err
			},
		},
		{
			name: "pane select direction with input",
			run: func(server Server) error {
				_, err := exactTestPane(server).Select(context.Background(), PaneSelectRequest{
					Direction: PaneSelectDirectionLast, Input: PaneInputDisable,
				})
				return err
			},
		},
		{
			name: "last pane invalid input",
			run: func(server Server) error {
				_, err := exactTestWindow(server).LastPane(context.Background(), LastPaneRequest{
					Input: PaneInputMode(99),
				})
				return err
			},
		},
		{
			name: "last pane input with keep zoom",
			run: func(server Server) error {
				_, err := exactTestWindow(server).LastPane(context.Background(), LastPaneRequest{
					Input: PaneInputDisable, KeepZoom: true,
				})
				return err
			},
		},
		{
			name: "rotate invalid direction",
			run: func(server Server) error {
				_, err := exactTestWindow(server).Rotate(context.Background(), RotateWindowRequest{
					Direction: RotateWindowDirection(99),
				})
				return err
			},
		},
		{
			name: "move both destinations",
			run: func(server Server) error {
				_, err := exactTestPane(server).Move(context.Background(), MovePaneRequest{
					TargetPane:   Pane{server: server, sessionID: "$4", windowID: "@5", paneID: "%6"},
					TargetWindow: Window{server: server, sessionID: "$4", windowID: "@5"},
				})
				return err
			},
		},
		{
			name: "join negative size",
			run: func(server Server) error {
				_, err := exactTestPane(server).Join(context.Background(), JoinPaneRequest{
					TargetWindow: Window{server: server, sessionID: "$4", windowID: "@5"},
					Size:         &negative,
				})
				return err
			},
		},
		{
			name: "move percentage overflow",
			run: func(server Server) error {
				_, err := exactTestPane(server).Move(context.Background(), MovePaneRequest{
					TargetWindow: Window{server: server, sessionID: "$4", windowID: "@5"},
					Percentage:   &overflow,
				})
				return err
			},
		},
		{
			name: "swap pane missing choice",
			run: func(server Server) error {
				_, err := exactTestPane(server).Swap(context.Background(), SwapPaneRequest{})
				return err
			},
		},
		{
			name: "swap pane identical target",
			run: func(server Server) error {
				_, err := exactTestPane(server).Swap(context.Background(), SwapPaneRequest{
					Target: exactTestPane(server),
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.run(serverWithRunner(runner))
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("operation error = %v, want ErrInvalidRequest", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want validation before execution", runner.callCount())
			}
		})
	}
}

func TestBreakPaneRejectsNULBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	_, err := exactTestPane(serverWithRunner(runner)).BreakPane(
		context.Background(), BreakPaneRequest{Name: "visible\x00secret"},
	)
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("BreakPane() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("BreakPane() retained name in error: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want validation before version probe", runner.callCount())
	}
}

func exactTestWindow(server Server) Window {
	return Window{server: server, sessionID: "$1", windowID: "@2", windowIndex: 3}
}

func exactTestPane(server Server) Pane {
	return Pane{
		server: server, sessionID: "$1", windowID: "@2", windowIndex: 3,
		paneID: "%3", paneIndex: 1,
	}
}

func TestExactSnapshotRefreshPreservesWinlinkIndex(t *testing.T) {
	t.Parallel()

	snapshot := duplicateWinlinkSnapshot()
	window, err := exactWindowFromSnapshot(snapshot, Window{
		sessionID: "$2", windowID: "@7", windowIndex: 3,
	})
	if err != nil {
		t.Fatalf("exactWindowFromSnapshot() error = %v", err)
	}
	if window.windowIndex != 3 {
		t.Fatalf("exactWindowFromSnapshot() index = %d, want 3", window.windowIndex)
	}

	pane, err := exactPaneFromSnapshot(snapshot, Pane{
		sessionID: "$2", windowID: "@7", windowIndex: 3, paneID: "%11",
	})
	if err != nil {
		t.Fatalf("exactPaneFromSnapshot() error = %v", err)
	}
	if pane.windowIndex != 3 {
		t.Fatalf("exactPaneFromSnapshot() index = %d, want 3", pane.windowIndex)
	}
}

func TestCreatedWindowLookupRequiresOneView(t *testing.T) {
	t.Parallel()

	snapshot := duplicateWinlinkSnapshot()
	_, err := createdWindowFromSnapshot(snapshot, "$2", "@7")
	if !errors.Is(err, ErrSnapshotAmbiguous) {
		t.Fatalf("creation lookup error = %v, want ErrSnapshotAmbiguous", err)
	}
	_, err = createdWindowFromSnapshot(snapshot, "$2", "@8")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("creation lookup error = %v, want ErrNotFound", err)
	}
}

func duplicateWinlinkSnapshot() Snapshot {
	windows := []Window{
		{sessionID: "$2", windowID: "@7", windowIndex: 1},
		{sessionID: "$2", windowID: "@7", windowIndex: 3},
	}
	panes := []Pane{
		{sessionID: "$2", windowID: "@7", windowIndex: 1, paneID: "%11"},
		{sessionID: "$2", windowID: "@7", windowIndex: 3, paneID: "%11"},
	}
	return Snapshot{state: &snapshotState{
		listed:  listedEverything,
		windows: windows,
		panes:   panes,
		windowsByID: map[WindowID][]int{
			"@7": {0, 1},
		},
		windowsByWinlink: map[winlinkKey][]int{
			{sessionID: "$2", windowID: "@7", index: 1}: {0},
			{sessionID: "$2", windowID: "@7", index: 3}: {1},
		},
		panesByView: map[paneViewKey][]int{
			{winlinkKey: winlinkKey{sessionID: "$2", windowID: "@7", index: 1}, paneID: "%11"}: {0},
			{winlinkKey: winlinkKey{sessionID: "$2", windowID: "@7", index: 3}, paneID: "%11"}: {1},
		},
	}}
}

func TestBreakPaneUsesLiteral37Workaround(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stdout: []string{"@9"}, ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{err: context.Canceled},
	}}
	server := serverWithRunner(runner)
	window, err := (Pane{
		server: server, sessionID: "$1", windowID: "@2", windowIndex: 3,
		paneID: "%3",
	}).BreakPane(context.Background(), BreakPaneRequest{Name: "named;"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BreakPane() error = %v, want refresh context cancellation", err)
	}
	if window.SessionID() != "$1" || window.ID() != "@9" || window.Index() != -1 {
		t.Fatalf("BreakPane() partial window = %#v, want $1:@9 with index -1", window)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[1], []string{
		"break-pane", "-P", "-F#{window_id}", "-d", "-n", "named\\;",
		"-s", "$1:.%3", "-t", "$1:",
	})
	assertRequestArguments(t, requests[2], []string{
		"rename-window", "-t", "$1:@9", "--", "named\\;",
	})
	callsBefore := runner.callCount()
	if err := window.NextLayout(context.Background()); !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("partial NextLayout() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if calls := runner.callCount(); calls != callsBefore {
		t.Fatalf("partial NextLayout() runner calls = %d, want %d", calls, callsBefore)
	}
}

func TestBreakPaneLeavesPatchedReleaseNamingNative(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version string
		name    string
		want    []string
	}{
		{
			version: "3.7a",
			want: []string{
				"break-pane", "-P", "-F#{window_id}", "-d",
				"-s", "$1:.%3", "-t", "$1:",
			},
		},
		{
			version: "3.7b",
			name:    "named;",
			want: []string{
				"break-pane", "-P", "-F#{window_id}", "-d", "-n", "named\\;",
				"-s", "$1:.%3", "-t", "$1:",
			},
		},
	} {
		t.Run(test.version+test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}, ExitCode: 0}},
				{result: tmuxcmd.Result{Stdout: []string{"@9"}, ExitCode: 0}},
				{err: context.Canceled},
			}}
			_, err := exactTestPane(serverWithRunner(runner)).BreakPane(
				context.Background(), BreakPaneRequest{Name: test.name},
			)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("BreakPane() error = %v, want refresh context cancellation", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 3 {
				t.Fatalf("request count = %d, want version, break, and refresh", len(requests))
			}
			assertRequestArguments(t, requests[1], test.want)
		})
	}
}

func TestBreakPaneRefusesUnnamedExact37BeforeMutation(t *testing.T) {
	t.Parallel()
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7"}, ExitCode: 0}},
	}}
	_, err := exactTestPane(serverWithRunner(runner)).BreakPane(context.Background(), BreakPaneRequest{})
	if !errors.Is(err, ErrVersionTooLow) {
		t.Fatalf("unnamed exact3.7 break: %v", err)
	}
	if got := len(runner.recordedRequests()); got != 1 {
		t.Fatalf("unnamed refusal made %d requests, want version only", got)
	}
}

func TestBreakPaneRecoversPre36SolePaneIdentity(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.5"}, ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{err: context.Canceled},
	}}
	window, err := exactTestPane(serverWithRunner(runner)).BreakPane(
		context.Background(), BreakPaneRequest{},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BreakPane() error = %v, want refresh context cancellation", err)
	}
	if window.SessionID() != "$1" || window.ID() != "@2" || window.Index() != -1 {
		t.Fatalf("BreakPane() partial window = %#v, want relinked $1:@2 with index -1", window)
	}
	callsBefore := runner.callCount()
	if err := window.NextLayout(context.Background()); !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("partial NextLayout() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if calls := runner.callCount(); calls != callsBefore {
		t.Fatalf("partial NextLayout() runner calls = %d, want %d", calls, callsBefore)
	}
}

func TestBreakPaneTransportPartialMarksUnknownExactIndex(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		{
			result: tmuxcmd.Result{Stdout: []string{"@9"}, ExitCode: -1},
			err:    context.Canceled,
		},
	}}
	window, err := exactTestPane(serverWithRunner(runner)).BreakPane(
		context.Background(), BreakPaneRequest{},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BreakPane() error = %v, want context canceled", err)
	}
	if window.SessionID() != "$1" || window.ID() != "@9" || window.Index() != -1 {
		t.Fatalf("BreakPane() partial window = %#v, want $1:@9 with index -1", window)
	}

	callsBefore := runner.callCount()
	if err := window.NextLayout(context.Background()); !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("partial NextLayout() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if calls := runner.callCount(); calls != callsBefore {
		t.Fatalf("partial NextLayout() runner calls = %d, want %d", calls, callsBefore)
	}
}

func TestTopologyRejectsUnprovenSameDaemonBeforeExecution(t *testing.T) {
	t.Parallel()

	sourceRunner := &versionQueueRunner{}
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	source := serverWithOptionsAndRunner(ServerOptions{
		SocketName: "shared", ProcessEnvironment: []string{"TMUX_TMPDIR=" + sourceRoot},
	}, sourceRunner)
	target := serverWithOptionsAndRunner(ServerOptions{
		SocketName: "shared", ProcessEnvironment: []string{"TMUX_TMPDIR=" + targetRoot},
	}, &versionQueueRunner{})
	_, err := (Window{server: source, sessionID: "$1", windowID: "@2"}).Swap(
		context.Background(),
		SwapWindowRequest{Target: Window{server: target, sessionID: "$3", windowID: "@4"}},
	)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Window.Swap() error = %v, want ErrInvalidRequest", err)
	}
	if sourceRunner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want validation before execution", sourceRunner.callCount())
	}
}

func TestTopologyAcceptsSeparateHandlesWithSameExplicitSocketPath(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{ExitCode: 0}},
		{err: context.Canceled},
	}}
	source := serverWithOptionsAndRunner(
		ServerOptions{SocketPath: "/tmp/shared.sock"},
		runner,
	)
	target := serverWithOptionsAndRunner(
		ServerOptions{SocketPath: "/tmp/shared.sock"},
		&versionQueueRunner{},
	)
	_, err := (Window{server: source, sessionID: "$1", windowID: "@2"}).Swap(
		context.Background(),
		SwapWindowRequest{Target: Window{server: target, sessionID: "$3", windowID: "@4"}},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Window.Swap() error = %v, want refresh context cancellation", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"-S/tmp/shared.sock", "swap-window", "-t", "$1:@2", "-s", "$3:@4",
	})
}
