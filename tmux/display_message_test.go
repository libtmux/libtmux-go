package tmux

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.display_message
// libtmux:parity libtmux.server.Server.display_message#overload:150116cee586
// libtmux:parity libtmux.server.Server.display_message#overload:d28d6ff0f270
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:all_formats:212a3f752cda
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:cmd,delay,format_string,target_client:da0890ac4ef0
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:cmd:e8816765af61
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:delay:0b3ab3dbe007
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:format_string:7284ef554e76
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:get_text:b70b1882775d
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:get_text:b70b1882775d:2
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:no_expand:fdf65ac06ee0
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:notify:546eb0a1d914
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.display_message#parameter-branch:verbose:414278ee0d55
// libtmux:parity libtmux.server.Server.display_message#version-branch:tmux-version:5bb2ac269d05
// libtmux:parity libtmux.server.Server.display_message#warning:25890e6f203b
// libtmux:parity libtmux.server.Server.display_message#warning:fe1b297a0f22
// libtmux:parity libtmux.pane.Pane.display_message
// libtmux:parity libtmux.pane.Pane.display_message#overload:327bb68aa279
// libtmux:parity libtmux.pane.Pane.display_message#overload:54e4d39e840a
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:all_formats:212a3f752cda
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:cmd,delay,format_string,target_client:da0890ac4ef0
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:cmd:e8816765af61
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:delay:0b3ab3dbe007
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:format_string:7284ef554e76
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:get_text:b70b1882775d
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:get_text:b70b1882775d:2
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:no_expand:fdf65ac06ee0
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:notify:546eb0a1d914
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:update_pane:36478bb48351
// libtmux:parity libtmux.pane.Pane.display_message#parameter-branch:verbose:414278ee0d55
// libtmux:parity libtmux.window.Window.display_message
// libtmux:parity libtmux.window.Window.display_message#overload:150116cee586
// libtmux:parity libtmux.window.Window.display_message#overload:d28d6ff0f270
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:all_formats:212a3f752cda
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:cmd,delay,format_string,target_client:da0890ac4ef0
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:cmd:e8816765af61
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:delay:0b3ab3dbe007
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:format_string:7284ef554e76
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:get_text:b70b1882775d
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:get_text:b70b1882775d:2
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:no_expand:fdf65ac06ee0
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:notify:546eb0a1d914
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.window.Window.display_message#parameter-branch:verbose:414278ee0d55
func TestDisplayMessageBuildsExactArgumentsByScope(t *testing.T) {
	t.Parallel()

	format := "#{window_id}:#{pane_id}"
	client := ClientName("/dev/pts/9")
	delay := 250
	request := DisplayMessageRequest{
		Message:      "#{version}",
		Print:        true,
		Format:       &format,
		AllFormats:   true,
		Verbose:      true,
		NoExpand:     true,
		TargetClient: client,
		Delay:        &delay,
		Notify:       true,
	}
	tests := []struct {
		name      string
		operation func(Server, DisplayMessageRequest) ([]string, error)
		want      []string
	}{
		{
			name: "server",
			operation: func(server Server, request DisplayMessageRequest) ([]string, error) {
				return server.DisplayMessage(context.Background(), request)
			},
			want: []string{
				"display-message", "-p", "-a", "-v", "-l", "-N",
				"-c", "/dev/pts/9", "-d", "250", "-F", "#{window_id}:#{pane_id}",
				"#{version}",
			},
		},
		{
			name: "window",
			operation: func(server Server, request DisplayMessageRequest) ([]string, error) {
				return (Window{server: server, sessionID: "$1", windowID: "@2"}).DisplayMessage(
					context.Background(), request,
				)
			},
			want: []string{
				"display-message", "-t", "$1:0", "-p", "-a", "-v", "-l", "-N",
				"-c", "/dev/pts/9", "-d", "250", "-F", "#{window_id}:#{pane_id}",
				"#{version}",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &displayQueueRunner{responses: []displayResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
				{result: tmuxcmd.Result{Stdout: []string{"printed"}}},
			}}
			output, err := test.operation(displayServerWithRunner(runner), request)
			if err != nil {
				t.Fatalf("DisplayMessage() error = %v", err)
			}
			if !slices.Equal(output, []string{"printed"}) {
				t.Fatalf("DisplayMessage() = %#v, want printed", output)
			}
			requests := runner.recordedRequests()
			if len(requests) != 2 {
				t.Fatalf("runner requests = %#v, want one version and one display", requests)
			}
			assertDisplayArguments(t, requests[0], []string{"-V"})
			assertDisplayArguments(t, requests[1], test.want)
		})
	}
}

