//go:build linux

package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestRunShellCommandPreservesInheritedShellSemantics(t *testing.T) {
	for _, shell := range []struct {
		name      string
		arguments string
		traps     bool
	}{
		{name: "bash", arguments: "--noprofile --norc -i", traps: true},
		{name: "zsh", arguments: "-f -i", traps: true},
		{name: "dash", arguments: "-i"},
		{name: "sh", arguments: "-i"},
	} {
		shell := shell
		t.Run(shell.name, func(t *testing.T) {
			executable, err := exec.LookPath(shell.name)
			if err != nil {
				t.Skipf("%s is unavailable: %v", shell.name, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			request := tmux.NewSessionRequest{Name: "work"}
			target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
				FixedShell: true, InitialSession: &request,
			})
			panes, err := target.Panes(ctx)
			if err != nil || len(panes) != 1 {
				t.Fatalf("Panes() = (%v, %v), want one", panes, err)
			}
			launch := "exec /usr/bin/env ENV= PS1=" + shellQuote(tmuxtest.ShellPrompt) +
				" " + shellQuote(executable) + " " + shell.arguments
			pane, err := panes[0].Respawn(ctx, tmux.RespawnRequest{Command: &launch, Kill: true})
			if err != nil {
				t.Fatal(err)
			}
			tmuxtest.WaitForShellReady(ctx, t, pane)

			stateRoot := t.TempDir()
			exitMarker := filepath.Join(stateRoot, "parent-exit")
			debugOut := "trap-debug-" + shell.name + "-\"stdout\""
			debugErr := "trap-debug-" + shell.name + "-'stderr'"
			errorOut := "trap-error-" + shell.name + "-\"stdout\""
			errorErr := "trap-error-" + shell.name + "-'stderr'"
			setup := "\\trap " + shellQuote("command printf x > "+shellQuote(exitMarker)) + " EXIT"
			if shell.traps {
				debugAction := "command printf '%s\\n' " + shellQuote(debugOut) + "\n" +
					"command printf '%s\\n' " + shellQuote(debugErr) + " >&2"
				errorAction := "command printf '%s\\n' " + shellQuote(errorOut) + "\n" +
					"command printf '%s\\n' " + shellQuote(errorErr) + " >&2"
				setup += "; \\trap " + shellQuote(errorAction) + " ERR" +
					"; \\trap " + shellQuote(debugAction) + " DEBUG"
				if shell.name == "bash" {
					setup += "; \\set -E; \\set -T"
				}
			}
			setup += "; \\set -e; \\set -x; \\set -C"
			tmuxtest.TypeAndWait(ctx, t, pane, setup)

			pane, err = target.Pane(ctx, pane.ID())
			if err != nil {
				t.Fatal(err)
			}
			before := readShellResources(t, pane)
			runRoot := t.TempDir()
			t.Setenv("TMPDIR", runRoot)
			instance := mustInternalMCPServer(t, target)
			callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})
			run := func(command string) runCommandOutput {
				t.Helper()
				_, output, runErr := instance.tools.runCommand(callCtx, nil, runCommandInput{
					PaneID: pane.ID().String(), Command: command, TimeoutSeconds: 5,
					MaxLines: 2_000,
				})
				if runErr != nil {
					t.Fatalf("run %q: %v", command, runErr)
				}
				if output.ExitStatus == nil {
					t.Fatalf("run %q did not finish: %+v", command, output)
				}
				return output
			}

			successMarker := "command-success-" + shell.name
			success := run("command printf '%s\\n' " + shellQuote(successMarker))
			if *success.ExitStatus != 0 || !slices.Contains(success.Output, successMarker) {
				t.Fatalf("success = %+v", success)
			}
			if shell.traps && (countLine(success.Output, debugOut) != 1 ||
				countLine(success.Output, debugErr) != 1) {
				t.Fatalf("success trap output = %#v", success.Output)
			}

			unreachable := "command-unreachable-" + shell.name
			failure := run("false; command printf '%s\\n' " + shellQuote(unreachable))
			if *failure.ExitStatus == 0 || slices.Contains(failure.Output, unreachable) {
				t.Fatalf("failure = %+v", failure)
			}
			if shell.traps && (countLine(failure.Output, errorOut) != 1 ||
				countLine(failure.Output, errorErr) != 1) {
				t.Fatalf("failure trap output = %#v", failure.Output)
			}

			if invalid := run("if then"); *invalid.ExitStatus == 0 {
				t.Fatalf("syntax error = %+v", invalid)
			}
			if exited := run("exit 23"); *exited.ExitStatus != 23 {
				t.Fatalf("exit = %+v", exited)
			}
			parentMarker := "parent-state-" + shell.name
			parent := run("case $- in *e*) :;; *) exit 90;; esac; " +
				"case $- in *x*) :;; *) exit 91;; esac; " +
				"case $- in *C*) :;; *) exit 92;; esac; " +
				"if set | command grep -q '^__libtmux_'; then exit 93; fi; " +
				"command printf '%s\\n' " + shellQuote(parentMarker))
			if *parent.ExitStatus != 0 || !slices.Contains(parent.Output, parentMarker) {
				t.Fatalf("parent state = %+v", parent)
			}
			if shell.traps && (countLine(parent.Output, debugOut) == 0 ||
				countLine(parent.Output, debugErr) == 0) {
				t.Fatalf("parent trap output = %#v", parent.Output)
			}
			tmuxtest.TypeAndWait(ctx, t, pane, "\\set +e; \\set -x")
			xtraceMarker := "xtrace-only-" + shell.name
			xtrace := run("case $- in *e*) exit 90;; esac; " +
				"case $- in *x*) :;; *) exit 91;; esac; false; " +
				"command printf '%s\\n' " + shellQuote(xtraceMarker))
			if *xtrace.ExitStatus != 0 || !slices.Contains(xtrace.Output, xtraceMarker) {
				t.Fatalf("xtrace-only state = %+v", xtrace)
			}
			if _, err := os.Stat(exitMarker); !os.IsNotExist(err) {
				t.Fatalf("parent EXIT trap ran during wrapper: %v", err)
			}
			if entries, err := os.ReadDir(runRoot); err != nil || len(entries) != 0 {
				t.Fatalf("run setup residue = (%v, %v)", entryNames(entries), err)
			}
			if after := readShellResources(t, pane); after != before {
				t.Fatalf("parent resources changed:\nbefore: %s\nafter:  %s", before, after)
			}
		})
	}
}

