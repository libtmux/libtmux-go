package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.pane.Pane.display_popup
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:border_lines:d9be2600ac4e
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:border_style:4d3b7eb6c12c
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_existing:b874c97fbb98
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_any_key:512abb2889cb
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_exit,close_on_success:7fe7f6d507f5
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_exit:8ae43bb440be
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:close_on_success:ba40729359dc
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:command:581de879aee9
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:environment:88c271e9ea0f
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:height:584748e889a5
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:no_border:f3385c3cc820
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:no_keys:5ad750b56292
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:start_directory:d91549582997
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:style:2fb8c408bf6c
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:title:a849ce4d4991
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:width:c4a3db243018
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:x:c2891f2208b1
// libtmux:parity libtmux.pane.Pane.display_popup#parameter-branch:y:0cf048966732
func TestDisplayPopupBuildsPythonFlagOrderAndExactTarget(t *testing.T) {
	t.Parallel()

	command := "printf secret;"
	client := ClientName("client-a")
	width, height := "40", "50%"
	x, y := "C", "P"
	directory := "/tmp"
	title := "title"
	borderLines := "single"
	style := "bg=blue"
	borderStyle := "fg=red"
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.6"}}},
		{result: tmuxcmd.Result{}},
	}}
	pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}
	err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{
		Command:        &command,
		CloseOnSuccess: true,
		CloseExisting:  true,
		TargetClient:   client,
		Width:          &width,
		Height:         &height,
		X:              &x,
		Y:              &y,
		StartDirectory: &directory,
		Title:          &title,
		BorderLines:    &borderLines,
		Style:          &style,
		BorderStyle:    &borderStyle,
		Environment:    map[string]string{"ZED": "last", "ALPHA": "first"},
		NoBorder:       true,
		CloseOnAnyKey:  true,
		NoKeys:         true,
	})
	if err != nil {
		t.Fatalf("DisplayPopup() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"display-popup", "-t", "$1:0.%3", "-C", "-c", "client-a", "-E", "-E",
		"-w", "40", "-h", "50%", "-x", "C", "-y", "P", "-d", "/tmp",
		"-T", "title", "-b", "single", "-s", "bg=blue", "-S", "fg=red",
		"-eALPHA=first", "-eZED=last", "-B", "-k", "-N", `printf secret\;`,
	})
}

// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:1cded5d69f99
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:1cded5d69f99:2
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:2
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:3
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:4
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:5
// libtmux:parity libtmux.pane.Pane.display_popup#version-branch:tmux-version:4e983827f5ca:6
// libtmux:parity libtmux.pane.Pane.display_popup#warning:02db17883dc3
// libtmux:parity libtmux.pane.Pane.display_popup#warning:04e24fc19aae
// libtmux:parity libtmux.pane.Pane.display_popup#warning:435d237695c4
// libtmux:parity libtmux.pane.Pane.display_popup#warning:7203e98175ed
// libtmux:parity libtmux.pane.Pane.display_popup#warning:8ae1ebc29718
// libtmux:parity libtmux.pane.Pane.display_popup#warning:b5ab51d893a0
// libtmux:parity libtmux.pane.Pane.display_popup#warning:c0c571ede24a
// libtmux:parity libtmux.pane.Pane.display_popup#warning:dd8c2a6933f5
func TestDisplayPopupWarnsAndOmitsUnsupportedFields(t *testing.T) {
	t.Parallel()

	title := "title"
	borderLines := "single"
	style := "style"
	borderStyle := "border"
	warnings := make([]Warning, 0, 8)
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}}},
		{result: tmuxcmd.Result{}},
	}}
	server := serverWithRunner(runner)
	server.connectionState().options.WarningHandler = func(warning Warning) {
		warnings = append(warnings, warning)
	}
	pane := Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"}
	err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{
		Title: &title, BorderLines: &borderLines, Style: &style, BorderStyle: &borderStyle,
		Environment: map[string]string{"KEY": "value"}, NoBorder: true,
		CloseOnAnyKey: true, NoKeys: true,
	})
	if err != nil {
		t.Fatalf("DisplayPopup() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[1], []string{"display-popup", "-t", "$1:0.%3"})
	wantFeatures := []string{
		"title", "border_lines", "style", "border_style", "environment", "no_border",
		"close_on_any_key", "no_keys",
	}
	if len(warnings) != len(wantFeatures) {
		t.Fatalf("warnings = %#v, want features %#v", warnings, wantFeatures)
	}
	for index, warning := range warnings {
		if warning.Feature != wantFeatures[index] || warning.Subcommand != "display-popup" {
			t.Fatalf("warning %d = %#v", index, warning)
		}
	}
}

func TestDisplayPopupZeroRequestSkipsVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}
	if err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{}); err != nil {
		t.Fatalf("DisplayPopup() error = %v", err)
	}
	requests := runner.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("runner requests = %#v, want one", requests)
	}
	assertRequestArguments(t, requests[0], []string{"display-popup", "-t", "$1:0.%3"})
}

func TestDisplayPopupCapturesPointerAndMapValuesBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := newDisplayVersionGateRunner()
	command := "before-command"
	client := ClientName("before-client")
	directory := "/before-directory"
	title := "before-title"
	environment := map[string]string{"KEY": "before-value"}
	pane := Pane{
		server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
	}
	response := make(chan error, 1)
	go func() {
		response <- pane.DisplayPopup(context.Background(), DisplayPopupRequest{
			Command: &command, TargetClient: client, StartDirectory: &directory,
			Title: &title, Environment: environment,
		})
	}()

	<-runner.versionStarted
	command = "after-command"
	directory = "/after-directory"
	title = "after-title"
	environment["KEY"] = "after-value"
	close(runner.releaseVersion)
	if err := <-response; err != nil {
		t.Fatalf("DisplayPopup() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[1], []string{
		"display-popup", "-t", "$1:0.%3", "-c", "before-client",
		"-d", "/before-directory", "-T", "before-title", "-eKEY=before-value",
		"before-command",
	})
}

func TestDisplayPopupValidatesExactPaneBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	title := "title"
	runner := &versionQueueRunner{}
	pane := Pane{server: serverWithRunner(runner), paneID: "%3"}
	err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{Title: &title})
	if !errors.Is(err, ErrMissingTarget) {
		t.Fatalf("DisplayPopup() error = %v, want ErrMissingTarget", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("runner calls = %d, want 0", calls)
	}
}

func TestDisplayPopupRejectsInvalidRequestsBeforeExecution(t *testing.T) {
	t.Parallel()

	nul := "unsafe\x00value"
	tests := []struct {
		name    string
		request DisplayPopupRequest
	}{
		{name: "mutually exclusive close modes", request: DisplayPopupRequest{
			CloseOnExit: true, CloseOnSuccess: true,
		}},
		{name: "NUL command before version", request: DisplayPopupRequest{
			Command: &nul, Title: &nul,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			pane := Pane{
				server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
			}
			err := pane.DisplayPopup(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidServerCommandRequest) && !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("DisplayPopup() error = %v, want request or target error", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestDisplayPopupReturnsCompletedStderrAsCommandError(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"popup failed"}, ExitCode: 1,
	}}}}
	pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}
	err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{})
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Subcommand != "display-popup" ||
		commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
		t.Fatalf("DisplayPopup() error = %#v, want display-popup CommandError", err)
	}
}
