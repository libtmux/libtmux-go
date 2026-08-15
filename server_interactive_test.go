package tmux

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

func TestConfirmBeforeBuildsExactArguments(t *testing.T) {
	t.Parallel()

	prompt := "Continue?"
	key := "a"
	client := ClientName("/dev/pts/9")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{}},
	}}
	err := serverWithRunner(runner).ConfirmBefore(context.Background(), ConfirmBeforeRequest{
		Command:      "set -g @confirmed yes",
		Prompt:       &prompt,
		ConfirmKey:   &key,
		DefaultYes:   true,
		TargetClient: client,
	})
	if err != nil {
		t.Fatalf("ConfirmBefore() error = %v", err)
	}
	assertInteractiveRequests(t, runner, [][]string{
		{"-V"},
		{
			"confirm-before", "-b", "-p", "Continue?", "-c", "a", "-y",
			"-t", "/dev/pts/9", "set -g @confirmed yes",
		},
	})
}

func TestCommandPromptBuildsExactArguments(t *testing.T) {
	t.Parallel()

	prompt := "Name:"
	inputs := "initial"
	client := ClientName("client")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{}},
	}}
	err := serverWithRunner(runner).CommandPrompt(context.Background(), CommandPromptRequest{
		Template:      "set -g @value '%1'",
		Prompt:        &prompt,
		Inputs:        &inputs,
		TargetClient:  client,
		OneKey:        true,
		KeyOnly:       true,
		OnInputChange: true,
		Numeric:       true,
		Type:          PromptTypeWindowTarget,
		ExpandFormat:  true,
		Literal:       true,
		BackspaceExit: true,
		NoFreeze:      true,
	})
	if err != nil {
		t.Fatalf("CommandPrompt() error = %v", err)
	}
	assertInteractiveRequests(t, runner, [][]string{
		{"-V"},
		{
			"command-prompt", "-b", "-1", "-k", "-i", "-N", "-F", "-l",
			"-e", "-C", "-p", "Name:", "-I", "initial", "-T",
			"window-target", "-t", "client", "set -g @value '%1'",
		},
	})
}

