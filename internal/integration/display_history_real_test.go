//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
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
//
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
// libtmux:parity libtmux.pane.Pane.display_message#version-branch:tmux-version:1cded5d69f99
// libtmux:parity libtmux.pane.Pane.display_message#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.pane.Pane.display_message#warning:25890e6f203b
// libtmux:parity libtmux.pane.Pane.display_message#warning:26e3948d3d8c
// libtmux:parity libtmux.pane.Pane.display_message#warning:fe1b297a0f22
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
// libtmux:parity libtmux.window.Window.display_message#version-branch:tmux-version:4ec38997c7f9
// libtmux:parity libtmux.window.Window.display_message#warning:25890e6f203b
// libtmux:parity libtmux.window.Window.display_message#warning:fe1b297a0f22
//
//libtmux:real-tmux
func TestDisplayMessageScopesAndVersionFlagsAgainstRealTmux(t *testing.T) {
	base := tmuxtest.NewServer(context.Background(), t)
	warnings := make([]tmux.Warning, 0, 2)
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: base.SocketPath(),
		ConfigFile: base.ConfigFile(),
		WarningHandler: func(warning tmux.Warning) {
			warnings = append(warnings, warning)
		},
	}).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	windows, err := server.Windows(ctx)
	if err != nil || len(windows) != 1 {
		t.Fatalf("Windows() = (%#v, %v), want one window", windows, err)
	}
	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes() = (%#v, %v), want one pane", panes, err)
	}
	window := windows[0]
	pane := panes[0]

	serverOutput, err := server.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Message: "#{version}", Print: true,
	})
	if err != nil || !slices.Equal(serverOutput, []string{version.String()}) {
		t.Fatalf("Server.DisplayMessage() = (%#v, %v), want %s", serverOutput, err, version)
	}
	leadingDash, err := server.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Message: "-literal", Print: true,
	})
	if err != nil || !slices.Equal(leadingDash, []string{"-literal"}) {
		t.Fatalf("DisplayMessage(leading dash) = (%#v, %v), want -literal", leadingDash, err)
	}
	separatorFormat := "#{version};"
	if _, err := server.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Message: "kill-server", Print: true, Format: &separatorFormat,
	}); err != nil {
		t.Fatalf("DisplayMessage(separator-shaped format) error = %v", err)
	}
	if alive, err := server.IsAlive(ctx); err != nil || !alive {
		t.Fatalf("server alive after literal display arguments = (%t, %v)", alive, err)
	}
	windowOutput, err := window.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Message: "#{window_id}", Print: true,
	})
	if err != nil || !slices.Equal(windowOutput, []string{window.ID().String()}) {
		t.Fatalf("Window.DisplayMessage() = (%#v, %v), want %s", windowOutput, err, window.ID())
	}
	paneOutput, err := pane.DisplayMessage(ctx, tmux.PaneDisplayMessageRequest{
		DisplayMessageRequest: tmux.DisplayMessageRequest{Message: "#{pane_id}", Print: true},
	})
	if err != nil || !slices.Equal(paneOutput, []string{pane.ID().String()}) {
		t.Fatalf("Pane.DisplayMessage() = (%#v, %v), want %s", paneOutput, err, pane.ID())
	}

	format := "#{socket_path}"
	formatted, err := server.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Print: true, Format: &format,
	})
	if err != nil || !slices.Equal(formatted, []string{base.SocketPath()}) {
		t.Fatalf("DisplayMessage(Format) = (%#v, %v), want socket path", formatted, err)
	}
	allFormats, err := server.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Print: true, AllFormats: true,
	})
	if err != nil || !containsDisplayLine(allFormats, "version=") {
		t.Fatalf("DisplayMessage(AllFormats) = (%#v, %v), want version format", allFormats, err)
	}
	verbose, err := pane.DisplayMessage(ctx, tmux.PaneDisplayMessageRequest{
		DisplayMessageRequest: tmux.DisplayMessageRequest{
			Message: "#{pane_id}", Print: true, Verbose: true,
		},
	})
	if err != nil || !slices.Contains(verbose, pane.ID().String()) {
		t.Fatalf("DisplayMessage(Verbose) = (%#v, %v), want pane id", verbose, err)
	}

	warnings = nil
	literal, err := pane.DisplayMessage(ctx, tmux.PaneDisplayMessageRequest{
		DisplayMessageRequest: tmux.DisplayMessageRequest{
			Message: "#{pane_id}", Print: true, NoExpand: true,
		},
		UpdatePane: true,
	})
	if err != nil {
		t.Fatalf("DisplayMessage(version flags) error = %v", err)
	}
	version34, err := tmux.ParseVersion("3.4")
	if err != nil {
		t.Fatal(err)
	}
	version36, err := tmux.ParseVersion("3.6")
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := []string{pane.ID().String()}
	wantFeatures := []string{"no_expand", "update_pane"}
	if version.AtLeast(version34) {
		wantOutput = []string{"#{pane_id}"}
		wantFeatures = []string{"update_pane"}
	}
	if version.AtLeast(version36) {
		wantFeatures = nil
	}
	if !slices.Equal(literal, wantOutput) {
		t.Fatalf("DisplayMessage(version flags) = %#v, want %#v on tmux %s", literal, wantOutput, version)
	}
	features := make([]string, len(warnings))
	for index, warning := range warnings {
		features[index] = warning.Feature
	}
	if !slices.Equal(features, wantFeatures) {
		t.Fatalf("DisplayMessage warning features = %#v, want %#v on tmux %s", features, wantFeatures, version)
	}

	warnings = nil
	conflictingFormat := "#{version}"
	failedOutput, err := server.DisplayMessage(ctx, tmux.DisplayMessageRequest{
		Message: "x", Print: true, Format: &conflictingFormat,
	})
	if err != nil || failedOutput == nil || len(failedOutput) != 0 {
		t.Fatalf("DisplayMessage(completed stderr) = (%#v, %v), want nonnil empty", failedOutput, err)
	}
	if len(warnings) != 1 || warnings[0].Kind != tmux.WarningCommandStderr ||
		warnings[0].Subcommand != "display-message" ||
		!strings.Contains(warnings[0].Message, "only one of -F or argument") {
		t.Fatalf("DisplayMessage(completed stderr) warnings = %#v", warnings)
	}

	version33, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	if version.AtLeast(version33) {
		sessions, sessionErr := base.Sessions(ctx)
		if sessionErr != nil || len(sessions) != 1 {
			t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, sessionErr)
		}
		control := tmuxtest.NewControlMode(context.Background(), t, base, sessions[0])
		client := control.ClientName()
		delay := 1
		output, displayErr := server.DisplayMessage(ctx, tmux.DisplayMessageRequest{
			Message: "status-message", TargetClient: client, Delay: &delay, Notify: true,
		})
		if displayErr != nil || output != nil {
			t.Fatalf("DisplayMessage(status) = (%#v, %v), want nil result", output, displayErr)
		}
	}
}

