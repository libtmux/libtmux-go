package tmux

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestAuxiliaryCreationFailuresRedactPayloads(t *testing.T) {
	t.Parallel()

	const secret = "auxiliary-creation-secret"
	failure := versionResponse{result: tmuxcmd.Result{
		Command:  []string{"tmux", "auxiliary", secret},
		Stdout:   []string{"stdout " + secret},
		Stderr:   []string{"stderr " + secret},
		ExitCode: 7,
	}}
	tests := []struct {
		name       string
		subcommand string
		responses  []versionResponse
		invoke     func(Server) error
	}{
		{
			name:       "session pre-kill",
			subcommand: "kill-session",
			responses: []versionResponse{
				{result: tmuxcmd.Result{ExitCode: 0}},
				failure,
			},
			invoke: func(server Server) error {
				_, err := server.NewSession(context.Background(), NewSessionRequest{
					Name: secret, KillExisting: true,
				})
				return err
			},
		},
		{
			name:       "window name expansion",
			subcommand: "display-message",
			responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
				failure,
			},
			invoke: func(server Server) error {
				name := secret
				_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
					context.Background(),
					NewWindowRequest{Name: &name, SelectExisting: true},
				)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.invoke(serverWithRunner(&versionQueueRunner{responses: test.responses}))
			assertExitOnlyCommandErrorRedacts(t, err, test.subcommand, 7, secret)
		})
	}
}

func TestNewSessionCopiesEnvironmentBeforeExistenceProbe(t *testing.T) {
	t.Parallel()

	width, height := 101, 31
	environment := map[string]string{"KEY": "before"}
	runner := newLifecycleProbeGateRunner(
		versionResponse{result: tmuxcmd.Result{ExitCode: 1}},
		versionResponse{result: tmuxcmd.Result{Stderr: []string{"stop"}, ExitCode: 7}},
	)
	done := make(chan error, 1)
	go func() {
		_, err := serverWithRunner(runner).NewSession(context.Background(), NewSessionRequest{
			Name: "owned", Width: width, Height: height, Environment: environment,
		})
		done <- err
	}()

	<-runner.probeStarted
	environment["KEY"] = "after"
	close(runner.releaseProbe)
	if err := <-done; !errors.Is(err, ErrCommand) {
		t.Fatalf("NewSession() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"new-session", "-P", "-F#{session_id}", "-sowned", "-d",
		"-x", "101", "-y", "31", "-eKEY=before",
	})
}

func TestNewWindowCapturesNameBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	name := "before-name"
	environment := map[string]string{"KEY": "before"}
	runner := newLifecycleProbeGateRunner(
		versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		versionResponse{result: tmuxcmd.Result{Stdout: []string{"before-name"}, ExitCode: 0}},
		versionResponse{result: tmuxcmd.Result{Stdout: []string{"before-name"}, ExitCode: 0}},
		versionResponse{result: tmuxcmd.Result{Stderr: []string{"stop"}, ExitCode: 7}},
	)
	done := make(chan error, 1)
	go func() {
		_, err := (Session{
			server: serverWithRunner(runner), sessionID: "$7",
		}).NewWindow(context.Background(), NewWindowRequest{
			Name: &name, Environment: environment, SelectExisting: true,
		})
		done <- err
	}()

	<-runner.probeStarted
	name = "after-name"
	environment["KEY"] = "after"
	close(runner.releaseProbe)
	if err := <-done; !errors.Is(err, ErrCommand) {
		t.Fatalf("NewWindow() error = %v, want ErrCommand", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[1], []string{
		"display-message", "-p", "-t", "$7", "-F", "before-name",
	})
	assertRequestArguments(t, requests[2], []string{
		"display-message", "-p", "-t", "$7", "-F", "before-name",
	})
	assertRequestArguments(t, requests[3], []string{
		"new-window", "-t", "$7", "-d", "-P", "-F#{window_id}",
		"-n", "before-name", "-eKEY=before", "-S",
	})
}