// libtmux:parity libtmux.server.Server.display_menu
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:border_lines:d9be2600ac4e
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:border_style:4d3b7eb6c12c
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:mouse:61c1f4bb05bb
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:selected_style:8d9e2e2259ea
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:starting_choice:47b908259302
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:stay_open:9abb4fe49285
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:style:2fb8c408bf6c
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:target_pane:5f9e4a0df2ff
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:title:a849ce4d4991
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:x:c2891f2208b1
// libtmux:parity libtmux.server.Server.display_menu#parameter-branch:y:0cf048966732
// libtmux:parity libtmux.server.Server.display_menu#version-branch:tmux-version:5bb2ac269d05
// libtmux:parity libtmux.server.Server.display_menu#version-branch:tmux-version:5bb2ac269d05:2
// libtmux:parity libtmux.server.Server.display_menu#version-branch:tmux-version:5bb2ac269d05:3
// libtmux:parity libtmux.server.Server.display_menu#version-branch:tmux-version:5bb2ac269d05:4
// libtmux:parity libtmux.server.Server.display_menu#version-branch:tmux-version:5bb2ac269d05:5
// libtmux:parity libtmux.server.Server.display_menu#version-branch:tmux-version:82c3b9df745c
// libtmux:parity libtmux.server.Server.display_menu#warning:2951ff2002a9
// libtmux:parity libtmux.server.Server.display_menu#warning:2953a2cb595a
// libtmux:parity libtmux.server.Server.display_menu#warning:79333b75bfbe
// libtmux:parity libtmux.server.Server.display_menu#warning:a35dc72d82e4
// libtmux:parity libtmux.server.Server.display_menu#warning:bee653a17e16
// libtmux:parity libtmux.server.Server.display_menu#warning:e62af5989ba6
// libtmux:parity libtmux.server.Server.start_server
func TestDisplayMenuBuildsTypedItemArguments(t *testing.T) {
	t.Parallel()

	title := "Actions"
	x := "C"
	y := "P"
	choice := "1"
	borderLines := "single"
	style := "bg=blue"
	borderStyle := "fg=red"
	selectedStyle := "bg=yellow"
	pane := PaneID("%3")
	client := ClientName("/dev/pts/8")
	runner := &versionQueueRunner{responses: []versionResponse{
		{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
		{result: tmuxcmd.Result{}},
	}}
	err := serverWithRunner(runner).DisplayMenu(context.Background(), DisplayMenuRequest{
		Items: []MenuItem{
			{Name: "First", Key: "1", Command: "select-pane -t %1"},
			{},
			{Name: "Second", Key: "2", Command: "select-pane -t %2"},
		},
		Title:          &title,
		TargetPane:     pane,
		TargetClient:   client,
		X:              &x,
		Y:              &y,
		StartingChoice: &choice,
		BorderLines:    &borderLines,
		Style:          &style,
		BorderStyle:    &borderStyle,
		SelectedStyle:  &selectedStyle,
		Mouse:          true,
		StayOpen:       true,
	})
	if err != nil {
		t.Fatalf("DisplayMenu() error = %v", err)
	}
	assertInteractiveRequests(t, runner, [][]string{
		{"-V"},
		{
			"display-menu", "-T", "Actions", "-c", "/dev/pts/8", "-t", "%3",
			"-x", "C", "-y", "P", "-C", "1", "-b", "single", "-s",
			"bg=blue", "-S", "fg=red", "-H", "bg=yellow", "-M", "-O",
			"First", "1", "select-pane -t %1", "",
			"Second", "2", "select-pane -t %2",
		},
	})
}

func TestStartServerUsesNoVersionProbe(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{responses: []versionResponse{{result: tmuxcmd.Result{}}}}
	if err := serverWithRunner(runner).Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assertInteractiveRequests(t, runner, [][]string{{"start-server"}})
}

// libtmux:parity libtmux.server.Server.command_prompt
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:bspace_exit:7238f0865c95
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:expand_format:3634369442f2
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:inputs:b6499bc9fd6d
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:key_only:c28e76c495c6
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:literal:399069e0cc76
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:no_freeze:85604e1972c7
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:numeric:fcc881c8bb5c
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:on_input_change:d0d5f3ef21cf
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:one_key:2835968062ff
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:prompt:16b451056c1b
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:prompt_type:83546fba116e
// libtmux:parity libtmux.server.Server.command_prompt#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.command_prompt#version-branch:tmux-version:157b9dba160f
// libtmux:parity libtmux.server.Server.command_prompt#version-branch:tmux-version:157b9dba160f:2
// libtmux:parity libtmux.server.Server.command_prompt#version-branch:tmux-version:161023f2e486
// libtmux:parity libtmux.server.Server.command_prompt#version-branch:tmux-version:d9801479e597
// libtmux:parity libtmux.server.Server.command_prompt#warning:37815d3900a7
// libtmux:parity libtmux.server.Server.command_prompt#warning:83696e92476d
// libtmux:parity libtmux.server.Server.command_prompt#warning:a1188bba9cd7
// libtmux:parity libtmux.server.Server.confirm_before
// libtmux:parity libtmux.server.Server.confirm_before#parameter-branch:confirm_key:a51acfa2582f
// libtmux:parity libtmux.server.Server.confirm_before#parameter-branch:default_yes:a54c1881b74f
// libtmux:parity libtmux.server.Server.confirm_before#parameter-branch:prompt:16b451056c1b
// libtmux:parity libtmux.server.Server.confirm_before#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.confirm_before#version-branch:tmux-version:5bb2ac269d05
// libtmux:parity libtmux.server.Server.confirm_before#version-branch:tmux-version:5bb2ac269d05:2
// libtmux:parity libtmux.server.Server.confirm_before#version-branch:tmux-version:d9801479e597
// libtmux:parity libtmux.server.Server.confirm_before#warning:931e3a7f3f33
// libtmux:parity libtmux.server.Server.confirm_before#warning:fd405b4a80ca
func TestInteractiveVersionBoundariesWarnAndOmit(t *testing.T) {
	t.Parallel()

	t.Run("confirm-before hard floor", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{responses: []versionResponse{{
			result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}},
		}}}
		err := serverWithRunner(runner).ConfirmBefore(
			context.Background(), ConfirmBeforeRequest{Command: "display-message no"},
		)
		var tooLow *VersionTooLowError
		if !errors.As(err, &tooLow) || tooLow.Current.String() != "3.2a" ||
			tooLow.Minimum.String() != "3.3" {
			t.Fatalf("ConfirmBefore() error = %#v, want tmux 3.3 floor", err)
		}
		assertInteractiveRequests(t, runner, [][]string{{"-V"}})
	})

	t.Run("confirm-before 3.4 flags", func(t *testing.T) {
		t.Parallel()
		key := "a"
		warnings := make([]Warning, 0, 2)
		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3a"}}},
			{result: tmuxcmd.Result{}},
		}}
		server := serverWithRunner(runner)
		server.connectionState().options.WarningHandler = func(warning Warning) {
			warnings = append(warnings, warning)
		}
		err := server.ConfirmBefore(context.Background(), ConfirmBeforeRequest{
			Command: "display-message ok", ConfirmKey: &key, DefaultYes: true,
		})
		if err != nil {
			t.Fatalf("ConfirmBefore() error = %v", err)
		}
		assertWarningFeatures(t, warnings, "confirm_key", "default_yes")
		assertInteractiveRequests(t, runner, [][]string{
			{"-V"}, {"confirm-before", "-b", "display-message ok"},
		})
	})

	t.Run("command-prompt later flags", func(t *testing.T) {
		t.Parallel()
		warnings := make([]Warning, 0, 3)
		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.3a"}}},
			{result: tmuxcmd.Result{}},
		}}
		server := serverWithRunner(runner)
		server.connectionState().options.WarningHandler = func(warning Warning) {
			warnings = append(warnings, warning)
		}
		err := server.CommandPrompt(context.Background(), CommandPromptRequest{
			Template:      "display-message %1",
			ExpandFormat:  true,
			Literal:       true,
			BackspaceExit: true,
			NoFreeze:      true,
		})
		if err != nil {
			t.Fatalf("CommandPrompt() error = %v", err)
		}
		assertWarningFeatures(t, warnings, "literal", "bspace_exit", "no_freeze")
		assertInteractiveRequests(t, runner, [][]string{
			{"-V"}, {"command-prompt", "-b", "-F", "display-message %1"},
		})
	})

	t.Run("display-menu later flags", func(t *testing.T) {
		t.Parallel()
		choice := "0"
		lines := "single"
		style := "default"
		border := "default"
		selected := "reverse"
		warnings := make([]Warning, 0, 6)
		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.2a"}}},
			{result: tmuxcmd.Result{}},
		}}
		server := serverWithRunner(runner)
		server.connectionState().options.WarningHandler = func(warning Warning) {
			warnings = append(warnings, warning)
		}
		err := server.DisplayMenu(context.Background(), DisplayMenuRequest{
			Items:          []MenuItem{{Name: "One", Key: "1", Command: "select-pane"}},
			StartingChoice: &choice,
			BorderLines:    &lines,
			Style:          &style,
			BorderStyle:    &border,
			SelectedStyle:  &selected,
			Mouse:          true,
			StayOpen:       true,
		})
		if err != nil {
			t.Fatalf("DisplayMenu() error = %v", err)
		}
		assertWarningFeatures(
			t, warnings, "starting_choice", "border_lines", "style",
			"border_style", "selected_style", "mouse",
		)
		assertInteractiveRequests(t, runner, [][]string{
			{"-V"}, {"display-menu", "-O", "One", "1", "select-pane"},
		})
	})
}