// libtmux:parity libtmux.server.Server.clear_prompt_history
// libtmux:parity libtmux.server.Server.clear_prompt_history#parameter-branch:prompt_type:83546fba116e
// libtmux:parity libtmux.server.Server.clear_prompt_history#version-branch:tmux-version:d9801479e597
// libtmux:parity libtmux.server.Server.show_prompt_history
// libtmux:parity libtmux.server.Server.show_prompt_history#parameter-branch:prompt_type:83546fba116e
// libtmux:parity libtmux.server.Server.show_prompt_history#version-branch:tmux-version:d9801479e597
//
//libtmux:real-tmux
func TestPromptHistoryVersionGateAndTypesAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	minimum, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	all, showErr := server.ShowPromptHistory(ctx, tmux.PromptHistoryRequest{})
	if !version.AtLeast(minimum) {
		if !errors.Is(showErr, tmux.ErrVersionTooLow) || all != nil {
			t.Fatalf("ShowPromptHistory() = (%#v, %v), want tmux 3.3 gate", all, showErr)
		}
		if clearErr := server.ClearPromptHistory(ctx, tmux.PromptHistoryRequest{}); !errors.Is(clearErr, tmux.ErrVersionTooLow) {
			t.Fatalf("ClearPromptHistory() error = %v, want tmux 3.3 gate", clearErr)
		}
		return
	}
	currentMinimum, err := tmux.ParseVersion("3.8")
	if err != nil {
		t.Fatal(err)
	}
	currentTypes := version.AtLeast(currentMinimum)
	if showErr != nil || !containsDisplayLine(all, "History for command:") ||
		!containsDisplayLine(all, "History for search:") {
		t.Fatalf("ShowPromptHistory(all) = (%#v, %v), want common typed headers", all, showErr)
	}
	if currentTypes {
		if containsDisplayLine(all, "History for target:") ||
			containsDisplayLine(all, "History for window-target:") {
			t.Fatalf("ShowPromptHistory(all) = %#v, want master command/search types only", all)
		}
	} else if !containsDisplayLine(all, "History for target:") ||
		!containsDisplayLine(all, "History for window-target:") {
		t.Fatalf("ShowPromptHistory(all) = %#v, want stable target types", all)
	}

	for _, test := range []struct {
		promptType tmux.PromptType
		header     string
		current    bool
	}{
		{promptType: tmux.PromptTypeCommand, header: "History for command:", current: true},
		{promptType: tmux.PromptTypeSearch, header: "History for search:", current: true},
		{promptType: tmux.PromptTypeTarget, header: "History for target:"},
		{promptType: tmux.PromptTypeWindowTarget, header: "History for window-target:"},
	} {
		request := tmux.PromptHistoryRequest{Type: test.promptType}
		lines, err := server.ShowPromptHistory(ctx, request)
		if currentTypes && !test.current {
			if lines != nil || !errors.Is(err, tmux.ErrCommand) {
				t.Fatalf("ShowPromptHistory(%s) = (%#v, %v), want master command error", test.header, lines, err)
			}
			if err := server.ClearPromptHistory(ctx, request); !errors.Is(err, tmux.ErrCommand) {
				t.Fatalf("ClearPromptHistory(%s) error = %v, want master command error", test.header, err)
			}
			continue
		}
		if err != nil || !containsDisplayLine(lines, test.header) {
			t.Fatalf("ShowPromptHistory(%s) = (%#v, %v), want header", test.header, lines, err)
		}
		if err := server.ClearPromptHistory(ctx, request); err != nil {
			t.Fatalf("ClearPromptHistory(%s) error = %v", test.header, err)
		}
	}
	if err := server.ClearPromptHistory(ctx, tmux.PromptHistoryRequest{}); err != nil {
		t.Fatalf("ClearPromptHistory(all) error = %v", err)
	}
}

//libtmux:real-tmux
func TestPromptHistoryFailurePolicyAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	minimum, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	if !version.AtLeast(minimum) {
		return
	}
	result, err := server.Cmd(ctx, "kill-server")
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("kill-server = (%#v, %v), want clean completion", result, err)
	}

	lines, err := server.ShowPromptHistory(ctx, tmux.PromptHistoryRequest{})
	if err != nil || lines == nil || len(lines) != 0 {
		t.Fatalf("lenient ShowPromptHistory() = (%#v, %v), want nonnil empty", lines, err)
	}
	if _, err := server.WithStrictErrors().ShowPromptHistory(
		ctx, tmux.PromptHistoryRequest{},
	); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("strict ShowPromptHistory() error = %v, want ErrCommand", err)
	}
	if err := server.ClearPromptHistory(ctx, tmux.PromptHistoryRequest{}); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("ClearPromptHistory() error = %v, want ErrCommand", err)
	}
}

func containsDisplayLine(lines []string, part string) bool {
	for _, line := range lines {
		if strings.Contains(line, part) {
			return true
		}
	}
	return false
}
