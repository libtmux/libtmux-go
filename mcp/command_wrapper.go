package mcp

import (
	"fmt"
	"strings"
)

const maximumInheritedTrapBytes = 64 * 1024

// wrapperPIDPath is where a wrapper records its own process, beside the
// record that says it has started. A run that was killed leaves no status,
// and its process being gone is then the only sign that it ended.
func wrapperPIDPath(openedPath string) string {
	return openedPath + ".pid"
}

// promptedPath is where a wrapper records that it found the cursor along a
// row the shell's prompt holds, beside the record that says it has started.
func promptedPath(openedPath string) string {
	return openedPath + ".prompted"
}

// wrapperScript renders the bookkeeping wrapper the pane runs. Bash and zsh
// trap declarations are carried as opaque code into the command subshell;
// bookkeeping runs with ERR, DEBUG, errexit, and xtrace disabled. After timeout
// cleanup removes the directory a trailing stderr redirection would be too
// late, because shells apply redirections left to right.
//
// The wrapper catches SIGINT. An interrupt reaches every process in the pane's
// foreground, the wrapper among them, and one that died with the command
// would record no status. A trap is not inherited, so the command still dies
// of the signal and the wrapper goes on to record that.
func wrapperScript(
	mark, column, openedPath, commandPath, trapPath, statusPath, closedPath, nonce string,
) string {
	command := shellQuote(commandPath)
	traps := shellQuote(trapPath)
	flags := "__libtmux_flags_" + nonce
	trapStatus := "__libtmux_trap_status_" + nonce
	trapBytes := "__libtmux_trap_bytes_" + nonce
	status := "__libtmux_status_" + nonce
	forget := "\\unset " + flags + " " + trapStatus + " " + trapBytes
	run := func(options string) string {
		return "  ( " + forget + "; " + options + "; . " + traps + " )\n"
	}
	return "(\n" +
		flags + "=$-\n" +
		"\\set +x\n" +
		"\\set +e\n" +
		trapStatus + "=0\n" +
		"\\umask 077\n" +
		// A child's parent is this subshell, which has no portable name
		// for its own process.
		publishRecord(`command sh -c 'command printf %s "$PPID"'`, wrapperPIDPath(openedPath)) +
		"if : >| " + traps + "; then\n" +
		"  case \"${BASH_VERSION-}:${ZSH_VERSION-}\" in\n" +
		"    ?*:*) if \\trap -p ERR DEBUG >| " + traps + "; then :; else " +
		trapStatus + "=125; fi; \\trap - ERR DEBUG ;;\n" +
		"    :?*) if \\trap >| " + traps + "; then :; else " +
		trapStatus + "=125; fi; \\trap - ERR DEBUG ;;\n" +
		"  esac\n" +
		"else " + trapStatus + "=125\n" +
		"fi\n" +
		"if command test \"$" + trapStatus + "\" -eq 0; then\n" +
		"  if " + trapBytes + "=$(command wc -c < " + traps + ") && " +
		"command test \"$" + trapBytes + "\" -le " + fmt.Sprint(maximumInheritedTrapBytes) +
		" 2>/dev/null; then :; else " + trapStatus + "=125; : >| " + traps + "; fi\n" +
		"fi\n" +
		"if command test \"$" + trapStatus + "\" -eq 0; then\n" +
		"  { command printf '\\n'; command cat " + command + "; } >> " + traps +
		" || " + trapStatus + "=125\n" +
		"fi\n" +
		"\\trap : INT\n" +
		// A shell with no line editor draws its prompt after the echo of keys
		// typed before it was ready, and a terminal that does not echo leaves
		// the cursor after the prompt: either way the cursor is along a row
		// that holds the prompt. The output starts on a row of its own
		// instead, and the record says the row above it is a prompt.
		"case \"$(" + column + " 2>/dev/null)\" in ''|0) ;; *) command printf '\\n'; : >| " +
		shellQuote(promptedPath(openedPath)) + " 2>/dev/null ;; esac\n" +
		publishRecord(mark, openedPath) +
		"if command test \"$" + trapStatus + "\" -ne 0; then\n" +
		"  " + status + "=125\n" +
		"else\n" +
		"  case \"$" + flags + "\" in\n" +
		"    *e*x*|*x*e*)\n" + run("\\set -e; \\set -x") +
		"    ;;\n" +
		"    *e*)\n" + run("\\set -e; \\set +x") +
		"    ;;\n" +
		"    *x*)\n" + run("\\set +e; \\set -x") +
		"    ;;\n" +
		"    *)\n" + run("\\set +e; \\set +x") +
		"    ;;\n" +
		"  esac\n" +
		"  " + status + "=$?\n" +
		"fi\n" +
		publishRecord(`command printf %s "$`+status+`"`, statusPath) +
		publishRecord(mark, closedPath) +
		"\\exit 0\n" +
		")\n"
}

// publishRecord renders one record written by producer and renamed into place,
// so a zero-time poll cannot observe the file after creation but before its
// payload. The brace group suppresses wrapper errors before the redirection.
func publishRecord(producer, path string) string {
	temporary := shellQuote(path + ".tmp")
	return fmt.Sprintf("{ %s > %s && command mv %s %s; } 2>/dev/null\n",
		producer, temporary, temporary, shellQuote(path))
}

// shellQuote makes a POSIX shell read one value as one word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
