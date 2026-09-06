package mcp

import (
	"fmt"
	"strings"
)

const maximumInheritedTrapBytes = 64 * 1024

// wrapperScript renders the bookkeeping wrapper the pane runs. Bash and zsh
// trap declarations are carried as opaque code into the command subshell;
// bookkeeping runs with ERR, DEBUG, errexit, and xtrace disabled. After timeout
// cleanup removes the directory a trailing stderr redirection would be too
// late, because shells apply redirections left to right.
func wrapperScript(
	mark, openedPath, commandPath, trapPath, statusPath, closedPath, nonce string,
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
