package mcp

import (
	"fmt"
	"strings"
)

// wrapperScript renders the bookkeeping wrapper the pane runs. Sourcing the
// caller's script inside a subshell keeps its syntax, and an `exit` in it, from
// changing this structure. After timeout cleanup removes the directory a
// trailing stderr redirection would be too late, because shells apply
// redirections left to right; command stderr stays captured.
func wrapperScript(mark, openedPath, commandPath, statusPath, closedPath string) string {
	command := shellQuote(commandPath)
	return "(\n" +
		"case $- in *e*) __libtmux_errexit=1 ;; *) __libtmux_errexit=0 ;; esac\n" +
		"set +e\n" +
		publishRecord(mark, openedPath) +
		"if [ \"$__libtmux_errexit\" -eq 1 ]; then\n" +
		"  ( set -e; . " + command + " )\n" +
		"else\n" +
		"  ( set +e; . " + command + " )\n" +
		"fi\n" +
		"__libtmux_status=$?\n" +
		publishRecord(`printf %s "$__libtmux_status"`, statusPath) +
		publishRecord(mark, closedPath) +
		"exit 0\n" +
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