func TestNewWindowCapturesIndexBeforeExecution(t *testing.T) {
	t.Parallel()

	index := 4
	runner := newLifecycleProbeGateRunner(
		versionResponse{result: tmuxcmd.Result{Stderr: []string{"stop"}, ExitCode: 7}},
	)
	done := make(chan error, 1)
	go func() {
		_, err := (Session{
			server: serverWithRunner(runner), sessionID: "$7",
		}).NewWindow(context.Background(), NewWindowRequest{Index: &index})
		done <- err
	}()

	<-runner.probeStarted
	index = 8
	close(runner.releaseProbe)
	if err := <-done; !errors.Is(err, ErrCommand) {
		t.Fatalf("NewWindow() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"new-window", "-t", "$7:4", "-d", "-P", "-F#{window_id}",
	})
}

func TestSplitPaneCapturesPointersBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	size := 10
	style := "before-style"
	active := "before-active"
	inactive := "before-inactive"
	message := "before-message"
	environment := map[string]string{"KEY": "before"}
	runner := newLifecycleProbeGateRunner(
		versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		versionResponse{result: tmuxcmd.Result{Stderr: []string{"stop"}, ExitCode: 7}},
	)
	done := make(chan error, 1)
	go func() {
		_, err := (Window{
			server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
		}).SplitPane(context.Background(), SplitPaneRequest{
			Size: &size, Style: &style, ActiveBorderStyle: &active,
			InactiveBorderStyle: &inactive, Message: &message, Environment: environment,
		})
		done <- err
	}()

	<-runner.probeStarted
	size = 20
	style = "after-style"
	active = "after-active"
	inactive = "after-inactive"
	message = "after-message"
	environment["KEY"] = "after"
	close(runner.releaseProbe)
	if err := <-done; !errors.Is(err, ErrCommand) {
		t.Fatalf("SplitPane() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"split-window", "-t", "$7:0", "-v", "-l10", "-P", "-F#{pane_id}",
		"-d", "-eKEY=before", "-s", "before-style", "-S", "before-active",
		"-R", "before-inactive", "-m", "before-message",
	})
}

func TestSplitPaneCapturesPercentageBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	percentage := 35
	runner := newLifecycleProbeGateRunner(
		versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		versionResponse{result: tmuxcmd.Result{Stderr: []string{"stop"}, ExitCode: 7}},
	)
	done := make(chan error, 1)
	go func() {
		_, err := (Window{
			server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
		}).SplitPane(context.Background(), SplitPaneRequest{
			Percentage: &percentage, Empty: true,
		})
		done <- err
	}()

	<-runner.probeStarted
	percentage = 70
	close(runner.releaseProbe)
	if err := <-done; !errors.Is(err, ErrCommand) {
		t.Fatalf("SplitPane() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"split-window", "-t", "$7:0", "-v", "-l35%", "-P", "-F#{pane_id}",
		"-d", "-E",
	})
}

func TestNewPaneCapturesPointersBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	width, height, x, y := 40, 10, -2, 3
	style := "before-style"
	active := "before-active"
	inactive := "before-inactive"
	message := "before-message"
	environment := map[string]string{"KEY": "before"}
	runner := newLifecycleProbeGateRunner(
		versionResponse{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		versionResponse{result: tmuxcmd.Result{Stderr: []string{"stop"}, ExitCode: 7}},
	)
	done := make(chan error, 1)
	go func() {
		_, err := (Window{
			server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
		}).NewPane(context.Background(), NewPaneRequest{
			Width: &width, Height: &height, X: &x, Y: &y,
			Style: &style, ActiveBorderStyle: &active,
			InactiveBorderStyle: &inactive, Message: &message, Environment: environment,
		})
		done <- err
	}()

	<-runner.probeStarted
	width = 80
	height = 20
	x = -4
	y = 6
	style = "after-style"
	active = "after-active"
	inactive = "after-inactive"
	message = "after-message"
	environment["KEY"] = "after"
	close(runner.releaseProbe)
	if err := <-done; !errors.Is(err, ErrCommand) {
		t.Fatalf("NewPane() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"new-pane", "-t", "$7:0", "-x40", "-y10", "-X-2", "-Y3",
		"-s", "before-style", "-S", "before-active", "-R", "before-inactive",
		"-m", "before-message", "-P", "-F#{pane_id}", "-d", "-eKEY=before",
	})
}

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
// libtmux:parity libtmux.constants.WINDOW_DIRECTION_FLAG_MAP
// libtmux:parity libtmux.constants.WindowDirection
// libtmux:parity libtmux.constants.WindowDirection.After
// libtmux:parity libtmux.constants.WindowDirection.Before
func TestNewWindowPreservesOptionalNameAndPythonOptionOrder(t *testing.T) {
	t.Parallel()

	empty := ""
	for _, test := range []struct {
		name     string
		value    *string
		wantName []string
	}{
		{name: "omitted", value: nil},
		{name: "explicit empty", value: &empty, wantName: []string{"-n", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7},
			}}}
			server := serverWithRunner(runner)
			_, err := (Session{server: server, sessionID: "$1"}).NewWindow(
				context.Background(),
				NewWindowRequest{
					Name:           test.value,
					StartDirectory: "/work",
					Environment: map[string]string{
						"ZED":   "last",
						"ALPHA": "first",
					},
					Direction:    NewWindowDirectionAfter,
					KillExisting: true,
					Command:      "sleep 1m",
				},
			)
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("NewWindow() error = %v, want ErrCommand", err)
			}
			want := []string{
				"new-window", "-t", "$1", "-d", "-P", "-c/work", "-F#{window_id}",
			}
			want = append(want, test.wantName...)
			want = append(want,
				"-eALPHA=first", "-eZED=last", "-a", "-k", "sleep 1m",
			)
			assertLifecycleArguments(t, runner, want)
		})
	}
}

