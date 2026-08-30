package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// libtmux:parity libtmux.pane.Pane.choose_buffer
// libtmux:parity libtmux.pane.Pane.choose_client
// libtmux:parity libtmux.pane.Pane.clock_mode
// libtmux:parity libtmux.pane.Pane.customize_mode
func TestPaneModeCommandsUseExactPaneContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(Pane) error
		want      []string
	}{
		{name: "copy", operation: func(pane Pane) error {
			return pane.CopyMode(context.Background(), CopyModeRequest{})
		}, want: []string{"copy-mode", "-t", "$1:0.%3"}},
		{name: "clock", operation: func(pane Pane) error {
			return pane.ClockMode(context.Background())
		}, want: []string{"clock-mode", "-t", "$1:0.%3"}},
		{name: "choose buffer", operation: func(pane Pane) error {
			return pane.ChooseBuffer(context.Background())
		}, want: []string{"choose-buffer", "-t", "$1:0.%3"}},
		{name: "choose client", operation: func(pane Pane) error {
			return pane.ChooseClient(context.Background())
		}, want: []string{"choose-client", "-t", "$1:0.%3"}},
		{name: "customize", operation: func(pane Pane) error {
			return pane.CustomizeMode(context.Background())
		}, want: []string{"customize-mode", "-t", "$1:0.%3"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
			pane := Pane{
				server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
			}
			if err := test.operation(pane); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			requests := runner.recordedRequests()
			if len(requests) != 1 {
				t.Fatalf("runner requests = %#v, want one", requests)
			}
			assertRequestArguments(t, requests[0], test.want)
		})
	}
}

// libtmux:parity libtmux.pane.Pane.copy_mode
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:cancel:d480d98efb7a
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:exit_on_bottom:da23cc28e1b5
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:mouse_drag:767a4ddd3b1d
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:page_down:62888fd473ff
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:scroll_up:64208af261d8
// libtmux:parity libtmux.pane.Pane.copy_mode#parameter-branch:source_pane:423c0a9156e7
func TestCopyModeBuildsPythonFlagOrder(t *testing.T) {
	t.Parallel()

	source := PaneID("%9")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.5"}}},
		{result: tmuxcmd.Result{}},
	}}
	pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}
	err := pane.CopyMode(context.Background(), CopyModeRequest{
		ScrollUp: true, ExitOnBottom: true, MouseDrag: true, PageDown: true,
		SourcePane: source, Cancel: true,
	})
	if err != nil {
		t.Fatalf("CopyMode() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{"-V"})
	assertRequestArguments(t, requests[1], []string{
		"copy-mode", "-t", "$1:0.%3", "-u", "-e", "-M", "-d", "-s", "%9", "-q",
	})
}

// libtmux:parity libtmux.pane.Pane.copy_mode#version-branch:tmux-version:820df7664d30
// libtmux:parity libtmux.pane.Pane.copy_mode#warning:07225fecc4d2
func TestCopyModeWarnsAndOmitsPageDownBefore35(t *testing.T) {
	t.Parallel()

	warnings := make([]Warning, 0, 1)
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.4"}}},
		{result: tmuxcmd.Result{}},
	}}
	server := serverWithOptionsAndRunner(ServerOptions{
		Unsupported: DegradeUnsupported,
		WarningHandler: func(warning Warning) {
			warnings = append(warnings, warning)
		},
	}, runner)
	pane := Pane{server: server, sessionID: "$1", windowID: "@2", paneID: "%3"}
	if err := pane.CopyMode(context.Background(), CopyModeRequest{PageDown: true}); err != nil {
		t.Fatalf("CopyMode() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[1], []string{"copy-mode", "-t", "$1:0.%3"})
	if len(warnings) != 1 || warnings[0].Feature != "page_down" ||
		warnings[0].RequiredVersion.String() != "3.5" {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestCopyModeZeroSourceOmitsFlagBeforePageDownVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{}},
	}}
	pane := Pane{
		server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
	}
	err := pane.CopyMode(context.Background(), CopyModeRequest{
		PageDown: true, Cancel: true,
	})
	if err != nil {
		t.Fatalf("CopyMode() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[1], []string{
		"copy-mode", "-t", "$1:0.%3", "-d", "-q",
	})
}

func TestCopyModeValidatesExactPaneBeforeVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	pane := Pane{server: serverWithRunner(runner), paneID: "%3"}
	err := pane.CopyMode(context.Background(), CopyModeRequest{PageDown: true})
	if !errors.Is(err, ErrMissingTarget) {
		t.Fatalf("CopyMode() error = %v, want ErrMissingTarget", err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("runner calls = %d, want 0", calls)
	}
}

// libtmux:parity libtmux.pane.Pane.choose_tree
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:filter_expression:741088570e6e
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:format_string:7284ef554e76
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:reverse:72696e193812
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:sessions_collapsed:dc36af30b6c2
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:sort_order:bc74294d9429
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:windows_collapsed:063263fb151d
// libtmux:parity libtmux.pane.Pane.choose_tree#parameter-branch:zoom:629cd868ae3d
func TestChooseTreeBuildsTypedSortAndLiteralFields(t *testing.T) {
	t.Parallel()

	format := "#{session_name};"
	filter := TmuxFilter("#{session_attached}")
	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}
	err := pane.ChooseTree(context.Background(), ChooseTreeRequest{
		SessionsCollapsed: true,
		WindowsCollapsed:  true,
		Format:            &format,
		Filter:            &filter,
		Sort:              TreeSortName,
		Reverse:           true,
		Zoom:              true,
	})
	if err != nil {
		t.Fatalf("ChooseTree() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{
		"choose-tree", "-t", "$1:0.%3", "-s", "-w", "-Z", "-r",
		"-F", `#{session_name}\;`, "-f", "#{session_attached}", "-O", "name",
	})
}

