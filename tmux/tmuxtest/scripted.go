package tmuxtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ScriptedCommand is one answer a scripted tmux gives. The first matching
// entry wins, and an invocation nothing matches exits 1 saying so.
type ScriptedCommand struct {
	// Contains selects the invocations this entry answers: one matches when
	// its arguments contain every string listed, so "list-sessions" answers
	// that subcommand whatever flags surround it. Empty matches every
	// invocation.
	Contains []string
	// Stdout is written to standard output exactly, newlines included.
	Stdout string
	// Stderr is written to standard error exactly, newlines included. This
	// package reads a completed failure from stderr rather than from an exit
	// code, so an entry standing in for one sets this.
	Stderr string
	// ExitCode is the status the invocation exits with.
	ExitCode int
}

// ScriptedTmux writes an executable that answers tmux invocations from script
// and returns its absolute path, for [tmux.ServerOptions.Binary]. It is how a
// test drives code that uses this package without a tmux to drive:
//
//	server, err := tmux.NewServer(tmux.ServerOptions{
//		Binary: tmuxtest.ScriptedTmux(t,
//			tmuxtest.ScriptedCommand{Contains: []string{"-V"}, Stdout: "tmux 3.7\n"},
//			tmuxtest.ScriptedCommand{
//				Contains: []string{"kill-pane"},
//				Stderr:   "can't find pane: %7\n",
//				ExitCode: 1,
//			},
//		),
//	})
//
// The executable is removed with the test. It answers commands only: opening a
// control connection or a notification stream speaks tmux's control protocol,
// which a script cannot, so code that opens one needs [NewServer] and a real
// tmux.
func ScriptedTmux(t testing.TB, script ...ScriptedCommand) string {
	t.Helper()

	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	// Written by ScriptedTmux; matching is on the whole argument list so a
	// caller names a subcommand without restating the flags around it.
	// Each argument is matched whole. Joining them into one string would let
	// a needle match across a boundary, and a tmux target may hold a space.
	body.WriteString("match() {\n")
	body.WriteString("  for argument in \"$@\"; do\n")
	body.WriteString("    [ \"$argument\" = \"$needle\" ] && return 0\n")
	body.WriteString("  done\n")
	body.WriteString("  return 1\n")
	body.WriteString("}\n")
	for index, command := range script {
		for _, needle := range command.Contains {
			if needle == "" {
				t.Fatalf("ScriptedTmux: entry %d matches on an empty string", index)
			}
			if strings.ContainsAny(needle, "\n") {
				t.Fatalf("ScriptedTmux: entry %d matches on %q, which the script "+
					"cannot carry", index, needle)
			}
		}
		var condition strings.Builder
		condition.WriteString("true")
		for _, needle := range command.Contains {
			fmt.Fprintf(&condition,
				" && { needle=%s; match \"$@\"; }", shellQuote(needle))
		}
		fmt.Fprintf(&body, "if %s; then\n", condition.String())
		if command.Stdout != "" {
			fmt.Fprintf(&body, "  printf %%s %s\n", shellQuote(command.Stdout))
		}
		if command.Stderr != "" {
			fmt.Fprintf(&body, "  printf %%s %s >&2\n", shellQuote(command.Stderr))
		}
		fmt.Fprintf(&body, "  exit %d\nfi\n", command.ExitCode)
	}
	body.WriteString(
		"printf 'scripted tmux has no answer for: %s\\n' \"$*\" >&2\nexit 1\n")

	// Written without the executable bit and marked afterwards. A file still
	// open for writing anywhere in this process cannot be executed, and a
	// suite running tests in parallel forks often enough to hit that window,
	// which surfaces as "text file busy" rather than as anything to do with
	// the script.
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatalf("ScriptedTmux: write %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("ScriptedTmux: make %s executable: %v", path, err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("ScriptedTmux: resolve %s: %v", path, err)
	}
	return absolute
}

// shellQuote renders value as one single-quoted shell word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
