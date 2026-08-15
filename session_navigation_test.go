package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/tmux-python/libtmux/golang/internal/tmuxcmd"
)

// libtmux:parity libtmux.session.Session.last_window
// libtmux:parity libtmux.session.Session.next_window
// libtmux:parity libtmux.session.Session.previous_window
// libtmux:parity libtmux.session.Session.select_window
func TestSessionWindowNavigationReturnsActiveWinlink(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	tests := []struct {
		name     string
		wantArgs []string
		invoke   func(Session) (Window, error)
	}{
		{
			name:     "last",
			wantArgs: []string{"last-window", "-t", "$7"},
			invoke: func(session Session) (Window, error) {
				return session.LastWindow(context.Background())
			},
		},
		{
			name:     "next",
			wantArgs: []string{"next-window", "-t", "$7"},
			invoke: func(session Session) (Window, error) {
				return session.NextWindow(context.Background())
			},
		},
		{
			name:     "previous",
			wantArgs: []string{"previous-window", "-t", "$7"},
			invoke: func(session Session) (Window, error) {
				return session.PreviousWindow(context.Background())
			},
		},
		{
			name:     "stable id",
			wantArgs: []string{"select-window", "-t", "$7:@8"},
			invoke: func(session Session) (Window, error) {
				return session.SelectWindow(context.Background(), SelectWindowRequest{
					WindowID: "@8",
				})
			},
		},
		{
			name:     "index",
			wantArgs: []string{"select-window", "-t", "$7:2"},
			invoke: func(session Session) (Window, error) {
				index := 2
				return session.SelectWindow(context.Background(), SelectWindowRequest{
					Index: &index,
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 0}}}
			responses = append(responses, lifecycleLookupResponses(
				t,
				version,
				"list-sessions",
				map[string]string{
					"session_id":    "$7",
					"window_id":     "@8",
					"window_index":  "2",
					"window_active": "1",
				},
			)...)
			runner := &versionQueueRunner{responses: responses}
			session := Session{server: serverWithRunner(runner), sessionID: "$7"}

			window, err := test.invoke(session)
			if err != nil {
				t.Fatalf("navigation error = %v", err)
			}
			if window.sessionID != "$7" || window.windowID != "@8" || window.windowIndex != 2 {
				t.Fatalf(
					"active winlink = %s:%s:%d, want $7:@8:2",
					window.sessionID,
					window.windowID,
					window.windowIndex,
				)
			}
			active, queried := window.Active()
			if !queried || !active {
				t.Fatalf("Active() = (%t, %t), want (true, true)", active, queried)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.wantArgs)
		})
	}
}

func TestSelectWindowRejectsInvalidRequestsBeforeExecution(t *testing.T) {
	t.Parallel()

	negative := -1
	zero := 0
	tests := []struct {
		name    string
		session SessionID
		request SelectWindowRequest
		want    error
	}{
		{name: "missing selector", session: "$7", want: ErrInvalidServerCommandRequest},
		{
			name:    "both selectors",
			session: "$7",
			request: SelectWindowRequest{WindowID: "@8", Index: &zero},
			want:    ErrInvalidServerCommandRequest,
		},
		{
			name:    "negative index",
			session: "$7",
			request: SelectWindowRequest{Index: &negative},
			want:    ErrInvalidServerCommandRequest,
		},
		{
			name:    "malformed window id",
			session: "$7",
			request: SelectWindowRequest{WindowID: "8"},
			want:    ErrInvalidTarget,
		},
		{
			name:    "malformed session id",
			session: "7",
			request: SelectWindowRequest{WindowID: "@8"},
			want:    ErrInvalidTarget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			_, err := (Session{
				server:    serverWithRunner(runner),
				sessionID: test.session,
			}).SelectWindow(context.Background(), test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("SelectWindow() error = %v, want %v", err, test.want)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestSelectWindowRedactsUnrepresentableWindowID(t *testing.T) {
	t.Parallel()

	const secret = "private-window-selector"
	runner := &versionQueueRunner{}
	_, err := (Session{
		server: serverWithRunner(runner), sessionID: "$7",
	}).SelectWindow(context.Background(), SelectWindowRequest{
		WindowID: WindowID("@8\x00" + secret),
	})
	if !errors.Is(err, ErrInvalidServerCommandRequest) || errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("SelectWindow() error = %v, want only ErrInvalidServerCommandRequest", err)
	}
	var requestError *ServerCommandRequestError
	if !errors.As(err, &requestError) || requestError.Value != "[redacted]" {
		t.Fatalf("SelectWindow() error = %#v, want redacted request error", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("SelectWindow() runner calls = %d, want 0", calls)
	}
	assertErrorGraphRedacts(t, err, secret)
}

func TestSessionNavigationSurfacesCommandErrorWithoutRefresh(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"no last window"}, ExitCode: 1,
	}}}}
	_, err := (Session{
		server:    serverWithRunner(runner),
		sessionID: "$7",
	}).LastWindow(context.Background())
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Subcommand != "last-window" {
		t.Fatalf("LastWindow() error = %#v, want last-window CommandError", err)
	}
	if calls := runner.callCount(); calls != 1 {
		t.Fatalf("runner calls = %d, want command only", calls)
	}
}