func TestInteractiveRequestsValidateBeforeVersionOrExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation func(Server) error
	}{
		{
			name: "empty confirm command",
			operation: func(server Server) error {
				return server.ConfirmBefore(context.Background(), ConfirmBeforeRequest{})
			},
		},
		{
			name: "invalid confirm key",
			operation: func(server Server) error {
				key := "Enter"
				return server.ConfirmBefore(context.Background(), ConfirmBeforeRequest{
					Command: "display-message ok", ConfirmKey: &key,
				})
			},
		},
		{
			name: "empty command prompt template",
			operation: func(server Server) error {
				return server.CommandPrompt(context.Background(), CommandPromptRequest{})
			},
		},
		{
			name: "unsupported prompt type",
			operation: func(server Server) error {
				return server.CommandPrompt(context.Background(), CommandPromptRequest{
					Template: "display-message %1", Type: PromptType(255),
				})
			},
		},
		{
			name: "NUL confirm target before version",
			operation: func(server Server) error {
				client := ClientName("secret-client\x00payload")
				return server.ConfirmBefore(context.Background(), ConfirmBeforeRequest{
					Command: "display-message ok", TargetClient: client,
				})
			},
		},
		{
			name: "NUL prompt target before version",
			operation: func(server Server) error {
				client := ClientName("secret-client\x00payload")
				return server.CommandPrompt(context.Background(), CommandPromptRequest{
					Template: "display-message %1", TargetClient: client,
				})
			},
		},
		{
			name: "menu requires entries",
			operation: func(server Server) error {
				return server.DisplayMenu(context.Background(), DisplayMenuRequest{})
			},
		},
		{
			name: "partial separator",
			operation: func(server Server) error {
				return server.DisplayMenu(context.Background(), DisplayMenuRequest{
					Items: []MenuItem{{Key: "1"}},
				})
			},
		},
		{
			name: "invalid pane target",
			operation: func(server Server) error {
				pane := PaneID("work:0.0")
				return server.DisplayMenu(context.Background(), DisplayMenuRequest{
					Items:      []MenuItem{{Name: "One", Key: "1", Command: "select-pane"}},
					TargetPane: pane,
				})
			},
		},
		{
			name: "NUL menu client before version",
			operation: func(server Server) error {
				client := ClientName("secret-client\x00payload")
				return server.DisplayMenu(context.Background(), DisplayMenuRequest{
					Items:        []MenuItem{{Name: "One", Key: "1", Command: "select-pane"}},
					TargetClient: client,
					Mouse:        true,
				})
			},
		},
		{
			name: "NUL menu pane before version",
			operation: func(server Server) error {
				pane := PaneID("%1\x00payload")
				return server.DisplayMenu(context.Background(), DisplayMenuRequest{
					Items:      []MenuItem{{Name: "One", Key: "1", Command: "select-pane"}},
					TargetPane: pane,
					Mouse:      true,
				})
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
				t.Fatalf("operation error retained target value: %v", err)
			}
		})
	}
}