// libtmux:parity libtmux.window.Window.new_window
func TestWindowNewWindowUsesExactLinkedTargetAndRejectsIndex(t *testing.T) {
	t.Parallel()

	t.Run("exact receiver", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7},
		}}}
		_, err := (Window{
			server: serverWithRunner(runner), sessionID: "$9", windowID: "@3",
		}).NewWindow(context.Background(), NewWindowRequest{Direction: NewWindowDirectionBefore})
		if !errors.Is(err, ErrCommand) {
			t.Fatalf("Window.NewWindow() error = %v, want ErrCommand", err)
		}
		assertLifecycleArguments(t, runner, []string{
			"new-window", "-t", "$9:0", "-d", "-P", "-F#{window_id}", "-b",
		})
	})

	t.Run("index conflicts with receiver", func(t *testing.T) {
		t.Parallel()

		index := 4
		runner := &versionQueueRunner{}
		_, err := (Window{
			server: serverWithRunner(runner), sessionID: "$9", windowID: "@3",
		}).NewWindow(context.Background(), NewWindowRequest{Index: &index})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Window.NewWindow() error = %v, want ErrInvalidRequest", err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("runner calls = %d, want 0", calls)
		}
	})
}

func TestNewWindowSelectExistingDoesNotRecoverForExplicitPlacement(t *testing.T) {
	t.Parallel()

	name := "existing"
	index := 4
	for _, test := range []struct {
		name   string
		invoke func(Server) (Window, error)
		want   []string
	}{
		{
			name: "indexed session target",
			invoke: func(server Server) (Window, error) {
				return (Session{server: server, sessionID: "$9"}).NewWindow(
					context.Background(),
					NewWindowRequest{Name: &name, Index: &index, SelectExisting: true},
				)
			},
			want: []string{
				"new-window", "-t", "$9:4", "-d", "-P", "-F#{window_id}",
				"-n", name, "-S",
			},
		},
		{
			name: "exact winlink target",
			invoke: func(server Server) (Window, error) {
				return (Window{
					server: server, sessionID: "$9", windowID: "@3",
				}).NewWindow(context.Background(), NewWindowRequest{
					Name: &name, Direction: NewWindowDirectionAfter, SelectExisting: true,
				})
			},
			want: []string{
				"new-window", "-t", "$9:0", "-d", "-P", "-F#{window_id}",
				"-n", name, "-a", "-S",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7},
			}}}
			_, err := test.invoke(serverWithRunner(runner))
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("NewWindow() error = %v, want ErrCommand", err)
			}
			if calls := runner.callCount(); calls != 1 {
				t.Fatalf("runner calls = %d, want command only without recovery probes", calls)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.want)
		})
	}
}