// libtmux:parity libtmux.pane.Pane.display_panes
// libtmux:parity libtmux.pane.Pane.display_panes#parameter-branch:duration:4675d6e179b9
// libtmux:parity libtmux.pane.Pane.display_panes#parameter-branch:no_select:8c2e5e8989eb
// libtmux:parity libtmux.pane.Pane.find_window
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:case_insensitive:cf484fe9e540
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:match_content:d19faeac104a
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:match_name:d259fbe84150
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:match_title:c066c53a42c5
// libtmux:parity libtmux.pane.Pane.find_window#parameter-branch:regex:eb6f0b4408a2
func TestFindWindowAndDisplayPanesBuildDistinctTargetShapes(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{}},
		{result: tmuxcmd.Result{}},
	}}
	pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}
	if err := pane.FindWindow(context.Background(), FindWindowRequest{
		Match: "needle;", MatchContent: true, CaseInsensitive: true,
		MatchName: true, Regex: true, MatchTitle: true,
	}); err != nil {
		t.Fatalf("FindWindow() error = %v", err)
	}
	duration := 250
	if err := pane.DisplayPanes(
		context.Background(), DisplayPanesRequest{Duration: &duration, NoSelect: true},
	); err != nil {
		t.Fatalf("DisplayPanes() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{
		"find-window", "-t", "$1:0.%3", "-C", "-i", "-N", "-r", "-T", "--", `needle\;`,
	})
	assertRequestArguments(t, requests[1], []string{"display-panes", "-d", "250", "-N"})
}

func TestFindWindowProtectsLeadingDashMatch(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	pane := Pane{
		server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
	}
	if err := pane.FindWindow(
		context.Background(), FindWindowRequest{Match: "-needle"},
	); err != nil {
		t.Fatalf("FindWindow() error = %v", err)
	}
	requests := runner.recordedRequests()
	assertRequestArguments(t, requests[0], []string{
		"find-window", "-t", "$1:0.%3", "--", "-needle",
	})
}

func TestPaneModesRejectInvalidRequestsBeforeExecution(t *testing.T) {
	t.Parallel()

	nul := "unsafe\x00value"
	negative := -1
	tests := []struct {
		name      string
		operation func(Pane) error
	}{
		{name: "unknown sort", operation: func(pane Pane) error {
			return pane.ChooseTree(context.Background(), ChooseTreeRequest{Sort: TreeSortOrder(99)})
		}},
		{name: "NUL format", operation: func(pane Pane) error {
			return pane.ChooseTree(context.Background(), ChooseTreeRequest{Format: &nul})
		}},
		{name: "NUL match", operation: func(pane Pane) error {
			return pane.FindWindow(context.Background(), FindWindowRequest{Match: nul})
		}},
		{name: "negative duration", operation: func(pane Pane) error {
			return pane.DisplayPanes(context.Background(), DisplayPanesRequest{Duration: &negative})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{}
			pane := Pane{
				server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
			}
			err := test.operation(pane)
			if !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("operation error = %v, want ErrInvalidServerCommandRequest", err)
			}
			if calls := runner.callCount(); calls != 0 {
				t.Fatalf("runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestPaneModesReturnRedactedCommandErrorsForCompletedStderr(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
		Stderr: []string{"mode failed"}, ExitCode: 1,
	}}}}
	pane := Pane{server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3"}
	err := pane.ClockMode(context.Background())
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Subcommand != "clock-mode" ||
		commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
		commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
		t.Fatalf("ClockMode() error = %#v, want clock-mode CommandError", err)
	}
}

func TestDocumentedPaneModeFailuresRedactPayloads(t *testing.T) {
	t.Parallel()

	const secret = "pane-mode-secret"
	format := secret
	filter := TmuxFilter(secret)
	tests := []struct {
		name       string
		subcommand string
		invoke     func(Pane) error
	}{
		{name: "copy mode", subcommand: "copy-mode", invoke: func(pane Pane) error {
			return pane.CopyMode(context.Background(), CopyModeRequest{})
		}},
		{name: "clock mode", subcommand: "clock-mode", invoke: func(pane Pane) error {
			return pane.ClockMode(context.Background())
		}},
		{name: "choose buffer", subcommand: "choose-buffer", invoke: func(pane Pane) error {
			return pane.ChooseBuffer(context.Background())
		}},
		{name: "choose client", subcommand: "choose-client", invoke: func(pane Pane) error {
			return pane.ChooseClient(context.Background())
		}},
		{name: "customize mode", subcommand: "customize-mode", invoke: func(pane Pane) error {
			return pane.CustomizeMode(context.Background())
		}},
		{name: "choose tree", subcommand: "choose-tree", invoke: func(pane Pane) error {
			return pane.ChooseTree(context.Background(), ChooseTreeRequest{
				Format: &format, Filter: &filter,
			})
		}},
		{name: "find window", subcommand: "find-window", invoke: func(pane Pane) error {
			return pane.FindWindow(context.Background(), FindWindowRequest{Match: secret})
		}},
		{name: "display panes", subcommand: "display-panes", invoke: func(pane Pane) error {
			return pane.DisplayPanes(context.Background(), DisplayPanesRequest{})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{
				Command:  []string{"tmux", test.subcommand, secret},
				Stdout:   []string{"stdout " + secret},
				Stderr:   []string{"stderr " + secret},
				ExitCode: 7,
			}}}}
			pane := Pane{
				server: serverWithRunner(runner), sessionID: "$1", windowID: "@2", paneID: "%3",
			}
			err := test.invoke(pane)
			assertExitOnlyCommandErrorRedacts(t, err, test.subcommand, 7, secret)
		})
	}
}