func TestWrapperRejectsOversizedTrapDeclarations(t *testing.T) {
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) {
			executable, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s is unavailable: %v", name, err)
			}
			root := t.TempDir()
			opened := filepath.Join(root, "opened")
			command := filepath.Join(root, "command")
			traps := filepath.Join(root, "traps")
			status := filepath.Join(root, "status")
			closed := filepath.Join(root, "closed")
			sideEffect := filepath.Join(root, "command-ran")
			if err := os.WriteFile(command,
				[]byte("command printf x > "+shellQuote(sideEffect)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(root, "script")
			contents := wrapperScript("command printf '0 0 0 80 24\\n'", opened,
				command, traps, status, closed, "bounded")
			if err := os.WriteFile(script, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			setup := "readonly __libtmux_errexit=human __libtmux_status=human; " +
				"trap_action=\": x$(command printf '%070000d' 0)\"; \\trap \"$trap_action\" ERR; " +
				"\\unset trap_action; . " + shellQuote(script) + "; " +
				"[ \"$__libtmux_errexit:$__libtmux_status\" = human:human ]"
			process := exec.Command(executable, "-f", "-c", setup)
			output, err := process.CombinedOutput()
			if err != nil {
				t.Fatalf("wrapper process: %v\n%s", err, output)
			}
			got, err := os.ReadFile(status)
			if err != nil || string(got) != "125" {
				t.Fatalf("status = (%q, %v), want 125", got, err)
			}
			if _, err := os.Stat(sideEffect); !os.IsNotExist(err) {
				t.Fatalf("oversized trap command side effect: %v", err)
			}
			info, err := os.Stat(traps)
			if err != nil || info.Mode().Perm() != 0o600 || info.Size() != 0 {
				t.Fatalf("trap transport = (%v, %v)", info, err)
			}
			for _, path := range []string{opened, closed} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("completion record %s: %v", filepath.Base(path), err)
				}
			}
		})
	}
}

func countLine(lines []string, want string) int {
	count := 0
	for _, line := range lines {
		if line == want {
			count++
		}
	}
	return count
}

func readShellResources(t *testing.T, pane tmux.Pane) string {
	t.Helper()
	pid, ok := pane.ProcessPID()
	if !ok || pid <= 0 {
		t.Fatalf("pane has no process id: %+v", pane.Formats())
	}
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		t.Fatal(err)
	}
	children, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("fds=%s children=%s", strings.Join(entryNames(fds), ","),
		strings.TrimSpace(string(children)))
}
