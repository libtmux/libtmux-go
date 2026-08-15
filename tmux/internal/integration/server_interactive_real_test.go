//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

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
//
//libtmux:real-tmux
func TestBackgroundPromptsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	minimum33 := interactiveRealVersion(t, "3.3")
	minimum34 := interactiveRealVersion(t, "3.4")
	if !version.AtLeast(minimum33) {
		if err := server.ConfirmBefore(ctx, tmux.ConfirmBeforeRequest{
			Command: "set-option -g @go-confirm yes",
		}); !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("ConfirmBefore() error = %v, want tmux 3.3 floor", err)
		}
		if err := server.CommandPrompt(ctx, tmux.CommandPromptRequest{
			Template: "set-option -g @go-prompt %1",
		}); !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("CommandPrompt() error = %v, want tmux 3.3 floor", err)
		}
		return
	}
	if !version.AtLeast(minimum34) {
		t.Skip("control-client prompt input is unreliable on tmux 3.3a")
	}

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	control := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0])
	client := control.ClientName()

	if err := server.ConfirmBefore(ctx, tmux.ConfirmBeforeRequest{
		Command:      "set-option -g @go-confirm yes",
		TargetClient: client,
	}); err != nil {
		t.Fatalf("ConfirmBefore() error = %v", err)
	}
	interactiveRealCommand(ctx, t, server, "send-keys", "-K", "-c", client.String(), "y")
	waitInteractiveOption(ctx, t, server, "@go-confirm", "yes")

	inputs := "prefilled"
	if err := server.CommandPrompt(ctx, tmux.CommandPromptRequest{
		Template:     "set-option -g @go-prompt '%1'",
		Inputs:       &inputs,
		TargetClient: client,
	}); err != nil {
		t.Fatalf("CommandPrompt() error = %v", err)
	}
	interactiveRealCommand(ctx, t, server, "send-keys", "-K", "-c", client.String(), "Enter")
	waitInteractiveOption(ctx, t, server, "@go-prompt", "prefilled")
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
//
//libtmux:real-tmux
func TestStartServerAndHeadlessMenuAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err := server.DisplayMenu(ctx, tmux.DisplayMenuRequest{
		Items: []tmux.MenuItem{{Name: "One", Key: "1", Command: "select-pane"}},
	})
	if err == nil {
		t.Fatal("DisplayMenu() error = nil without a TTY-backed client")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DisplayMenu() hung without a TTY-backed client: %v", err)
	}
}

func interactiveRealVersion(t *testing.T, value string) tmux.Version {
	t.Helper()
	version, err := tmux.ParseVersion(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func interactiveRealCommand(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	arguments ...string,
) {
	t.Helper()
	result, err := server.Cmd(ctx, arguments...)
	if err != nil {
		t.Fatalf("tmux %v error = %v", arguments, err)
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("tmux %v exited %d: %v", arguments, result.ExitCode, result.Stderr)
	}
}

func waitInteractiveOption(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	name string,
	want string,
) {
	t.Helper()
	for {
		result, err := server.Cmd(ctx, "show-options", "-gv", name)
		if err == nil && result.ExitCode == 0 && len(result.Stdout) == 1 &&
			result.Stdout[0] == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("option %s did not become %q: %v", name, want, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