func TestNewWindowSelectExistingResolvesNoOutputInExactSession(t *testing.T) {
	t.Parallel()

	name := "existing"
	version := mustParseVersion(t, "3.7b")
	windowFields, err := formatFieldsFor("list-windows", version)
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stdout: []string{name}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stdout: []string{name}, ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		liveIdentityResponse(version),
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(windowFields, snapshotRowValues(version, map[string]string{
				"session_id": "$9", "window_id": "@4", "window_index": "2", "window_name": name,
			})),
			ExitCode: 0,
		}},
		liveIdentityResponse(version),
	}}
	server := serverWithRunner(runner)

	window, err := (Session{server: server, sessionID: "$9"}).NewWindow(
		context.Background(),
		NewWindowRequest{Name: &name, SelectExisting: true},
	)
	if err != nil {
		t.Fatalf("NewWindow(SelectExisting) error = %v", err)
	}
	if window.sessionID != "$9" || window.windowID != "@4" || window.windowIndex != 2 {
		t.Fatalf("NewWindow(SelectExisting) = %#v, want exact $9:@4 index 2", window)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[1], []string{
		"display-message", "-p", "-t", "$9", "-F", name,
	})
	assertRequestArguments(t, requests[2], []string{
		"display-message", "-p", "-t", "$9", "-F", name,
	})
	assertRequestArguments(t, requests[3], []string{
		"new-window", "-t", "$9", "-d", "-P", "-F#{window_id}", "-n", name, "-S",
	})
}

func TestCreationTransportPartialsPreserveReceiverContext(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		stdout string
		invoke func(Server) (SessionID, WindowID, error)
	}{
		{
			name:   "window",
			stdout: "@8",
			invoke: func(server Server) (SessionID, WindowID, error) {
				window, err := (Session{server: server, sessionID: "$7"}).NewWindow(
					context.Background(), NewWindowRequest{},
				)
				return window.sessionID, window.windowID, err
			},
		},
		{
			name:   "pane",
			stdout: "%9",
			invoke: func(server Server) (SessionID, WindowID, error) {
				pane, err := (Window{
					server: server, sessionID: "$7", windowID: "@8",
				}).SplitPane(context.Background(), SplitPaneRequest{})
				return pane.sessionID, pane.windowID, err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: []string{test.stdout}, ExitCode: -1},
				err:    context.Canceled,
			}}}
			sessionID, windowID, err := test.invoke(serverWithRunner(runner))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("create error = %v, want context canceled", err)
			}
			if sessionID != "$7" || windowID != "@8" {
				t.Fatalf("partial parent = %s:%s, want $7:@8", sessionID, windowID)
			}
		})
	}
}

func TestNewWindowPartialsMarkUnknownExactIndex(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		responses []versionResponse
	}{
		{
			name: "transport failure",
			responses: []versionResponse{{
				result: tmuxcmd.Result{Stdout: []string{"@8"}, ExitCode: -1},
				err:    context.Canceled,
			}},
		},
		{
			name: "refresh failure",
			responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"@8"}, ExitCode: 0}},
				{err: context.Canceled},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: test.responses}
			window, err := (Session{
				server: serverWithRunner(runner), sessionID: "$7",
			}).NewWindow(context.Background(), NewWindowRequest{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("NewWindow() error = %v, want context canceled", err)
			}
			if window.SessionID() != "$7" || window.ID() != "@8" || window.Index() != -1 {
				t.Fatalf("NewWindow() partial = %#v, want $7:@8 with index -1", window)
			}

			callsBefore := runner.callCount()
			err = window.NextLayout(context.Background())
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("partial NextLayout() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != callsBefore {
				t.Fatalf("partial NextLayout() runner calls = %d, want %d", calls, callsBefore)
			}
		})
	}
}

func TestNewWindowPartialRawCmdUsesStableID(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{
			result: tmuxcmd.Result{Stdout: []string{"@8"}, ExitCode: -1},
			err:    context.Canceled,
		},
		{result: tmuxcmd.Result{ExitCode: 0}},
	}}
	window, err := (Session{
		server: serverWithRunner(runner), sessionID: "$7",
	}).NewWindow(context.Background(), NewWindowRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewWindow() error = %v, want context canceled", err)
	}
	if window.SessionID() != "$7" || window.ID() != "@8" || window.Index() != -1 {
		t.Fatalf("NewWindow() partial = %#v, want $7:@8 with index -1", window)
	}

	_, err = window.Cmd(context.Background(), "rename-window", "kept")
	if err != nil {
		t.Fatalf("partial Window.Cmd() error = %v", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"rename-window", "-t", "@8", "kept",
	})
}

