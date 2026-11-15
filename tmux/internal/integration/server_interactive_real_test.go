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

func TestBackgroundPromptsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
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

func TestStartServerAndHeadlessMenuAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
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

// TestInteractiveCommandsTreatLeadingDashTextAsPositionalAgainstRealTmux
// proves the confirm-before, command-prompt, and display-menu "--" guards
// against a live tmux. Without the guard, tmux's own parser reads a value
// beginning with "-" as that subcommand's own flags and refuses it, which the
// raw unguarded controls below reproduce.
//
// confirm-before validates its Command against tmux's own command table
// before ever displaying anything, and echoes the exact text back in
// "unknown command: <text>" once the value reaches that check - proof of
// verbatim arrival, checked on the raw guarded call because the typed method
// redacts completed stderr.
//
// command-prompt's Template is not resolved until submission, so a
// dash-prefixed one is accepted outright by a guarded call. Targeting a real
// attached client turns that into a clean, redaction-proof success signal:
// the typed call returns nil once guarded and a [tmux.ErrCommand] failure
// when the raw call omits the guard.
func TestInteractiveCommandsTreatLeadingDashTextAsPositionalAgainstRealTmux(t *testing.T) {
	probe := tmuxtest.NewServer(context.Background(), t)
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer probeCancel()
	version, err := probe.Version(probeCtx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if !version.AtLeast(interactiveRealVersion(t, "3.3")) {
		t.Skip("confirm-before and command-prompt require tmux 3.3")
	}
	if !version.AtLeast(interactiveRealVersion(t, "3.4")) {
		t.Skip("control-client prompt input is unreliable on tmux 3.3a")
	}

	const value = "-zzz-not-a-flag"

	t.Run("confirm-before", func(t *testing.T) {
		server := tmuxtest.NewServer(context.Background(), t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sessions, err := server.Sessions(ctx)
		if err != nil || len(sessions) != 1 {
			t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
		}
		client := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0]).ClientName()

		raw, err := server.Cmd(ctx, "confirm-before", "-b", "-t", client.String(), value)
		if err != nil || raw.ExitCode == 0 || containsAllLine(raw.Stderr, "unknown command") {
			t.Fatalf("unguarded confirm-before = (%#v, %v), want an unknown-flag parse failure", raw, err)
		}
		guarded, err := server.Cmd(ctx, "confirm-before", "-b", "-t", client.String(), "--", value)
		if err != nil || guarded.ExitCode == 0 || !containsAllLine(guarded.Stderr, "unknown command: "+value) {
			t.Fatalf("guarded confirm-before = (%#v, %v), want it to quote %q", guarded, err, value)
		}
		confirmErr := server.ConfirmBefore(ctx, tmux.ConfirmBeforeRequest{
			Command: value, TargetClient: client,
		})
		if !errors.Is(confirmErr, tmux.ErrCommand) {
			t.Fatalf("ConfirmBefore() error = %v, want ErrCommand (reached tmux's command table)", confirmErr)
		}
	})

	t.Run("command-prompt", func(t *testing.T) {
		server := tmuxtest.NewServer(context.Background(), t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sessions, err := server.Sessions(ctx)
		if err != nil || len(sessions) != 1 {
			t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
		}
		client := tmuxtest.NewControlMode(context.Background(), t, server, sessions[0]).ClientName()

		raw, err := server.Cmd(ctx, "command-prompt", "-b", "-t", client.String(), value)
		if err != nil || raw.ExitCode == 0 {
			t.Fatalf("unguarded command-prompt = (%#v, %v), want a parse failure", raw, err)
		}
		if err := server.CommandPrompt(ctx, tmux.CommandPromptRequest{
			Template: value, TargetClient: client,
		}); err != nil {
			t.Fatalf("CommandPrompt() error = %v, want nil once the guard reaches a real client", err)
		}
	})

	t.Run("display-menu", func(t *testing.T) {
		server := tmuxtest.NewServer(context.Background(), t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		raw, err := server.Cmd(ctx, "display-menu", value, "1", "select-pane")
		if err != nil || raw.ExitCode == 0 {
			t.Fatalf("unguarded display-menu = (%#v, %v), want a parse failure", raw, err)
		}
		guarded, err := server.Cmd(ctx, "display-menu", "--", value, "1", "select-pane")
		if err != nil || guarded.ExitCode == 0 || !containsAllLine(guarded.Stderr, "no current client") {
			t.Fatalf("guarded display-menu = (%#v, %v), want \"no current client\"", guarded, err)
		}
		menuErr := server.DisplayMenu(ctx, tmux.DisplayMenuRequest{
			Items: []tmux.MenuItem{{Name: value, Key: "1", Command: "select-pane"}},
		})
		if !errors.Is(menuErr, tmux.ErrCommand) {
			t.Fatalf("DisplayMenu() error = %v, want ErrCommand (reached tmux)", menuErr)
		}
	})
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
