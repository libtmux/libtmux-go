package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.kill_session
func TestServerKillSessionPreservesTmuxSelector(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 7},
	}}}
	server := serverWithRunner(runner)
	if err := server.KillSession(context.Background(), "work*;"); err != nil {
		t.Fatalf("KillSession() error = %v", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"kill-session", "-t", `work*\;`,
	})
}

// libtmux:parity libtmux.session.Session.kill_window
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:3fc216416bbd
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:8f6d7a09cc34
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:c12246bcb583
func TestSessionKillWindowPreservesCurrentStringAndIndexTargets(t *testing.T) {
	t.Parallel()

	custom := "other:3;"
	empty := ""
	index := -1
	tests := []struct {
		name    string
		request KillWindowRequest
		want    []string
	}{
		{
			name: "current window",
			want: []string{"kill-window", "-t", "$1"},
		},
		{
			name:    "literal tmux target",
			request: KillWindowRequest{Target: &custom},
			want:    []string{"kill-window", "-t", `other:3\;`},
		},
		{
			name:    "explicit empty target",
			request: KillWindowRequest{Target: &empty},
			want:    []string{"kill-window", "-t", ""},
		},
		{
			name:    "session window index",
			request: KillWindowRequest{Index: &index},
			want:    []string{"kill-window", "-t", "$1:-1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{ExitCode: 9},
			}}}
			session := Session{server: serverWithRunner(runner), sessionID: "$1"}
			if err := session.KillWindow(context.Background(), test.request); err != nil {
				t.Fatalf("KillWindow() error = %v", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.want)
		})
	}
}

func TestTargetedKillMethodsRejectInvalidRequestsBeforeExecution(t *testing.T) {
	t.Parallel()

	target := "other"
	index := 1
	tests := []struct {
		name string
		call func(Server) error
		want error
	}{
		{
			name: "server target NUL",
			call: func(server Server) error {
				return server.KillSession(context.Background(), "secret\x00value")
			},
			want: ErrInvalidServerCommandRequest,
		},
		{
			name: "window target and index",
			call: func(server Server) error {
				return (Session{server: server, sessionID: "$1"}).KillWindow(
					context.Background(),
					KillWindowRequest{Target: &target, Index: &index},
				)
			},
			want: ErrInvalidRequest,
		},
		{
			name: "window target NUL",
			call: func(server Server) error {
				bad := "secret\x00value"
				return (Session{server: server, sessionID: "$1"}).KillWindow(
					context.Background(), KillWindowRequest{Target: &bad},
				)
			},
			want: ErrInvalidServerCommandRequest,
		},
		{
			name: "missing session identity",
			call: func(server Server) error {
				return (Session{server: server}).KillWindow(
					context.Background(), KillWindowRequest{Index: &index},
				)
			},
			want: ErrMissingTarget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{}
			err := test.call(serverWithRunner(runner))
			if !errors.Is(err, test.want) {
				t.Fatalf("targeted kill error = %v, want %v", err, test.want)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

// libtmux:parity libtmux.server.Server.kill_session
// libtmux:parity libtmux.session.Session.kill_window
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:3fc216416bbd
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:8f6d7a09cc34
// libtmux:parity libtmux.session.Session.kill_window#parameter-branch:target_window:c12246bcb583
func TestTargetedKillMethodsUseStderrOnlyCompletionPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(Server) error
	}{
		{
			name: "server session",
			call: func(server Server) error {
				return server.KillSession(context.Background(), "work")
			},
		},
		{
			name: "session window",
			call: func(server Server) error {
				return (Session{server: server, sessionID: "$1"}).KillWindow(
					context.Background(), KillWindowRequest{},
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{Stderr: []string{"tmux rejected target"}, ExitCode: 1},
			}}}
			err := test.call(serverWithRunner(runner))
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("targeted kill error = %v, want ErrCommand", err)
			}
		})
	}
}
