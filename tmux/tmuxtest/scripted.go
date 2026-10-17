package tmuxtest

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
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
//
// The private version probe reports no daemon unless an entry explicitly
// matches its complete format argument. An entry that does so answers with the
// record in Stdout; the script reports it the way tmux does, as the refusal of
// the probe's random command word on stderr. Ordinary commands then use the
// executable version supplied by the script's -V answer.
func ScriptedTmux(t testing.TB, script ...ScriptedCommand) string {
	t.Helper()

	const versionProbeFormat = "#{q:pid}|#{q:start_time}|#{q:socket_path}|#{q:version}="
	const probeFormat = "#{q:pid}|#{q:start_time}|#{q:version}|"
	if !slices.ContainsFunc(script, func(command ScriptedCommand) bool {
		return slices.Contains(command.Contains, versionProbeFormat)
	}) {
		script = append([]ScriptedCommand{{
			Contains: []string{versionProbeFormat},
			Stderr:   "no server running on scripted socket\n",
			ExitCode: 1,
		}}, script...)
	}

	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	// The hook-free probe is run-shell -C <marker><format>; the marker is
	// random, so the script recovers it from the argument.
	body.WriteString("marker=\n")
	body.WriteString("for argument in \"$@\"; do\n")
	fmt.Fprintf(&body, "  case $argument in libtmux-version-*%s*) marker=${argument%%%%'#{'*};; esac\n",
		shellQuote(probeFormat))
	body.WriteString("done\n")
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
		probe := false
		for _, needle := range command.Contains {
			if needle == versionProbeFormat {
				probe = true
				condition.WriteString(` && [ -n "$marker" ]`)
				continue
			}
			fmt.Fprintf(&condition,
				" && { needle=%s; match \"$@\"; }", shellQuote(needle))
		}
		fmt.Fprintf(&body, "if %s; then\n", condition.String())
		if probe && command.Stdout != "" {
			fmt.Fprintf(&body, "  printf 'unknown command: %%s%%s\\n' \"$marker\" %s >&2\n",
				shellQuote(probeRecord(command.Stdout)))
			body.WriteString("  exit 1\nfi\n")
			continue
		}
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

	// A child process writes the file, never a descriptor of this one. A fork
	// anywhere in this process inherits every open descriptor until it execs,
	// and executing a file some process holds open for writing fails with
	// "text file busy"; marking the file executable only after closing it
	// does not help, since the fork can still be holding it.
	path := filepath.Join(t.TempDir(), "tmux")
	write := exec.Command("/bin/sh", "-c", `umask 077 && cat >"$1" && chmod 700 "$1"`, "sh", path)
	write.Stdin = strings.NewReader(body.String())
	if output, err := write.CombinedOutput(); err != nil {
		t.Fatalf("ScriptedTmux: write %s: %v: %s", path, err, output)
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

// probeRecord reorders a scripted pid|start_time|socket_path|version= record
// into the order the probe reports: the socket path, whose bytes are
// unconstrained, goes last.
func probeRecord(stdout string) string {
	record := strings.TrimSuffix(strings.TrimSuffix(stdout, "\n"), "=")
	parts := strings.SplitN(record, "|", 4)
	if len(parts) != 4 {
		return strings.TrimSuffix(stdout, "\n")
	}
	return parts[0] + "|" + parts[1] + "|" + parts[3] + "|" + parts[2] + "="
}
