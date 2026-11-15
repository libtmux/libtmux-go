package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestKillOptionsBuildExactTargetsAndPythonOrder(t *testing.T) {
	t.Parallel()

	t.Run("session", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
		session := Session{server: serverWithRunner(runner), sessionID: "$1"}
		err := session.KillWith(context.Background(), SessionKillRequest{AllExcept: true})
		if err != nil {
			t.Fatalf("KillWith() error = %v", err)
		}
		assertRequestArguments(t, runner.recordedRequests()[0], []string{
			"kill-session", "-t", "$1", "-a",
		})
	})

	t.Run("window", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
		window := Window{
			server: serverWithRunner(runner), sessionID: "$1", windowID: "@2",
		}
		if err := window.KillOthers(context.Background()); err != nil {
			t.Fatalf("KillOthers() error = %v", err)
		}
		assertRequestArguments(t, runner.recordedRequests()[0], []string{
			"kill-window", "-t", "$1:@2", "-a",
		})
	})

	t.Run("pane", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
		pane := Pane{
			server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
		}
		if err := pane.KillOthers(context.Background()); err != nil {
			t.Fatalf("KillOthers() error = %v", err)
		}
		assertRequestArguments(t, runner.recordedRequests()[0], []string{
			"kill-pane", "-t", "$1:.%3", "-a",
		})
	})
}

func TestSessionKillRejectsConflictingModesBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []SessionKillRequest{
		{AllExcept: true, ClearAlerts: true},
		{AllExcept: true, Group: true},
		{ClearAlerts: true, Group: true},
		{AllExcept: true, ClearAlerts: true, Group: true},
	}
	for _, request := range tests {
		runner := &versionQueueRunner{}
		err := (Session{server: serverWithRunner(runner), sessionID: "$1"}).KillWith(
			context.Background(), request,
		)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("KillWith(%#v) error = %v, want ErrInvalidRequest", request, err)
		}
		if calls := runner.callCount(); calls != 0 {
			t.Fatalf("KillWith(%#v) runner calls = %d, want 0", request, calls)
		}
	}
}

func TestKillFamilyIgnoresCompletedNonzeroWithoutStderr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(Server) error
	}{
		{
			name: "session",
			call: func(server Server) error {
				return (Session{server: server, sessionID: "$1"}).Kill(context.Background())
			},
		},
		{
			name: "window",
			call: func(server Server) error {
				return (Window{server: server, sessionID: "$1", windowID: "@2"}).Kill(
					context.Background(),
				)
			},
		},
		{
			name: "other windows",
			call: func(server Server) error {
				return (Window{server: server, sessionID: "$1", windowID: "@2"}).KillOthers(
					context.Background(),
				)
			},
		},
		{
			name: "pane",
			call: func(server Server) error {
				return (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).Kill(context.Background())
			},
		},
		{
			name: "other panes",
			call: func(server Server) error {
				return (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).KillOthers(context.Background())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{ExitCode: 9},
			}}}
			if err := test.call(serverWithRunner(runner)); err != nil {
				t.Fatalf("kill error = %v, want nil for nonzero without stderr", err)
			}
		})
	}
}

func TestSessionKillGroupWarnsAndOmitsBefore37(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
		{result: tmuxcmd.Result{}},
	}}
	warnings := make([]Warning, 0, 1)
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported: DegradeUnsupported,
		WarningHandler: func(warning Warning) {
			warnings = append(warnings, warning)
		},
	}, runner)
	err := (Session{server: server, sessionID: "$1"}).KillWith(
		context.Background(),
		SessionKillRequest{Group: true},
	)
	if err != nil {
		t.Fatalf("KillWith() error = %v", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[1], []string{
		"kill-session", "-t", "$1",
	})
	if len(warnings) != 1 || warnings[0].Kind != WarningUnsupportedFeature ||
		warnings[0].Subcommand != "kill-session" || warnings[0].Feature != "group" ||
		warnings[0].RequiredVersion.String() != "3.7" {
		t.Fatalf("warnings = %#v, want one group compatibility warning", warnings)
	}
}

func TestSessionKillWithoutGroupSkipsVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	session := Session{server: serverWithRunner(runner), sessionID: "$1"}
	if err := session.KillWith(context.Background(), SessionKillRequest{ClearAlerts: true}); err != nil {
		t.Fatalf("KillWith() error = %v", err)
	}
	if requests := runner.recordedRequests(); len(requests) != 1 {
		t.Fatalf("runner requests = %d, want one command without a version probe", len(requests))
	} else {
		assertRequestArguments(t, requests[0], []string{"kill-session", "-t", "$1", "-C"})
	}
}

func TestKillOptionsValidateExactTargetsBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	err := (Session{server: serverWithRunner(runner)}).KillWith(
		context.Background(),
		SessionKillRequest{Group: true},
	)
	if !errors.Is(err, ErrMissingTarget) {
		t.Fatalf("KillWith() error = %v, want ErrMissingTarget", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("runner calls = %d, want 0", calls)
	}
}
