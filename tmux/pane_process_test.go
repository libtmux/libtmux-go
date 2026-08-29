package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.pane.Pane.respawn
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:kill:c73eb1e87efe
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:shell:613b2dd997a5
// libtmux:parity libtmux.pane.Pane.respawn#parameter-branch:start_directory:d91549582997
func TestPaneRespawnUsesExactLinkedTargetAndReturnsRefreshedPane(t *testing.T) {
	t.Parallel()

	command := "sleep 30"
	version := mustParseVersion(t, "3.7b")
	responses := []versionResponse{{result: tmuxcmd.Result{}}}
	responses = append(responses, lifecycleLookupResponses(
		t,
		version,
		"list-panes",
		map[string]string{
			"session_id": "$7", "window_id": "@8", "window_index": "5",
			"pane_id": "%9", "pane_index": "2",
		},
	)...)
	runner := &versionQueueRunner{responses: responses}
	pane, err := (Pane{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8",
		windowIndex: 5, paneID: "%9", paneIndex: 1,
	}).Respawn(context.Background(), RespawnRequest{
		Command: &command,
		Environment: map[string]string{
			"ZED": "last", "ALPHA": "first",
		},
		Kill: true,
	})
	if err != nil {
		t.Fatalf("Respawn() error = %v", err)
	}
	if pane.paneIndex != 2 {
		t.Fatalf("Respawn() PaneIndex = %d, want refreshed 2", pane.paneIndex)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"respawn-pane", "-t", "$7:5.%9", "-k",
		"-eALPHA=first", "-eZED=last", "sleep 30",
	})
}

func TestPaneRespawnReturnsReceiverWhenRefreshFails(t *testing.T) {
	t.Parallel()

	receiver := Pane{
		server: serverWithRunner(&versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{}},
			{err: context.Canceled},
		}}),
		sessionID: "$7", windowID: "@8", windowIndex: 5, paneID: "%9", paneIndex: 2,
	}
	pane, err := receiver.Respawn(context.Background(), RespawnRequest{Kill: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Respawn() error = %v, want context canceled", err)
	}
	if pane.sessionID != receiver.sessionID || pane.windowID != receiver.windowID ||
		pane.windowIndex != receiver.windowIndex || pane.paneID != receiver.paneID ||
		pane.paneIndex != receiver.paneIndex {
		t.Fatalf("Respawn() pane = %#v, want original receiver %#v", pane, receiver)
	}
}

// libtmux:parity libtmux.pane.Pane.paste_buffer
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:bracket:f0ca1fd9e751
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:delete_after:c39a94610567
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:linefeed_separator:e71619e032d9
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:no_vis:5a188285cf0c
// libtmux:parity libtmux.pane.Pane.paste_buffer#parameter-branch:separator:70ecb771763a
func TestPasteBufferBuildsPythonOrderedArguments(t *testing.T) {
	t.Parallel()

	bufferName := "named;"
	separator := "separator;"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{}},
	}}
	err := (Pane{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
	}).PasteBuffer(context.Background(), PasteBufferRequest{
		BufferName:        &bufferName,
		DeleteAfter:       true,
		LinefeedSeparator: true,
		Bracket:           true,
		Separator:         &separator,
		NoVis:             true,
	})
	if err != nil {
		t.Fatalf("PasteBuffer() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and paste", requests)
	}
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"paste-buffer", "-t", "$7:0.%9", "-d", "-r", "-p",
		"-b", `named\;`, "-s", `separator\;`, "-S",
	})
}

func TestPasteBufferPreservesNilAndEmptyValues(t *testing.T) {
	t.Parallel()

	empty := ""
	for _, test := range []struct {
		name    string
		request PasteBufferRequest
		want    []string
	}{
		{
			name: "nil uses top buffer and default separator",
			want: []string{"paste-buffer", "-t", "$7:0.%9"},
		},
		{
			name:    "empty values remain operands",
			request: PasteBufferRequest{BufferName: &empty, Separator: &empty},
			want:    []string{"paste-buffer", "-t", "$7:0.%9", "-b", "", "-s", ""},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			err := (Pane{
				server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
			}).PasteBuffer(context.Background(), test.request)
			if err != nil {
				t.Fatalf("PasteBuffer() error = %v", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.want)
		})
	}
}