func TestInteractiveCommandsUseLiteralArgumentSeam(t *testing.T) {
	t.Parallel()

	t.Run("nested command terminal semicolon", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{responses: []versionResponse{
			{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
			{result: tmuxcmd.Result{}},
		}}
		err := serverWithRunner(runner).CommandPrompt(
			context.Background(),
			CommandPromptRequest{Template: "display-message literal;"},
		)
		if err != nil {
			t.Fatalf("CommandPrompt() error = %v", err)
		}
		assertInteractiveRequests(t, runner, [][]string{
			{"-V"}, {"command-prompt", "-b", `display-message literal\;`},
		})
	})

	t.Run("NUL is redacted before execution", func(t *testing.T) {
		t.Parallel()
		runner := &versionQueueRunner{}
		secret := "sensitive\x00value"
		err := serverWithRunner(runner).DisplayMenu(
			context.Background(),
			DisplayMenuRequest{Items: []MenuItem{{Name: "One", Key: "1", Command: secret}}},
		)
		if !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Fatalf("DisplayMenu() error = %v, want invalid request", err)
		}
		if runner.callCount() != 0 {
			t.Fatalf("runner calls = %d, want zero", runner.callCount())
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Fatalf("DisplayMenu() error retained NUL-bearing value: %v", err)
		}
	})
}

