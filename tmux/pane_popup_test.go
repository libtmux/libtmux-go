package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

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
		"display-popup", "-t", "$1:.%3", "-C", "-c", "client-a", "-E", "-E",
		"-w", "40", "-h", "50%", "-x", "C", "-y", "P", "-d", "/tmp",
		"-T", "title", "-b", "single", "-s", "bg=blue", "-S", "fg=red",
		"-eALPHA=first", "-eZED=last", "-B", "-k", "-N", "--", `printf secret\;`,
	})
}

func TestDisplayPopupWarnsAndOmitsUnsupportedFields(t *testing.T) {
	t.Parallel()

	title := "title"
	borderLines := "single"
	style := "style"
	borderStyle := "border"
	warnings := make([]Warning, 0, 8)
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3a"}}},
		{result: tmuxcmd.Result{}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported: DegradeUnsupported,
		WarningHandler: func(warning Warning) {
			warnings = append(warnings, warning)
		},
	}, runner)
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
	assertRequestArguments(t, requests[1], []string{
		"display-popup", "-t", "$1:.%3", "-T", "title", "-b", "single", "-s", "style",
		"-S", "border", "-eKEY=value", "-B",
	})
	wantFeatures := []string{"close_on_any_key", "no_keys"}
	if len(warnings) != len(wantFeatures) {
		t.Fatalf("warnings = %#v, want features %#v", warnings, wantFeatures)
	}
	for index, warning := range warnings {
		if warning.Feature != wantFeatures[index] || warning.Subcommand != "display-popup" {
			t.Fatalf("warning %d = %#v", index, warning)
		}
	}
}

// -N exists from 3.6 and is gone from 3.9, where a popup is a floating pane
// with no earlier popup to modify; the flag is sent between and omitted
// without a warning after.
func TestDisplayPopupSendsNoKeysOnlyWhereTmuxOffersIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version  string
		wantFlag bool
	}{
		{"3.6", true}, {"3.8", true}, {"next-3.9", false}, {"3.9", false},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()

			var warnings []Warning
			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux " + test.version}}},
				{result: tmuxcmd.Result{}},
			}}
			server := serverWithOptionsAndRunner(ServerOptions{
				WarningHandler: func(warning Warning) { warnings = append(warnings, warning) },
			}, runner)
			pane := Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"}
			if err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{NoKeys: true}); err != nil {
				t.Fatal(err)
			}
			requests := runner.recordedRequests()
			if got := slices.Contains(requests[1].Arguments, "-N"); got != test.wantFlag {
				t.Fatalf("display-popup argv = %#v, want -N sent = %t", requests[1].Arguments, test.wantFlag)
			}
			if len(warnings) != 0 {
				t.Fatalf("warnings = %#v, want none", warnings)
			}
		})
	}
}