func TestNewWindowRefreshesWithinReceiverSession(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7b")
	sessionFields, err := formatFieldsFor("list-sessions", version)
	if err != nil {
		t.Fatal(err)
	}
	windowFields, err := formatFieldsFor("list-windows", version)
	if err != nil {
		t.Fatal(err)
	}
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"@8"}, ExitCode: 0}},
		liveIdentityResponse(version),
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(sessionFields, snapshotRowValues(version, map[string]string{
				"session_id": "$7", "session_name": "receiver",
			})),
			ExitCode: 0,
		}},
		{result: tmuxcmd.Result{
			RawStdout: framedSnapshotRecord(windowFields, snapshotRowValues(version, map[string]string{
				"session_id": "$7", "window_id": "@8", "window_index": "2",
			})),
			ExitCode: 0,
		}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		{result: tmuxcmd.Result{ExitCode: 0}},
		liveIdentityResponse(version),
	}}

	window, err := (Session{
		server: serverWithRunner(runner), sessionID: "$7",
	}).NewWindow(context.Background(), NewWindowRequest{})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if window.sessionID != "$7" || window.windowID != "@8" || window.windowIndex != 2 {
		t.Fatalf("NewWindow() = %#v, want exact $7:@8 index 2", window)
	}
}

// libtmux:parity libtmux.constants.PANE_DIRECTION_FLAG_MAP
// libtmux:parity libtmux.constants.PaneDirection
// libtmux:parity libtmux.constants.PaneDirection.Above
// libtmux:parity libtmux.constants.PaneDirection.Below
// libtmux:parity libtmux.constants.PaneDirection.Left
// libtmux:parity libtmux.constants.PaneDirection.Right
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
// libtmux:parity libtmux.window.Window.split
func TestSplitPaneBuildsExtendedPythonOptionOrder(t *testing.T) {
	t.Parallel()

	size := 10
	style := "bg=blue"
	active := "fg=green"
	inactive := "fg=grey"
	message := "done"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7}},
	}}
	_, err := (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
	}).SplitPane(context.Background(), SplitPaneRequest{
		Direction:           PaneDirectionLeft,
		Size:                &size,
		FullWindow:          true,
		Zoom:                true,
		StartDirectory:      "/work",
		Environment:         map[string]string{"ZED": "last", "ALPHA": "first"},
		Empty:               true,
		Style:               &style,
		ActiveBorderStyle:   &active,
		InactiveBorderStyle: &inactive,
		Message:             &message,
		Keep:                true,
	})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("SplitPane() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"split-window", "-t", "$7:0", "-h", "-b", "-l10", "-f", "-Z",
		"-P", "-F#{pane_id}", "-c/work", "-d", "-eALPHA=first", "-eZED=last",
		"-E", "-s", style, "-S", active, "-R", inactive, "-m", message, "-k",
	})
}

func TestPaneSplitUsesExactPaneTarget(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7},
	}}}
	_, err := (Pane{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
	}).Split(context.Background(), SplitPaneRequest{Direction: PaneDirectionRight})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("Pane.Split() error = %v, want ErrCommand", err)
	}
	assertLifecycleArguments(t, runner, []string{
		"split-window", "-t", "$7:0.%9", "-h", "-P", "-F#{pane_id}", "-d",
	})
}

// libtmux:parity libtmux.pane.Pane.split#version-branch:tmux-version:c6a18af85027
// libtmux:parity libtmux.pane.Pane.split#version-branch:tmux-version:c6a18af85027:2
// libtmux:parity libtmux.pane.Pane.split#warning:39e08b1dd04b
// libtmux:parity libtmux.pane.Pane.split#warning:c85dca20c257
func TestSplitPaneWarnsAndOmitsTmux37FieldsOnOlderTmux(t *testing.T) {
	t.Parallel()

	style := "bg=blue"
	warnings := make([]Warning, 0, 2)
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7}},
	}}
	server := serverWithRunner(runner)
	server.connectionState().options.WarningHandler = func(warning Warning) {
		warnings = append(warnings, warning)
	}
	_, err := (Window{
		server: server, sessionID: "$7", windowID: "@8",
	}).SplitPane(context.Background(), SplitPaneRequest{
		Empty: true, Style: &style, Keep: true, Command: "sleep 1m",
	})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("SplitPane() error = %v, want ErrCommand", err)
	}
	if len(warnings) != 2 || warnings[0].Feature != "empty" ||
		warnings[1].Feature != "style/border/message/keep" {
		t.Fatalf("SplitPane() warnings = %#v, want empty then styling group", warnings)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"split-window", "-t", "$7:0", "-v", "-P", "-F#{pane_id}", "-d", "sleep 1m",
	})
}