func TestPaneDisplayMessageAddsUpdateFlagInPythonOrder(t *testing.T) {
	t.Parallel()

	format := "#{pane_id}"
	client := ClientName("client")
	delay := 0
	runner := &displayQueueRunner{responses: []displayResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
		{result: tmuxcmd.Result{Stdout: []string{"%3"}}},
	}}
	server := displayServerWithRunner(runner)
	pane := Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"}
	output, err := pane.DisplayMessage(context.Background(), PaneDisplayMessageRequest{
		DisplayMessageRequest: DisplayMessageRequest{
			Message:      "value",
			Print:        true,
			Format:       &format,
			AllFormats:   true,
			Verbose:      true,
			NoExpand:     true,
			TargetClient: client,
			Delay:        &delay,
			Notify:       true,
		},
		UpdatePane: true,
	})
	if err != nil {
		t.Fatalf("DisplayMessage() error = %v", err)
	}
	if !slices.Equal(output, []string{"%3"}) {
		t.Fatalf("DisplayMessage() = %#v, want pane output", output)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want one version and one display", requests)
	}
	assertDisplayArguments(t, requests[0], []string{"-V"})
	assertDisplayArguments(t, requests[1], []string{
		"display-message", "-t", "$1:0.%3", "-p", "-a", "-v", "-l", "-N", "-C",
		"-c", "client", "-d", "0", "-F", "#{pane_id}", "value",
	})
}

func TestDisplayMessageZeroRequestUsesScopeAndReturnsNil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(Server) ([]string, error)
		want      []string
	}{
		{
			name: "server",
			operation: func(server Server) ([]string, error) {
				return server.DisplayMessage(context.Background(), DisplayMessageRequest{})
			},
			want: []string{"display-message"},
		},
		{
			name: "window",
			operation: func(server Server) ([]string, error) {
				return (Window{server: server, sessionID: "$1", windowID: "@2"}).DisplayMessage(
					context.Background(), DisplayMessageRequest{},
				)
			},
			want: []string{"display-message", "-t", "$1:0"},
		},
		{
			name: "pane",
			operation: func(server Server) ([]string, error) {
				return (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).DisplayMessage(
					context.Background(), PaneDisplayMessageRequest{},
				)
			},
			want: []string{"display-message", "-t", "$1:0.%3"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &displayQueueRunner{responses: []displayResponse{{
				result: tmuxcmd.Result{Stdout: []string{"ignored"}},
			}}}
			output, err := test.operation(displayServerWithRunner(runner))
			if err != nil {
				t.Fatalf("DisplayMessage() error = %v", err)
			}
			if output != nil {
				t.Fatalf("DisplayMessage() = %#v, want nil without Print", output)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 {
				t.Fatalf("runner requests = %#v, want one display", requests)
			}
			assertDisplayArguments(t, requests[0], test.want)
		})
	}
}

func TestDisplayMessageTreatsLeadingDashMessageAsData(t *testing.T) {
	t.Parallel()

	runner := &displayQueueRunner{responses: []displayResponse{{
		result: tmuxcmd.Result{Stdout: []string{"-literal"}},
	}}}
	output, err := displayServerWithRunner(runner).DisplayMessage(
		context.Background(),
		DisplayMessageRequest{Message: "-literal", Print: true},
	)
	if err != nil || !slices.Equal(output, []string{"-literal"}) {
		t.Fatalf("DisplayMessage() = (%#v, %v), want -literal", output, err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one display command", requests)
	}
	assertDisplayArguments(
		t,
		requests[0],
		[]string{"display-message", "-p", "--", "-literal"},
	)
}

func TestDisplayMessagePrintReturnsFreshNonNilStdout(t *testing.T) {
	t.Parallel()

	source := []string{"one", "two"}
	runner := &displayQueueRunner{responses: []displayResponse{
		{result: tmuxcmd.Result{Stdout: source}},
		{result: tmuxcmd.Result{}},
	}}
	server := displayServerWithRunner(runner)
	output, err := server.DisplayMessage(
		context.Background(), DisplayMessageRequest{Print: true},
	)
	if err != nil || !slices.Equal(output, []string{"one", "two"}) {
		t.Fatalf("DisplayMessage(Print) = (%#v, %v), want owned stdout", output, err)
	}
	output[0] = "changed"
	if source[0] != "one" {
		t.Fatalf("DisplayMessage(Print) aliases runner stdout: %#v", source)
	}

	empty, err := server.DisplayMessage(
		context.Background(), DisplayMessageRequest{Print: true},
	)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("DisplayMessage(empty Print) = (%#v, %v), want nonnil empty", empty, err)
	}
}

