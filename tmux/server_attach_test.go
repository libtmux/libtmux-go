package tmux

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.attach_session
// libtmux:parity libtmux.session.Session.attach
// libtmux:parity libtmux.session.Session.attach#parameter-branch:exit_:ec1dc0f87e55
// libtmux:parity libtmux.session.Session.attach#parameter-branch:flags_:68f4c159cd01
func TestServerAttachSessionBuildsExactStreamingRequest(t *testing.T) {
	t.Parallel()

	stdin := attachTestFile(t, "stdin")
	stdout := attachTestFile(t, "stdout")
	stderr := attachTestFile(t, "stderr")
	startDirectory := "work;"
	clientFlags := []string{"ignore-size", "read-only"}
	environment := []string{"TERM=xterm-256color"}
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	server := serverWithOptionsAndRunner(ServerOptions{
		SocketPath:         "/tmp/libtmux-attach.sock",
		ConfigFile:         "/tmp/libtmux-attach.conf",
		Colors:             Color256,
		ProcessEnvironment: environment,
	}, runner)

	err := server.AttachSession(context.Background(), AttachSessionRequest{
		Target: "work*",
		AttachSessionOptions: AttachSessionOptions{
			DetachOthers:        true,
			DetachParent:        true,
			NoUpdateEnvironment: true,
			ReadOnly:            true,
			StartDirectory:      &startDirectory,
			ClientFlags:         clientFlags,
			Stdin:               stdin,
			Stdout:              stdout,
			Stderr:              stderr,
		},
	})
	if err != nil {
		t.Fatalf("AttachSession() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one attach", requests)
	}
	assertRequestArguments(t, requests[0], []string{
		"-2", "-f/tmp/libtmux-attach.conf", "-S/tmp/libtmux-attach.sock",
		"attach-session", "-d", "-x", "-E", "-r", "-c", `work\;`,
		"-f", "ignore-size,read-only", "-t", "work*",
	})
	if requests[0].Stdio == nil || requests[0].Stdio.Stdin != stdin ||
		requests[0].Stdio.Stdout != stdout || requests[0].Stdio.Stderr != stderr {
		t.Fatalf("runner stdio = %#v, want caller files", requests[0].Stdio)
	}
	if !slices.Equal(requests[0].Environment, environment) {
		t.Fatalf("runner environment = %#v, want %#v", requests[0].Environment, environment)
	}

	startDirectory = "changed"
	clientFlags[0] = "changed"
	environment[0] = "TERM=changed"
	assertRequestArguments(t, requests[0], []string{
		"-2", "-f/tmp/libtmux-attach.conf", "-S/tmp/libtmux-attach.sock",
		"attach-session", "-d", "-x", "-E", "-r", "-c", `work\;`,
		"-f", "ignore-size,read-only", "-t", "work*",
	})
	if !slices.Equal(requests[0].Environment, []string{"TERM=xterm-256color"}) {
		t.Fatalf("runner environment aliases caller: %#v", requests[0].Environment)
	}
}

func TestAttachSessionTargetShapes(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	server := serverWithRunner(runner)
	if err := server.AttachSession(context.Background(), AttachSessionRequest{}); err != nil {
		t.Fatalf("AttachSession(default target) error = %v", err)
	}
	session := Session{server: server, sessionID: "$7"}
	if err := session.Attach(context.Background(), AttachSessionOptions{}); err != nil {
		t.Fatalf("Session.Attach() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want two attaches", requests)
	}
	assertRequestArguments(t, requests[0], []string{"attach-session"})
	assertRequestArguments(t, requests[1], []string{"attach-session", "-t", "$7"})
	for index, request := range requests {
		if request.Stdio == nil {
			t.Fatalf("request %d has nil streaming marker", index)
		}
	}
}

func TestAttachSessionRejectsUnrepresentableFieldsBeforeExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(Server) error
	}{
		{
			name: "target NUL",
			operation: func(server Server) error {
				return server.AttachSession(context.Background(), AttachSessionRequest{
					Target: "secret-target\x00payload",
				})
			},
		},
		{
			name: "directory NUL",
			operation: func(server Server) error {
				directory := "secret-directory\x00payload"
				return server.AttachSession(context.Background(), AttachSessionRequest{
					AttachSessionOptions: AttachSessionOptions{StartDirectory: &directory},
				})
			},
		},
		{
			name: "client flag NUL",
			operation: func(server Server) error {
				return server.AttachSession(context.Background(), AttachSessionRequest{
					AttachSessionOptions: AttachSessionOptions{
						ClientFlags: []string{"secret-flag\x00payload"},
					},
				})
			},
		},
		{
			name: "zero session target",
			operation: func(server Server) error {
				return (Session{server: server}).Attach(
					context.Background(), AttachSessionOptions{},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{}
			err := test.operation(serverWithRunner(runner))
			if err == nil {
				t.Fatal("operation error = nil")
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want zero", runner.callCount())
			}
			if strings.Contains(err.Error(), "payload") {
				t.Fatalf("operation error retained dynamic value: %v", err)
			}
		})
	}
}

func TestAttachSessionReturnsStreamingFailures(t *testing.T) {
	t.Parallel()

	transportFailure := errors.New("transport failed")
	tests := []struct {
		name       string
		response   versionResponse
		want       error
		wantResult bool
	}{
		{
			name: "nonzero",
			response: versionResponse{result: tmuxcmd.Result{
				Command:  []string{"tmux", "attach-session"},
				Stdout:   []string{"must not be retained"},
				Stderr:   []string{"must not be retained"},
				ExitCode: 1,
			}},
			want:       ErrCommand,
			wantResult: true,
		},
		{
			name:     "transport",
			response: versionResponse{err: transportFailure},
			want:     transportFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{test.response}}
			err := serverWithRunner(runner).AttachSession(
				context.Background(), AttachSessionRequest{},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("AttachSession() error = %v, want %v", err, test.want)
			}
			if test.wantResult {
				var commandError *CommandError
				if !errors.As(err, &commandError) {
					t.Fatalf("AttachSession() error = %#v, want CommandError", err)
				}
				if commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
					commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
					t.Fatalf("AttachSession() CommandError = %#v, want exit-code-only result", commandError.Result)
				}
			}
		})
	}
}

func attachTestFile(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close %s: %v", name, err)
		}
	})
	return file
}
