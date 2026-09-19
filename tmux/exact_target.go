package tmux

import (
	"context"
	"strconv"
	"strings"
)

// exactPaneTarget names the pane within its session and lets tmux find its
// window, so a pane whose window was renumbered, or which moved to another
// window, is still the one reached.
func exactPaneTarget(pane Pane) (string, error) {
	if _, err := validatePaneView(pane); err != nil {
		return "", err
	}
	if inSession, _ := windowLinks(pane.formats); inSession > 1 {
		return pane.sessionID.String() + ":" + strconv.Itoa(pane.windowIndex) + "." + pane.paneID.String(), nil
	}
	return pane.sessionID.String() + ":." + pane.paneID.String(), nil
}

// exactWindowTarget names the one winlink the record views with the shortest
// target that still names only it. An index names whatever window holds it
// now, so it is used only where an id cannot say which link is meant. A
// session is added only when the window is linked into more than one: tmux
// resolves a missing @id within a named session to that session's current
// window for the commands that tolerate a failed target, such as set-option.
func exactWindowTarget(window Window) (string, error) {
	if _, err := validateWindowView(window); err != nil {
		return "", err
	}
	switch inSession, total := windowLinks(window.formats); {
	case inSession > 1:
		return window.sessionID.String() + ":" + strconv.Itoa(window.windowIndex), nil
	case total == 1:
		return window.windowID.String(), nil
	default:
		return window.sessionID.String() + ":" + window.windowID.String(), nil
	}
}

// windowLinks counts the record's window links in its own session and in all,
// from #{window_linked_sessions_list}, which names a session once per link.
// Both are zero when the record did not carry the formats.
func windowLinks(formats formatValues) (inSession, total int) {
	session, ok := formats.getString("session_name")
	if !ok {
		return 0, 0
	}
	linked, ok := formats.getString("window_linked_sessions_list")
	if !ok {
		return 0, 0
	}
	for name := range strings.SplitSeq(linked, ",") {
		total++
		if name == session {
			inSession++
		}
	}
	return inSession, total
}

func literalPaneCommand(
	ctx context.Context,
	pane Pane,
	args []string,
) (CommandResult, error) {
	arguments, err := exactPaneArguments(pane, args)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return pane.server.literalCmd(ctx, arguments...)
}

// exactPaneArguments validates args and returns them addressed to this pane's
// exact target. Every pane-scoped command builds its argument vector here, so
// a command that reaches tmux another way cannot quietly lose its target and
// act on whichever pane tmux considers current.
func exactPaneArguments(pane Pane, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, ErrMissingSubcommand
	}
	for _, argument := range args {
		if err := validateServerCommandArgument(
			"command", "Arguments", argument, true,
		); err != nil {
			return nil, err
		}
	}
	target, err := exactPaneTarget(pane)
	if err != nil {
		return nil, err
	}
	arguments := make([]string, 0, len(args)+2)
	arguments = append(arguments, args[0], "-t", target)
	arguments = append(arguments, args[1:]...)
	return arguments, nil
}