// libtmux:parity libtmux.pane.Pane.paste_buffer#version-branch:tmux-version:c6a18af85027
// libtmux:parity libtmux.pane.Pane.paste_buffer#warning:7e6d34b02246
func TestPasteBufferVersionGatesNoVis(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		version     string
		noVis       bool
		wantCalls   int
		wantWarning bool
		wantArgs    []string
	}{
		{
			name:      "not requested does not probe",
			wantCalls: 1,
			wantArgs:  []string{"paste-buffer", "-t", "$7:0.%9"},
		},
		{
			name:    "3.6 warns and omits",
			version: "3.6", noVis: true, wantCalls: 2, wantWarning: true,
			wantArgs: []string{"paste-buffer", "-t", "$7:0.%9"},
		},
		{
			name:    "3.7 emits",
			version: "3.7", noVis: true, wantCalls: 2,
			wantArgs: []string{"paste-buffer", "-t", "$7:0.%9", "-S"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			warnings := make([]Warning, 0, 1)
			responses := make([]versionResponse, 0, test.wantCalls)
			if test.version != "" {
				responses = append(responses, versionResponse{result: tmuxcmd.Result{
					Stdout: []string{"tmux " + test.version},
				}})
			}
			responses = append(responses, versionResponse{result: tmuxcmd.Result{}})
			runner := &versionQueueRunner{responses: responses}
			server := serverWithOptionsAndRunner(ServerOptions{
				Unsupported: DegradeUnsupported,
				WarningHandler: func(warning Warning) {
					warnings = append(warnings, warning)
				},
			}, runner)
			err := (Pane{
				server: server, sessionID: "$7", windowID: "@8", paneID: "%9",
			}).PasteBuffer(context.Background(), PasteBufferRequest{NoVis: test.noVis})
			if err != nil {
				t.Fatalf("PasteBuffer() error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != test.wantCalls {
				t.Fatalf("runner requests = %#v, want %d", requests, test.wantCalls)
			}
			assertRequestArguments(t, requests[len(requests)-1], test.wantArgs)
			if (len(warnings) == 1) != test.wantWarning {
				t.Fatalf("warnings = %#v, want warning %t", warnings, test.wantWarning)
			}
			if test.wantWarning {
				warning := warnings[0]
				if warning.Kind != WarningUnsupportedFeature || warning.Subcommand != "paste-buffer" ||
					warning.Feature != "no_vis" || warning.RequiredVersion.String() != "3.7" {
					t.Fatalf("warning = %#v", warning)
				}
			}
		})
	}
}

func TestPasteBufferVersionFailureStopsBeforeWarningAndPaste(t *testing.T) {
	t.Parallel()

	warnings := 0
	runner := &versionQueueRunner{responses: []versionResponse{{err: context.Canceled}}}
	server := serverWithOptionsAndRunner(ServerOptions{
		WarningHandler: func(Warning) { warnings++ },
	}, runner)
	err := (Pane{
		server: server, sessionID: "$7", windowID: "@8", paneID: "%9",
	}).PasteBuffer(context.Background(), PasteBufferRequest{NoVis: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PasteBuffer() error = %v, want context canceled", err)
	}
	if warnings != 0 || runner.callCount() != 1 {
		t.Fatalf("warnings/calls = %d/%d, want 0/1", warnings, runner.callCount())
	}
}

func TestPasteBufferSnapshotsPointerValuesBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	bufferName := "before-name"
	separator := "before-separator"
	runner := &paneInputBlockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	pane := Pane{
		server: degradingServerWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
	}
	done := make(chan error, 1)
	go func() {
		done <- pane.PasteBuffer(context.Background(), PasteBufferRequest{
			BufferName: &bufferName,
			Separator:  &separator,
			NoVis:      true,
		})
	}()
	<-runner.started
	bufferName = "after-name\x00invalid"
	separator = "after-separator\x00invalid"
	close(runner.release)
	if err := <-done; err != nil {
		t.Fatalf("PasteBuffer() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and paste", requests)
	}
	assertRequestArguments(t, requests[1], []string{
		"paste-buffer", "-t", "$7:0.%9",
		"-b", "before-name", "-s", "before-separator",
	})
}

func TestPasteBufferRejectsNULBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	nulName := "name-secret\x00tail"
	nulSeparator := "separator-secret\x00tail"
	for _, test := range []struct {
		name    string
		request PasteBufferRequest
		secret  string
	}{
		{name: "buffer name", request: PasteBufferRequest{BufferName: &nulName, NoVis: true}, secret: "name-secret"},
		{name: "separator", request: PasteBufferRequest{Separator: &nulSeparator, NoVis: true}, secret: "separator-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := (Pane{
				server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
			}).PasteBuffer(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("PasteBuffer() error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("PasteBuffer() error disclosed secret: %v", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestPasteBufferUsesPythonCompletedErrorPolicy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		result  tmuxcmd.Result
		wantErr error
	}{
		{name: "nonzero without stderr is ignored", result: tmuxcmd.Result{ExitCode: 9}},
		{name: "stderr at zero is loud", result: tmuxcmd.Result{Stderr: []string{"warning"}}, wantErr: ErrCommand},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: test.result}}}
			err := (Pane{
				server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
			}).PasteBuffer(context.Background(), PasteBufferRequest{})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("PasteBuffer() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

// libtmux:parity libtmux.pane.Pane.pipe
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:command:581de879aee9
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:input_only:09d5057c767b
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:output_only:3a41f8d69dcf
// libtmux:parity libtmux.pane.Pane.pipe#parameter-branch:toggle:fecb07ce0a7c
func TestPipeBuildsPythonOrderedArgumentsAndRedactsCompletedErrors(t *testing.T) {
	t.Parallel()

	command := "cat >> secret;"
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Command:  []string{"tmux", "pipe-pane", "secret"},
		Stdout:   []string{"sensitive stdout"},
		Stderr:   []string{"pipe failed"},
		ExitCode: 1,
	}}}}
	err := (Pane{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
	}).Pipe(context.Background(), PipePaneRequest{
		Command: &command, OutputOnly: true, InputOnly: true, Toggle: true,
	})
	if !errors.Is(err, ErrCommand) {
		t.Fatalf("Pipe() error = %v, want ErrCommand", err)
	}
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("Pipe() error = %#v, want CommandError", err)
	}
	if commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
		t.Fatalf("Pipe() CommandError result = %#v, want exit-only redaction", commandError.Result)
	}
	assertRequestArguments(t, runner.recordedRequests()[0], []string{
		"pipe-pane", "-t", "$7:0.%9", "-O", "-I", "-o", `cat >> secret\;`,
	})
}

func TestPipePreservesNilAndEmptyCommand(t *testing.T) {
	t.Parallel()

	empty := ""
	for _, test := range []struct {
		name    string
		command *string
		want    []string
	}{
		{name: "nil stops without operand", want: []string{"pipe-pane", "-t", "$7:0.%9"}},
		{
			name: "empty remains an operand", command: &empty,
			want: []string{"pipe-pane", "-t", "$7:0.%9", ""},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{ExitCode: 9}}}}
			err := (Pane{
				server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
			}).Pipe(context.Background(), PipePaneRequest{Command: test.command})
			if err != nil {
				t.Fatalf("Pipe() error = %v, want ignored nonzero without stderr", err)
			}
			assertRequestArguments(t, runner.recordedRequests()[0], test.want)
		})
	}
}

func TestPaneProcessMethodsRejectInvalidExactTargetsBeforeExecution(t *testing.T) {
	t.Parallel()

	command := "echo ok"
	for _, test := range []struct {
		name   string
		invoke func(Pane) error
	}{
		{
			name: "respawn",
			invoke: func(pane Pane) error {
				_, err := pane.Respawn(context.Background(), RespawnRequest{Command: &command})
				return err
			},
		},
		{
			name: "paste",
			invoke: func(pane Pane) error {
				return pane.PasteBuffer(context.Background(), PasteBufferRequest{})
			},
		},
		{
			name: "pipe",
			invoke: func(pane Pane) error {
				return pane.Pipe(context.Background(), PipePaneRequest{Command: &command})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			err := test.invoke(Pane{
				server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "pane-without-sigil",
			})
			if !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("operation error = %v, want ErrInvalidTarget", err)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestPipeRejectsNULWithoutDisclosingCommand(t *testing.T) {
	t.Parallel()

	command := "pipe-secret\x00tail"
	runner := &versionQueueRunner{}
	err := (Pane{
		server: serverWithRunner(runner), sessionID: "$7", windowID: "@8", paneID: "%9",
	}).Pipe(context.Background(), PipePaneRequest{Command: &command})
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("Pipe() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if strings.Contains(err.Error(), "pipe-secret") {
		t.Fatalf("Pipe() error disclosed command: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.callCount())
	}
}
