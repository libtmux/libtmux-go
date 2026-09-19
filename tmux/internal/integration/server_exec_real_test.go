//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.server.Server.run_shell
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:args:fd422ab3a1ca
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:as_tmux_command:0fe82009cc1e
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:background:af1c44728d8a
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:background:af1c44728d8a:2
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:cwd:c8f5f1bebe8f
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:delay:0b3ab3dbe007
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:show_stderr:77ae01eb137f
// libtmux:parity libtmux.server.Server.run_shell#parameter-branch:target_pane:5f9e4a0df2ff
// libtmux:parity libtmux.server.Server.run_shell#version-branch:tmux-version:157b9dba160f
// libtmux:parity libtmux.server.Server.run_shell#version-branch:tmux-version:161023f2e486
// libtmux:parity libtmux.server.Server.run_shell#version-branch:tmux-version:5bb2ac269d05
// libtmux:parity libtmux.server.Server.run_shell#warning:39bcbdbb8388
// libtmux:parity libtmux.server.Server.run_shell#warning:78d80521a5af
// libtmux:parity libtmux.server.Server.run_shell#warning:9349ea8de11f
//
//libtmux:real-tmux
func TestRunShellAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version35, err := tmux.ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}
	version36, err := tmux.ParseVersion("3.6")
	if err != nil {
		t.Fatal(err)
	}
	version37, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}

	if version.AtLeast(version35) {
		directory, evalErr := filepath.EvalSymlinks(t.TempDir())
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		request := tmux.RunShellRequest{
			Command:        "printf '%s\\n' \"$PWD\"",
			StartDirectory: &directory,
		}
		want := directory
		if version.AtLeast(version37) {
			request.Command = "printf '%s:%s:%s\\n' '#{1}' '#{2}' \"$PWD\""
			request.Args = []string{"alpha", "beta"}
			want = "alpha:beta:" + directory
		}
		output, runErr := server.RunShell(ctx, request)
		if runErr != nil {
			t.Fatalf("RunShell() error = %v", runErr)
		}
		if !slices.Contains(output, want) {
			t.Fatalf("RunShell() = %#v, want line %q", output, want)
		}
	}

	if version.AtLeast(version36) {
		output, runErr := server.RunShell(ctx, tmux.RunShellRequest{
			Command:    "printf 'stdout\\n'; printf 'stderr\\n' >&2",
			ShowStderr: true,
		})
		if runErr != nil {
			t.Fatalf("RunShell(ShowStderr) error = %v", runErr)
		}
		if !slices.Contains(output, "stdout") || !slices.Contains(output, "stderr") {
			t.Fatalf("RunShell(ShowStderr) = %#v, want stdout and stderr", output)
		}
	}

	background, err := server.RunShell(ctx, tmux.RunShellRequest{
		Command:    "true",
		Background: true,
	})
	if err != nil {
		t.Fatalf("RunShell(background) error = %v", err)
	}
	if background != nil {
		t.Fatalf("RunShell(background) = %#v, want nil", background)
	}
}

//libtmux:real-tmux
func TestRunShellExitSevenRemainsCompletedOutput(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version35, err := tmux.ParseVersion("3.5")
	if err != nil {
		t.Fatal(err)
	}
	version33, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	output, err := server.RunShell(ctx, tmux.RunShellRequest{Command: "exit 7"})
	if err != nil {
		t.Fatalf("RunShell(exit 7) error = %v, want nil", err)
	}
	if (!version.AtLeast(version33) || version.AtLeast(version35)) &&
		!slices.Equal(output, []string{"'exit 7' returned 7"}) {
		t.Fatalf("RunShell(exit 7) = %#v, want tmux completion output", output)
	}
	if version.AtLeast(version33) && !version.AtLeast(version35) && len(output) != 0 {
		t.Fatalf("RunShell(exit 7) = %#v, want empty 3.3-3.4 passthrough", output)
	}
}

//libtmux:real-tmux
func TestCommandKillDoesNotWaitForShellExit(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	minimum := interactiveRealVersion(t, "3.5")
	next := interactiveRealVersion(t, "3.6")
	if !version.AtLeast(minimum) || version.AtLeast(next) {
		t.Skip("tmux 3.5 runs jobs through default-shell")
	}
	snapshot := mustRealSnapshot(t, server)
	session := snapshot.Sessions()[0]
	running, err := session.Start(ctx, "sleep 30", tmux.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}

	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
	}
	shell := filepath.Join(t.TempDir(), "kill-shell")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in *'kill -s KILL --'*) ;; *) exec /bin/sh "$@" ;; esac