func TestInteractiveCommandFailuresRedactPayloads(t *testing.T) {
	t.Parallel()

	const secret = "interactive-secret"
	tests := []struct {
		name string
		run  func(Server) error
	}{
		{
			name: "confirm before",
			run: func(server Server) error {
				return server.ConfirmBefore(
					context.Background(),
					ConfirmBeforeRequest{Command: secret},
				)
			},
		},
		{
			name: "command prompt",
			run: func(server Server) error {
				return server.CommandPrompt(
					context.Background(),
					CommandPromptRequest{Template: secret},
				)
			},
		},
		{
			name: "display menu",
			run: func(server Server) error {
				return server.DisplayMenu(
					context.Background(),
					DisplayMenuRequest{
						Items:          []MenuItem{{Name: "secret", Command: secret}},
						StartingChoice: Ptr("0"),
					},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stdout: []string{"tmux 3.7b"}}},
				{result: tmuxcmd.Result{
					Command:  []string{"tmux", secret},
					Stdout:   []string{"stdout " + secret},
					Stderr:   []string{"stderr " + secret},
					ExitCode: 1,
				}},
			}}
			err := test.run(serverWithRunner(runner))
			var commandError *CommandError
			if !errors.As(err, &commandError) {
				t.Fatalf("interactive error = %#v, want *CommandError", err)
			}
			if commandError.Result.ExitCode != 1 || commandError.Result.Command != nil ||
				commandError.Result.Stdout != nil || commandError.Result.Stderr != nil {
				t.Fatalf("interactive CommandError = %#v, want exit-code-only result", commandError)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("interactive error disclosed payload: %v", err)
			}
		})
	}
}

func TestInteractiveCommandFailuresStayLoud(t *testing.T) {
	t.Parallel()

	transportFailure := errors.New("transport failed")
	tests := []struct {
		name      string
		response  versionResponse
		want      error
		wantCause error
	}{
		{
			name:     "completed stderr",
			response: versionResponse{result: tmuxcmd.Result{Stderr: []string{"no current client"}, ExitCode: 1}},
			want:     ErrCommand,
		},
		{
			name:      "transport",
			response:  versionResponse{err: transportFailure},
			wantCause: transportFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &versionQueueRunner{responses: []versionResponse{test.response}}
			err := serverWithRunner(runner).Start(context.Background())
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Fatalf("Start() error = %v, want %v", err, test.wantCause)
			}
		})
	}
}

func assertInteractiveRequests(
	t *testing.T,
	runner *versionQueueRunner,
	want [][]string,
) {
	t.Helper()
	requests := runner.recordedRequests()
	if len(requests) != len(want) {
		t.Fatalf("runner requests = %#v, want %d requests", requests, len(want))
	}
	for index := range want {
		assertRequestArguments(t, requests[index], want[index])
	}
}

func assertWarningFeatures(t *testing.T, warnings []Warning, want ...string) {
	t.Helper()
	features := make([]string, len(warnings))
	for index, warning := range warnings {
		if warning.Kind != WarningUnsupportedFeature {
			t.Fatalf("warning %d kind = %v, want unsupported feature", index, warning.Kind)
		}
		features[index] = warning.Feature
	}
	if !slices.Equal(features, want) {
		t.Fatalf("warning features = %#v, want %#v", features, want)
	}
}
