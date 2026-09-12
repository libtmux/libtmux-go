package tmux

import (
	"context"
	"strconv"
)

func exactPaneTarget(pane Pane) (string, error) {
	if _, err := validatePaneView(pane); err != nil {
		return "", err
	}
	return pane.sessionID.String() + ":" + strconv.Itoa(pane.windowIndex) + "." + pane.paneID.String(), nil
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