/bin/sh "$@"
%s -S %s wait-for -S kill-dispatched
%s -S %s wait-for release-kill-job
`,
		quote(server.Executable()), quote(server.SocketPath()),
		quote(server.Executable()), quote(server.SocketPath()))
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := session.SetDefaultShell(ctx, shell); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_ = server.WaitFor(cleanupCtx, tmux.WaitForRequest{
			Channel: "release-kill-job", Mode: tmux.WaitForModeSignal,
		})
	})

	killCtx, cancelKill := context.WithTimeout(ctx, 5*time.Second)
	defer cancelKill()
	killed := make(chan error, 1)
	go func() { killed <- running.Kill(killCtx) }()
	if err := server.WaitFor(ctx, tmux.WaitForRequest{Channel: "kill-dispatched"}); err != nil {
		t.Fatal(err)
	}
	if err := <-killed; err != nil {
		t.Fatalf("Kill() waited for the dispatch job: %v", err)
	}
	result, err := running.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Signal == "" {
		t.Fatalf("Wait() reported no terminating signal: %+v", result)
	}
}

// libtmux:parity libtmux.server.Server.wait_for
// libtmux:parity libtmux.server.Server.wait_for#parameter-branch:lock:92e800fe2c02
// libtmux:parity libtmux.server.Server.wait_for#parameter-branch:set_flag:6fd02ea1aab4
// libtmux:parity libtmux.server.Server.wait_for#parameter-branch:unlock:8d2b1171c459
//
//libtmux:real-tmux
func TestWaitForSignalLockUnlockAndCancellation(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)

	t.Run("signal", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		waited := make(chan error, 1)
		go func() {
			waited <- server.WaitFor(ctx, tmux.WaitForRequest{Channel: "phase6-signal"})
		}()
		if err := server.WaitFor(ctx, tmux.WaitForRequest{
			Channel: "phase6-signal",
			Mode:    tmux.WaitForModeSignal,
		}); err != nil {
			t.Fatalf("WaitFor(signal) error = %v", err)
		}
		if err := <-waited; err != nil {
			t.Fatalf("WaitFor(wait) error = %v", err)
		}
	})

	t.Run("lock and unlock", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request := tmux.WaitForRequest{Channel: "phase6-lock", Mode: tmux.WaitForModeLock}
		if err := server.WaitFor(ctx, request); err != nil {
			t.Fatalf("first WaitFor(lock) error = %v", err)
		}
		locked := make(chan error, 1)
		go func() { locked <- server.WaitFor(ctx, request) }()
		if err := server.WaitFor(ctx, tmux.WaitForRequest{
			Channel: "phase6-lock",
			Mode:    tmux.WaitForModeUnlock,
		}); err != nil {
			t.Fatalf("WaitFor(unlock) error = %v", err)
		}
		if err := <-locked; err != nil {
			t.Fatalf("second WaitFor(lock) error = %v", err)
		}
		if err := server.WaitFor(ctx, tmux.WaitForRequest{
			Channel: "phase6-lock",
			Mode:    tmux.WaitForModeUnlock,
		}); err != nil {
			t.Fatalf("final WaitFor(unlock) error = %v", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err := server.WaitFor(ctx, tmux.WaitForRequest{Channel: "phase6-cancel"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WaitFor(cancel) error = %v, want context deadline", err)
		}
	})
}

// libtmux:parity libtmux.server.Server.if_shell
// libtmux:parity libtmux.server.Server.if_shell#parameter-branch:background:af1c44728d8a
// libtmux:parity libtmux.server.Server.if_shell#parameter-branch:else_command:bef5b34f0329
// libtmux:parity libtmux.server.Server.if_shell#parameter-branch:target_pane:5f9e4a0df2ff
// libtmux:parity libtmux.server.Server.source_file
// libtmux:parity libtmux.server.Server.source_file#parameter-branch:parse_only:656bfbd1a466
// libtmux:parity libtmux.server.Server.source_file#parameter-branch:quiet:8573bc8befe4
// libtmux:parity libtmux.server.Server.source_file#parameter-branch:verbose:414278ee0d55
//
//libtmux:real-tmux
func TestIfShellAndSourceFileAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	otherwise := "set-option -g @phase6_if no"
	if err := server.IfShell(ctx, tmux.IfShellRequest{
		ShellCommand: "false",
		ThenCommand:  "set-option -g @phase6_if yes",
		ElseCommand:  &otherwise,
	}); err != nil {
		t.Fatalf("IfShell() error = %v", err)
	}
	result, err := server.Cmd(ctx, "show-options", "-gv", "@phase6_if")
	if err != nil || !slices.Equal(result.Stdout, []string{"no"}) {
		t.Fatalf("show if-shell option = (%#v, %v), want no", result, err)
	}

	configuration := filepath.Join(t.TempDir(), "phase6.conf")
	if err := os.WriteFile(configuration, []byte("set-option -g @phase6_source yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.SourceFile(ctx, tmux.SourceFileRequest{Path: configuration}); err != nil {
		t.Fatalf("SourceFile() error = %v", err)
	}
	result, err = server.Cmd(ctx, "show-options", "-gv", "@phase6_source")
	if err != nil || !slices.Equal(result.Stdout, []string{"yes"}) {
		t.Fatalf("show sourced option = (%#v, %v), want yes", result, err)
	}

	missing := filepath.Join(t.TempDir(), "missing.conf")
	if err := server.SourceFile(ctx, tmux.SourceFileRequest{Path: missing, Quiet: true}); err != nil {
		t.Fatalf("SourceFile(quiet missing) error = %v", err)
	}

	invalid := filepath.Join(t.TempDir(), "invalid.conf")
	if err := os.WriteFile(
		invalid,
		[]byte("set-option -g @phase6_parse after\nwibble\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	result, err = server.Cmd(ctx, "set-option", "-g", "@phase6_parse", "before")
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("set parse-only sentinel = (%#v, %v)", result, err)
	}
	raw, err := server.Cmd(ctx, "source-file", "-n", invalid)
	if err != nil || raw.ExitCode == 0 || len(raw.Stderr) != 0 ||
		!strings.Contains(strings.Join(raw.Stdout, "\n"), "unknown command: wibble") {
		t.Fatalf("raw parse-only syntax failure = (%#v, %v)", raw, err)
	}
	if err := server.SourceFile(ctx, tmux.SourceFileRequest{
		Path:      invalid,
		ParseOnly: true,
	}); err != nil {
		t.Fatalf("SourceFile(parse invalid) error = %v, want nil for stdout-only failure", err)
	}
	result, err = server.Cmd(ctx, "show-options", "-gv", "@phase6_parse")
	if err != nil || !slices.Equal(result.Stdout, []string{"before"}) {
		t.Fatalf("parse-only sentinel = (%#v, %v), want before", result, err)
	}
}

//libtmux:real-tmux
func TestSourceFilePreservesTerminalSemicolonPathAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	configuration := filepath.Join(t.TempDir(), "terminal.conf;")
	if err := os.WriteFile(configuration, []byte("set-option -g @phase6_semicolon yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.SourceFile(ctx, tmux.SourceFileRequest{Path: configuration}); err != nil {
		t.Fatalf("SourceFile() error = %v", err)
	}
	result, err := server.Cmd(ctx, "show-options", "-gv", "@phase6_semicolon")
	if err != nil || !slices.Equal(result.Stdout, []string{"yes"}) {
		t.Fatalf("show sourced option = (%#v, %v), want yes", result, err)
	}
}

// TestRunShellTreatsLeadingDashCommandAsPositionalAgainstRealTmux proves the
// run-shell "--" guard against a live tmux. Without it, tmux's own argument
// parser reads a Command beginning with "-" as run-shell's own flags and
// refuses it before any shell ever runs, which is what the raw control below
// reproduces (no job, so nothing is ever reported "returned").
//
// The command itself still can't run cleanly here: /bin/sh -c rejects a "-c"
// operand that itself starts with "-" as a shell invocation option, a
// limitation of the shell tmux execs and orthogonal to tmux's own argument
// parsing. tmux reports that job failure as `'<cmd>' returned <n>`
// (cmd-run-shell.c, stable across every supported tmux release), quoting the
// exact command string back - proof it reached a real job verbatim, which is
// everything this guard is responsible for.
//
//libtmux:real-tmux
func TestRunShellTreatsLeadingDashCommandAsPositionalAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const command = "-not-a-flag || true"

	// Control: the same command without the "--" guard is parsed as flags by
	// tmux itself, and fails before a shell ever runs.
	raw, err := server.Cmd(ctx, "run-shell", command)
	if err != nil || raw.ExitCode == 0 || slices.ContainsFunc(raw.Stdout, func(line string) bool {
		return strings.Contains(line, "returned")
	}) {
		t.Fatalf("unguarded run-shell = (%#v, %v), want a parse failure with no job output", raw, err)
	}

	output, err := server.RunShell(ctx, tmux.RunShellRequest{Command: command})
	if err != nil {
		t.Fatalf("RunShell() error = %v", err)
	}
	if len(output) == 0 {
		t.Fatalf("RunShell() output = %#v, want a job completion line", output)
	}
	if want := "'" + command + "' returned "; !strings.HasPrefix(output[len(output)-1], want) {
		t.Fatalf("RunShell() output tail = %q, want prefix %q", output[len(output)-1], want)
	}
}

// TestIfShellTreatsLeadingDashShellCommandAsPositionalAgainstRealTmux proves
// the if-shell "--" guard: one guard precedes ShellCommand, ThenCommand, and
// ElseCommand together, so a dash-prefixed condition is enough to show all
// three still land as positional operands.
//
// As with run-shell, /bin/sh -c cannot execute a condition that itself starts
// with "-", so the condition job exits nonzero for reasons of its own; that
// is still proof the guarded value reached a real job (and picked the else
// branch) rather than being refused by tmux before ever running one.
//
//libtmux:real-tmux
func TestIfShellTreatsLeadingDashShellCommandAsPositionalAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const shellCommand = "-not-a-flag || true"
	thenCommand := "set-option -g @phase6_dash then"
	elseCommand := "set-option -g @phase6_dash else"

	// Control: refused by tmux's own parser before either branch runs.
	raw, err := server.Cmd(ctx, "if-shell", shellCommand, thenCommand, elseCommand)
	if err != nil || raw.ExitCode == 0 {
		t.Fatalf("unguarded if-shell = (%#v, %v), want a parse failure", raw, err)
	}
	result, err := server.Cmd(ctx, "show-options", "-gv", "@phase6_dash")
	if err != nil || result.ExitCode == 0 {
		t.Fatalf("show if-shell option after unguarded call = (%#v, %v), want unset", result, err)
	}

	if err := server.IfShell(ctx, tmux.IfShellRequest{
		ShellCommand: shellCommand,
		ThenCommand:  thenCommand,
		ElseCommand:  &elseCommand,
	}); err != nil {
		t.Fatalf("IfShell() error = %v", err)
	}
	result, err = server.Cmd(ctx, "show-options", "-gv", "@phase6_dash")
	if err != nil || !slices.Equal(result.Stdout, []string{"else"}) {
		t.Fatalf(
			"show if-shell option = (%#v, %v), want else (the guarded condition ran and failed)",
			result, err,
		)
	}
}

// TestWaitForTreatsLeadingDashChannelAsPositionalAgainstRealTmux proves the
// wait-for "--" guard: a channel name is a bare positional argument on every
// mode, so an unguarded "-S -chan" is parsed as two flag clusters rather than
// a signal and its channel.
//
//libtmux:real-tmux
func TestWaitForTreatsLeadingDashChannelAsPositionalAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	raw, err := server.Cmd(ctx, "wait-for", "-S", "-dash-channel")
	if err != nil || raw.ExitCode == 0 {
		t.Fatalf("unguarded wait-for -S = (%#v, %v), want a parse failure", raw, err)
	}

	if err := server.WaitFor(ctx, tmux.WaitForRequest{
		Channel: "-dash-channel",
		Mode:    tmux.WaitForModeSignal,
	}); err != nil {
		t.Fatalf("WaitFor(signal) error = %v", err)
	}
	// tmux remembers a signal that arrived with no waiter, so a later plain
	// wait on the same channel returns at once instead of blocking.
	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	if err := server.WaitFor(waitCtx, tmux.WaitForRequest{Channel: "-dash-channel"}); err != nil {
		t.Fatalf("WaitFor(wait) error = %v, want the remembered signal to return at once", err)
	}
}