// libtmux:parity libtmux.pane.Pane.display_message#version-branch:tmux-version:1cded5d69f99
// libtmux:parity libtmux.pane.Pane.display_message#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.pane.Pane.display_message#warning:25890e6f203b
// libtmux:parity libtmux.pane.Pane.display_message#warning:26e3948d3d8c
// libtmux:parity libtmux.pane.Pane.display_message#warning:fe1b297a0f22
// libtmux:parity libtmux.window.Window.display_message#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.window.Window.display_message#warning:25890e6f203b
// libtmux:parity libtmux.window.Window.display_message#warning:fe1b297a0f22
func TestDisplayMessageVersionBoundariesWarnAndOmitUnsupportedFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		version      string
		pane         bool
		wantArgs     []string
		wantFeatures []string
		wantMinimums []string
	}{
		{
			name:         "server before no-expand",
			version:      "3.3",
			wantArgs:     []string{"display-message", "value"},
			wantFeatures: []string{"no_expand"},
			wantMinimums: []string{"3.4"},
		},
		{
			name:         "pane before both",
			version:      "3.3",
			pane:         true,
			wantArgs:     []string{"display-message", "-t", "$1:0.%3", "value"},
			wantFeatures: []string{"no_expand", "update_pane"},
			wantMinimums: []string{"3.4", "3.6"},
		},
		{
			name:         "pane at no-expand boundary",
			version:      "3.4",
			pane:         true,
			wantArgs:     []string{"display-message", "-t", "$1:0.%3", "-l", "value"},
			wantFeatures: []string{"update_pane"},
			wantMinimums: []string{"3.6"},
		},
		{
			name:     "pane at update boundary",
			version:  "3.6",
			pane:     true,
			wantArgs: []string{"display-message", "-t", "$1:0.%3", "-l", "-C", "value"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			warnings := make([]Warning, 0, 2)
			runner := &displayQueueRunner{responses: []displayResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}}},
				{result: tmuxcmd.Result{}},
			}}
			server := displayServerWithRunner(runner)
			server.connectionState().options.WarningHandler = func(warning Warning) {
				warnings = append(warnings, warning)
			}
			var err error
			if test.pane {
				_, err = (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "%3",
				}).DisplayMessage(
					context.Background(),
					PaneDisplayMessageRequest{
						DisplayMessageRequest: DisplayMessageRequest{Message: "value", NoExpand: true},
						UpdatePane:            true,
					},
				)
			} else {
				_, err = server.DisplayMessage(context.Background(), DisplayMessageRequest{
					Message: "value", NoExpand: true,
				})
			}
			if err != nil {
				t.Fatalf("DisplayMessage() error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 2 {
				t.Fatalf("runner requests = %#v, want one version and one display", requests)
			}
			assertDisplayArguments(t, requests[0], []string{"-V"})
			assertDisplayArguments(t, requests[1], test.wantArgs)
			if len(warnings) != len(test.wantFeatures) {
				t.Fatalf("warnings = %#v, want features %#v", warnings, test.wantFeatures)
			}
			for index, warning := range warnings {
				if warning.Kind != WarningUnsupportedFeature ||
					warning.Subcommand != "display-message" ||
					warning.Feature != test.wantFeatures[index] ||
					warning.CurrentVersion.String() != test.version ||
					warning.RequiredVersion.String() != test.wantMinimums[index] {
					t.Fatalf("warning %d = %#v, want %s requiring %s", index, warning, test.wantFeatures[index], test.wantMinimums[index])
				}
			}
		})
	}
}

