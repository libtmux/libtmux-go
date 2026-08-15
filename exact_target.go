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
	if len(args) == 0 {
		return CommandResult{ExitCode: -1}, ErrMissingSubcommand
	}
	for _, argument := range args {
		if err := validateServerCommandArgument(
			"command", "Arguments", argument, true,
		); err != nil {
			return CommandResult{ExitCode: -1}, err
		}
	}
	target, err := exactPaneTarget(pane)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	arguments := make([]string, 0, len(args)+2)
	arguments = append(arguments, args[0], "-t", target)
	arguments = append(arguments, args[1:]...)
	return pane.server.literalCmd(ctx, arguments...)
}