func TestSplitPaneRejectsSupportedEmptyWithCommandBeforeMutation(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0},
	}}}
	_, err := (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
	}).SplitPane(context.Background(), SplitPaneRequest{Empty: true, Command: "sleep 1m"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SplitPane() error = %v, want ErrInvalidRequest", err)
	}
	if calls := runner.callCount(); calls != 1 {
		t.Fatalf("runner calls = %d, want version query before compatibility validation", calls)
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
// libtmux:parity libtmux.window.Window.new_pane
func TestNewPaneBuildsPythonOptionOrderAndExactTarget(t *testing.T) {
	t.Parallel()

	width, height, x, y := 40, 10, -2, 3
	style := "bg=blue"
	active := "fg=green"
	inactive := "fg=grey"
	message := "done"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}, ExitCode: 0}},
		{result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7}},
	}}
	_, err := (Pane{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
	}).NewPane(context.Background(), NewPaneRequest{
		Width:               &width,
		Height:              &height,
		X:                   &x,
		Y:                   &y,
		Zoom:                true,
		Style:               &style,
		ActiveBorderStyle:   &active,
		InactiveBorderStyle: &inactive,
		Message:             &message,
		Keep:                true,
		StartDirectory:      "/work",
		Environment:         map[string]string{"ZED": "last", "ALPHA": "first"},
		Empty:               true,
	})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("Pane.NewPane() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"new-pane", "-t", "$7:0.%9", "-x40", "-y10", "-X-2", "-Y3", "-Z",
		"-s", style, "-S", active, "-R", inactive, "-m", message, "-k",
		"-P", "-F#{pane_id}", "-c/work", "-d", "-eALPHA=first", "-eZED=last", "-E",
	})
}

// libtmux:parity libtmux.pane.Pane.new_pane#version-branch:tmux-version:9dfb8df17e6f
func TestWindowNewPaneRequiresTmux37BeforeMutation(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}, ExitCode: 0},
	}}}
	_, err := (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
	}).NewPane(context.Background(), NewPaneRequest{})
	if !errors.Is(err, ErrVersionTooLow) {
		t.Fatalf("Window.NewPane() error = %v, want ErrVersionTooLow", err)
	}
	if calls := runner.callCount(); calls != 1 {
		t.Fatalf("runner calls = %d, want version query only", calls)
	}
}

func TestNewPaneRejectsEmptyWithCommandBeforeExecution(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	_, err := (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
	}).NewPane(context.Background(), NewPaneRequest{Empty: true, Command: "sleep 1m"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Window.NewPane() error = %v, want ErrInvalidRequest", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("runner calls = %d, want 0", calls)
	}
}

type lifecycleProbeGateRunner struct {
	mu            sync.Mutex
	probeStarted  chan struct{}
	releaseProbe  chan struct{}
	responses     []versionResponse
	requests      []tmuxcmd.Request
	probeStartOne sync.Once
}

func newLifecycleProbeGateRunner(responses ...versionResponse) *lifecycleProbeGateRunner {
	return &lifecycleProbeGateRunner{
		probeStarted: make(chan struct{}),
		releaseProbe: make(chan struct{}),
		responses:    responses,
	}
}

func (r *lifecycleProbeGateRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	if len(r.responses) == 0 {
		r.mu.Unlock()
		return tmuxcmd.Result{}, errors.New("unexpected lifecycle command")
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	first := len(r.requests) == 1
	r.mu.Unlock()
	if first {
		r.probeStartOne.Do(func() { close(r.probeStarted) })
		<-r.releaseProbe
	}
	return response.result, response.err
}

func (r *lifecycleProbeGateRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tmuxcmd.Request(nil), r.requests...)
}
