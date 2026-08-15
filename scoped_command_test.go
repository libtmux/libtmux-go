package tmux

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.session.Session.cmd
// libtmux:parity libtmux.session.Session.cmd#parameter-branch:target:7179a7fee946
// libtmux:parity libtmux.common.CmdMixin
// libtmux:parity libtmux.common.CmdMixin.cmd
// libtmux:parity libtmux.common.CmdProtocol
// libtmux:parity libtmux.common.CmdProtocol.__call__
// libtmux:parity libtmux.pane.Pane.cmd
// libtmux:parity libtmux.pane.Pane.cmd#parameter-branch:target:7179a7fee946
// libtmux:parity libtmux.window.Window.cmd
// libtmux:parity libtmux.window.Window.cmd#parameter-branch:target:7179a7fee946
func TestObjectCmdInjectsStableTargetAfterSubcommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(context.Context, ...string) (CommandResult, error)
		want []string
	}{
		{
			name: "session",
			run:  (Session{sessionID: SessionID("$1")}).Cmd,
			want: []string{"rename-session", "-t", "$1", "new"},
		},
		{
			name: "window",
			run:  (Window{windowID: WindowID("@2")}).Cmd,
			want: []string{"rename-window", "-t", "@2", "new"},
		},
		{
			name: "pane",
			run:  (Pane{paneID: PaneID("%3")}).Cmd,
			want: []string{"send-keys", "-t", "%3", "literal"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{
				result: tmuxcmd.Result{ExitCode: 0},
			}}}
			server := serverWithRunner(runner)
			switch test.name {
			case "session":
				test.run = (Session{server: server, sessionID: SessionID("$1")}).Cmd
			case "window":
				test.run = (Window{server: server, windowID: WindowID("@2")}).Cmd
			case "pane":
				test.run = (Pane{server: server, paneID: PaneID("%3")}).Cmd
			}
			_, err := test.run(context.Background(), test.want[0], test.want[3])
			if err != nil {
				t.Fatalf("Cmd() error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 || !slices.Equal(requests[0].Arguments, test.want) {
				t.Fatalf("Cmd() arguments = %#v, want %#v", requests, test.want)
			}
		})
	}
}

func TestObjectCmdRequiresSubcommand(t *testing.T) {
	t.Parallel()

	result, err := (Pane{paneID: PaneID("%1")}).Cmd(context.Background())
	if !errors.Is(err, ErrMissingSubcommand) {
		t.Fatalf("Cmd() error = %v, want ErrMissingSubcommand", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("Cmd() ExitCode = %d, want -1", result.ExitCode)
	}

	result, err = (Pane{}).Cmd(context.Background(), "send-keys")
	if !errors.Is(err, ErrMissingTarget) {
		t.Fatalf("zero Pane.Cmd() error = %v, want ErrMissingTarget", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("zero Pane.Cmd() ExitCode = %d, want -1", result.ExitCode)
	}

	runner := &versionQueueRunner{}
	server := serverWithRunner(runner)
	result, err = (Pane{server: server, paneID: "pane-name"}).Cmd(
		context.Background(),
		"send-keys",
	)
	if !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("malformed Pane.Cmd() error = %v, want ErrInvalidTarget", err)
	}
	if result.ExitCode != -1 || runner.callCount() != 0 {
		t.Fatalf(
			"malformed Pane.Cmd() = (exit %d, calls %d), want (-1, 0)",
			result.ExitCode,
			runner.callCount(),
		)
	}
}

func TestObjectLiteralCmdEscapesTargetAndArguments(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	server := serverWithRunner(runner)
	pane := Pane{
		server: server, sessionID: "$1", windowID: "@2", windowIndex: 3,
		paneID: PaneID("%3"),
	}
	_, err := pane.literalCmd(context.Background(), "send-keys", "literal;")
	if err != nil {
		t.Fatalf("literalCmd() error = %v", err)
	}
	want := []string{"send-keys", "-t", `$1:3.%3`, `literal\;`}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("literalCmd() arguments = %#v, want %#v", requests, want)
	}
}

func TestObjectRawCmdPreservesTargetAndArgumentSeparators(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{
		result: tmuxcmd.Result{ExitCode: 0},
	}}}
	server := serverWithRunner(runner)
	pane := Pane{server: server, paneID: PaneID("%3")}
	_, err := pane.Cmd(context.Background(), "send-keys", "literal;")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	want := []string{"send-keys", "-t", "%3", "literal;"}
	requests := runner.recordedRequests()
	if len(requests) != 1 || !slices.Equal(requests[0].Arguments, want) {
		t.Fatalf("Cmd() arguments = %#v, want raw %#v", requests, want)
	}
}

func TestObjectLiteralCmdRejectsNULBeforeTargetValidation(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	pane := Pane{
		server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", windowIndex: 3,
		paneID: PaneID("%3\x00secret"),
	}
	_, err := pane.literalCmd(context.Background(), "send-keys", "value")
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("literalCmd() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("literalCmd() error retained target: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want validation before execution", runner.callCount())
	}
}