// tmux 3.8 refuses to host a popup on a control-mode target client, but
// answers exactly as it does on success (CMD_RETURN_NORMAL either way), so a
// caller who only checks the error would never learn the popup never ran.
// DisplayPopup must catch this itself, and only for a target it can actually
// confirm is control-mode - not for one it cannot resolve, and not on a
// version too old for the refusal to exist.
func TestDisplayPopupRejectsAControlModeTargetOnlyOnCapableVersions(t *testing.T) {
	t.Parallel()

	version38 := mustParseVersion(t, "3.8")
	clientFields, err := formatFieldsFor("list-clients", version38)
	if err != nil {
		t.Fatal(err)
	}
	clientLookupResponses := func(controlMode string) []versionResponse {
		return []versionResponse{
			liveIdentityResponse(version38),
			{result: tmuxcmd.Result{
				RawStdout: framedSnapshotRecord(clientFields, snapshotRowValues(version38, map[string]string{
					"client_name": "control-a", "client_control_mode": controlMode,
				})),
				ExitCode: 0,
			}},
			liveIdentityResponse(version38),
		}
	}

	t.Run("control-mode target is rejected before any display-popup command", func(t *testing.T) {
		t.Parallel()

		responses := []versionResponse{{result: tmuxcmd.Result{Stdout: []string{"tmux 3.8"}}}}
		responses = append(responses, clientLookupResponses("1")...)
		runner := &versionQueueRunner{responses: responses}
		pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}

		err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{
			TargetClient: ClientName("control-a"),
		})
		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("DisplayPopup() error = %v, want ErrInvalidServerCommandRequest", err)
		}
		if calls := runner.callCount(); calls != len(responses) {
			t.Fatalf("runner calls = %d, want %d (no display-popup sent)", calls, len(responses))
		}
	})

	t.Run("non-control-mode target still reaches tmux", func(t *testing.T) {
		t.Parallel()

		responses := []versionResponse{{result: tmuxcmd.Result{Stdout: []string{"tmux 3.8"}}}}
		responses = append(responses, clientLookupResponses("0")...)
		responses = append(responses, versionResponse{
			result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7},
		})
		runner := &versionQueueRunner{responses: responses}
		pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}

		err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{
			TargetClient: ClientName("control-a"),
		})
		if !errors.Is(err, ErrCommand) {
			t.Fatalf("DisplayPopup() error = %v, want ErrCommand (the display-popup command itself)", err)
		}
		requests := runner.recordedRequests()
		if len(requests) != len(responses) {
			t.Fatalf("request count = %d, want %d", len(requests), len(responses))
		}
		assertRequestArguments(t, requests[len(requests)-1], []string{
			"display-popup", "-t", "$1:.%3", "-c", "control-a",
		})
	})

	t.Run("a target lookup that fails falls through to the request", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.8"}}},
			{err: errors.New("transport failed")},
			{result: tmuxcmd.Result{Stderr: []string{"stop after argv capture"}, ExitCode: 7}},
		}}
		pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}

		err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{
			TargetClient: ClientName("unresolvable"),
		})
		if !errors.Is(err, ErrCommand) {
			t.Fatalf("DisplayPopup() error = %v, want ErrCommand", err)
		}
	})

	t.Run("below tmux 3.8 the target is never checked", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7c"}}},
			{result: tmuxcmd.Result{}},
		}}
		pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}

		err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{
			TargetClient: ClientName("control-a"),
		})
		if err != nil {
			t.Fatalf("DisplayPopup() error = %v", err)
		}
		if calls := runner.callCount(); calls != 2 {
			t.Fatalf("runner calls = %d, want 2 (version probe, then display-popup - no client lookup)", calls)
		}
	})

	t.Run("CloseExisting is exempt on any version", func(t *testing.T) {
		t.Parallel()

		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.8"}}},
			{result: tmuxcmd.Result{}},
		}}
		pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}

		err := pane.DisplayPopup(context.Background(), DisplayPopupRequest{
			TargetClient:  ClientName("control-a"),
			CloseExisting: true,
		})
		if err != nil {
			t.Fatalf("DisplayPopup() error = %v", err)
		}
		if calls := runner.callCount(); calls != 2 {
			t.Fatalf("runner calls = %d, want 2 (version probe, then display-popup - no client lookup)", calls)
		}
	})
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
	assertRequestArguments(t, requests[0], []string{"display-popup", "-t", "$1:.%3"})
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
		"display-popup", "-t", "$1:.%3", "-c", "before-client",
		"-d", "/before-directory", "-T", "before-title", "-eKEY=before-value",
		"--", "before-command",
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

// The popup guard opens at 3.8: 3.7c is below it, and 3.8 is at it. 3.8a is
// only an ordering sample, not a release.
func TestPopupGuardOpensAtThreeEight(t *testing.T) {
	t.Parallel()

	if got := panePopupVersion38.String(); got != "3.8" {
		t.Errorf("popup guard floor = %s, want 3.8", got)
	}
	for raw, want := range map[string]bool{"3.7c": false, "3.8": true, "3.8a": true} {
		version := mustParseVersion(t, raw)
		if got := version.AtLeast(panePopupVersion38); got != want {
			t.Errorf("%s reaches the popup guard = %t, want %t", raw, got, want)
		}
	}
}