func TestDisplayMessageCompletedStderrIsConcreteSynchronousWarning(t *testing.T) {
	t.Parallel()

	sourceStdout := []string{"partial"}
	sourceStderr := []string{"only one of -F", "argument must be given"}
	runner := &displayQueueRunner{responses: []displayResponse{{result: tmuxcmd.Result{
		Stdout: sourceStdout, Stderr: sourceStderr, ExitCode: 1,
	}}}}
	server := displayServerWithRunner(runner)
	var got Warning
	server.connectionState().options.WarningHandler = func(warning Warning) {
		if runner.callCount() != 1 {
			t.Errorf("warning delivered after %d runner calls, want completed display call", runner.callCount())
		}
		got = warning
	}
	output, err := server.DisplayMessage(
		context.Background(), DisplayMessageRequest{Message: "x", Print: true},
	)
	if err != nil || !slices.Equal(output, []string{"partial"}) {
		t.Fatalf("DisplayMessage() = (%#v, %v), want partial output and nil error", output, err)
	}
	if got.Kind != WarningCommandStderr || got.Subcommand != "display-message" ||
		got.Message != "display-message: only one of -F; argument must be given" {
		t.Fatalf("warning = %#v, want completed display stderr", got)
	}
	sourceStderr[0] = "runner mutation"
	if got.Message != "display-message: only one of -F; argument must be given" {
		t.Fatalf("warning message aliases runner stderr: %#v", got)
	}
}

func TestDisplayMessageReturnsTransportErrorsWithoutWarnings(t *testing.T) {
	t.Parallel()

	transport := errors.New("transport stopped")
	tests := []struct {
		name string
		err  error
	}{
		{name: "transport", err: transport},
		{name: "context", err: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			warnings := 0
			runner := &displayQueueRunner{responses: []displayResponse{{
				result: tmuxcmd.Result{Stderr: []string{"incomplete"}, ExitCode: -1},
				err:    test.err,
			}}}
			server := displayServerWithRunner(runner)
			server.connectionState().options.WarningHandler = func(Warning) { warnings++ }
			output, err := server.DisplayMessage(
				context.Background(), DisplayMessageRequest{Print: true},
			)
			if !errors.Is(err, test.err) || output != nil {
				t.Fatalf("DisplayMessage() = (%#v, %v), want nil and %v", output, err, test.err)
			}
			if warnings != 0 {
				t.Fatalf("warnings = %d, want none for incomplete command", warnings)
			}
		})
	}

	runner := &displayQueueRunner{responses: []displayResponse{{result: tmuxcmd.Result{
		Stdout: []string{"exit-only"}, ExitCode: 7,
	}}}}
	output, err := displayServerWithRunner(runner).DisplayMessage(
		context.Background(), DisplayMessageRequest{Print: true},
	)
	if err != nil || !slices.Equal(output, []string{"exit-only"}) {
		t.Fatalf("DisplayMessage(exit-only) = (%#v, %v), want completion data", output, err)
	}
}

func TestDisplayMessageVersionProbeFailuresStayLoud(t *testing.T) {
	t.Parallel()

	transport := errors.New("version transport failed")
	tests := []struct {
		name      string
		response  displayResponse
		wantError error
	}{
		{
			name:      "transport",
			response:  displayResponse{err: transport},
			wantError: transport,
		},
		{
			name:      "context",
			response:  displayResponse{err: context.DeadlineExceeded},
			wantError: context.DeadlineExceeded,
		},
		{
			name: "completed version failure",
			response: displayResponse{result: tmuxcmd.Result{
				Stderr: []string{"version failed"}, ExitCode: 1,
			}},
			wantError: ErrVersionQuery,
		},
		{
			name: "invalid version",
			response: displayResponse{result: tmuxcmd.Result{
				Stdout: []string{"tmux invalid!"},
			}},
			wantError: ErrVersionQuery,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			warnings := 0
			runner := &displayQueueRunner{responses: []displayResponse{test.response}}
			server := displayServerWithRunner(runner)
			server.connectionState().options.WarningHandler = func(Warning) { warnings++ }
			output, err := server.DisplayMessage(
				context.Background(), DisplayMessageRequest{Print: true, NoExpand: true},
			)
			if output != nil || !errors.Is(err, test.wantError) {
				t.Fatalf("DisplayMessage() = (%#v, %v), want nil and %v", output, err, test.wantError)
			}
			if warnings != 0 {
				t.Fatalf("warnings = %d, want none before a known version", warnings)
			}
			if runner.callCount() != 1 {
				t.Fatalf("runner calls = %d, want only version probe", runner.callCount())
			}
		})
	}
}

func TestDisplayMessageValidatesStableTargetsBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(Server) error
		want      error
	}{
		{
			name: "missing window",
			operation: func(server Server) error {
				_, err := (Window{server: server, sessionID: "$1"}).DisplayMessage(
					context.Background(), DisplayMessageRequest{NoExpand: true},
				)
				return err
			},
			want: ErrMissingTarget,
		},
		{
			name: "invalid window",
			operation: func(server Server) error {
				_, err := (Window{
					server: server, sessionID: "$1", windowID: "window",
				}).DisplayMessage(
					context.Background(), DisplayMessageRequest{NoExpand: true},
				)
				return err
			},
			want: ErrInvalidTarget,
		},
		{
			name: "missing pane",
			operation: func(server Server) error {
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@2",
				}).DisplayMessage(
					context.Background(), PaneDisplayMessageRequest{UpdatePane: true},
				)
				return err
			},
			want: ErrMissingTarget,
		},
		{
			name: "invalid pane",
			operation: func(server Server) error {
				_, err := (Pane{
					server: server, sessionID: "$1", windowID: "@2", paneID: "pane",
				}).DisplayMessage(
					context.Background(), PaneDisplayMessageRequest{UpdatePane: true},
				)
				return err
			},
			want: ErrInvalidTarget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &displayQueueRunner{}
			err := test.operation(displayServerWithRunner(runner))
			if !errors.Is(err, test.want) {
				t.Fatalf("DisplayMessage() error = %v, want %v", err, test.want)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want validation before execution", runner.callCount())
			}
		})
	}
}

func TestDisplayMessageRejectsNULBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &displayQueueRunner{}
	_, err := displayServerWithRunner(runner).DisplayMessage(
		context.Background(),
		DisplayMessageRequest{Message: "secret\x00message", NoExpand: true},
	)
	if !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("DisplayMessage() error = %v, want ErrInvalidServerCommandRequest", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("DisplayMessage() error retained message: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want validation before version probe", runner.callCount())
	}
}

func TestDisplayMessageCapturesPointerFieldsBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := newDisplayVersionGateRunner()
	server := displayServerWithRunner(runner)
	format := "before-format"
	client := ClientName("before-client")
	delay := 25
	response := make(chan error, 1)
	go func() {
		_, err := server.DisplayMessage(context.Background(), DisplayMessageRequest{
			Message:      "before-message",
			Print:        true,
			Format:       &format,
			NoExpand:     true,
			TargetClient: client,
			Delay:        &delay,
		})
		response <- err
	}()

	<-runner.versionStarted
	format = "after-format"
	delay = 50
	close(runner.releaseVersion)
	if err := <-response; err != nil {
		t.Fatalf("DisplayMessage() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("runner requests = %#v, want version and display", requests)
	}
	assertDisplayArguments(t, requests[1], []string{
		"display-message", "-p", "-l", "-c", "before-client", "-d", "25",
		"-F", "before-format", "before-message",
	})
}

type displayVersionGateRunner struct {
	versionStarted chan struct{}
	releaseVersion chan struct{}

	mu       sync.Mutex
	requests []tmuxcmd.Request
}

type displayResponse struct {
	result tmuxcmd.Result
	err    error
}

type displayQueueRunner struct {
	mu        sync.Mutex
	responses []displayResponse
	requests  []tmuxcmd.Request
}

func (r *displayQueueRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	if len(r.responses) == 0 {
		return tmuxcmd.Result{}, errors.New("unexpected display runner call")
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return response.result, response.err
}

func (r *displayQueueRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *displayQueueRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func displayServerWithRunner(runner commandRunner) Server {
	return Server{state: &serverState{runner: runner}}
}

func assertDisplayArguments(t *testing.T, request tmuxcmd.Request, want []string) {
	t.Helper()
	if !slices.Equal(request.Arguments, want) {
		t.Fatalf("runner arguments = %#v, want %#v", request.Arguments, want)
	}
}

func newDisplayVersionGateRunner() *displayVersionGateRunner {
	return &displayVersionGateRunner{
		versionStarted: make(chan struct{}),
		releaseVersion: make(chan struct{}),
	}
}

func (r *displayVersionGateRunner) Run(
	_ context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	call := len(r.requests)
	r.mu.Unlock()
	if call == 1 {
		close(r.versionStarted)
		<-r.releaseVersion
		return tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}, nil
	}
	return tmuxcmd.Result{}, nil
}

func (r *displayVersionGateRunner) recordedRequests() []tmuxcmd.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}
