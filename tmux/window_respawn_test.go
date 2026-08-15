package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.window.Window.respawn
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:kill:c73eb1e87efe
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:shell:613b2dd997a5
// libtmux:parity libtmux.window.Window.respawn#parameter-branch:start_directory:d91549582997
func TestWindowRespawnBuildsExactArgumentsAndRedactsCompletedErrors(t *testing.T) {
	t.Parallel()

	command := "printf done;"
	startDirectory := ""
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Command:  []string{"tmux", "respawn-window", "secret"},
		Stdout:   []string{"sensitive stdout"},
		Stderr:   []string{"respawn failed"},
		ExitCode: 1,
	}}}}
	window, err := (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
	}).Respawn(context.Background(), RespawnRequest{
		Command:        &command,
		StartDirectory: &startDirectory,
		Environment: map[string]string{
			"ZED":   "last",
			"ALPHA": "first",
		},
		Kill: true,
	})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("Respawn() error = %v, want ErrCommand", err)
	}
	if window.sessionID != "" || window.windowID != "" || window.windowIndex != 0 ||
		window.server.state != nil {
		t.Fatalf("Respawn() window = %#v, want zero value on command failure", window)
	}
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("Respawn() error = %#v, want CommandError", err)
	}
	if commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
		t.Fatalf("Respawn() CommandError result = %#v, want exit-only redaction", commandError.Result)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"respawn-window", "-t", "$7:0", "-k", "-c.",
		"-eALPHA=first", "-eZED=last", `printf done\;`,
	})
}

func TestWindowRespawnPreservesNilAndEmptyCommand(t *testing.T) {
	t.Parallel()

	empty := ""
	for _, test := range []struct {
		name    string
		command *string
		want    []string
	}{
		{
			name: "nil reuses stored command",
			want: []string{"respawn-window", "-t", "$7:0", "-k"},
		},
		{
			name:    "empty remains an operand",
			command: &empty,
			want:    []string{"respawn-window", "-t", "$7:0", "-k", ""},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Stderr: []string{"stop after argv capture"}, ExitCode: 1,
			}}}}
			_, err := (Window{
				server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
			}).Respawn(context.Background(), RespawnRequest{Command: test.command, Kill: true})
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("Respawn() error = %v, want ErrCommand", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.want)
		})
	}
}

func TestWindowRespawnExpandsCurrentUserStartDirectory(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	startDirectory := "~/respawn-work"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"stop after argv capture"}, ExitCode: 1,
	}}}}
	_, err = (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
	}).Respawn(context.Background(), RespawnRequest{StartDirectory: &startDirectory})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("Respawn() error = %v, want ErrCommand", err)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"respawn-window", "-t", "$7:0", "-c" + filepath.Join(home, "respawn-work"),
	})
}

func TestWindowRespawnRejectsInvalidInputBeforeExecutionWithoutSecrets(t *testing.T) {
	t.Parallel()

	nulCommand := "command-secret\x00tail"
	nulDirectory := "directory-secret\x00tail"
	for _, test := range []struct {
		name    string
		window  Window
		request RespawnRequest
		want    error
		secret  string
	}{
		{
			name:   "invalid target",
			window: Window{sessionID: "$7", windowID: "window-without-sigil"},
			want:   ErrInvalidTarget,
		},
		{
			name:    "NUL command",
			window:  Window{sessionID: "$7", windowID: "@8"},
			request: RespawnRequest{Command: &nulCommand},
			want:    ErrInvalidServerCommandRequest,
			secret:  "command-secret",
		},
		{
			name:    "NUL directory",
			window:  Window{sessionID: "$7", windowID: "@8"},
			request: RespawnRequest{StartDirectory: &nulDirectory},
			want:    ErrInvalidServerCommandRequest,
			secret:  "directory-secret",
		},
		{
			name:    "invalid environment name",
			window:  Window{sessionID: "$7", windowID: "@8"},
			request: RespawnRequest{Environment: map[string]string{"BAD=NAME": "value"}},
			want:    ErrInvalidEnvironmentName,
		},
		{
			name:    "invalid environment value",
			window:  Window{sessionID: "$7", windowID: "@8"},
			request: RespawnRequest{Environment: map[string]string{"TOKEN": "value-secret\nnext"}},
			want:    ErrInvalidEnvironmentValue,
			secret:  "value-secret",
		},
		{
			name:   "named user directory",
			window: Window{sessionID: "$7", windowID: "@8"},
			request: func() RespawnRequest {
				path := "~other/work"
				return RespawnRequest{StartDirectory: &path}
			}(),
			want: ErrInvalidRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			window := test.window
			window.server = serverWithRunner(runner)
			_, err := window.Respawn(context.Background(), test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Respawn() error = %v, want %v", err, test.want)
			}
			if test.secret != "" && strings.Contains(err.Error(), test.secret) {
				t.Fatalf("Respawn() error disclosed secret: %v", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestWindowRespawnIgnoresCompletedNonzeroWithoutStderrAndRefreshes(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7b")
	responses := []versionResponse{{result: tmuxcmd.Result{ExitCode: 9}}}
	responses = append(responses, lifecycleLookupResponses(
		t,
		version,
		"list-windows",
		map[string]string{
			"session_id": "$7", "window_id": "@8", "window_index": "5",
		},
	)...)
	runner := &versionQueueRunner{responses: responses}
	window, err := (Window{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", windowIndex: 2,
	}).Respawn(context.Background(), RespawnRequest{Kill: true})
	if err != nil {
		t.Fatalf("Respawn() error = %v", err)
	}
	if window.windowIndex != 5 {
		t.Fatalf("Respawn() WindowIndex = %d, want refreshed 5", window.windowIndex)
	}
}

func TestWindowRespawnReturnsReceiverWhenRefreshFails(t *testing.T) {
	t.Parallel()

	receiver := Window{
		server: serverWithRunner(&versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{}},
			{err: context.Canceled},
		}}),
		sessionID: "$7", windowID: "@8", windowIndex: 5,
	}
	window, err := receiver.Respawn(context.Background(), RespawnRequest{Kill: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Respawn() error = %v, want context canceled", err)
	}
	if window.sessionID != receiver.sessionID || window.windowID != receiver.windowID ||
		window.windowIndex != receiver.windowIndex {
		t.Fatalf("Respawn() window = %#v, want original receiver %#v", window, receiver)
	}
}
