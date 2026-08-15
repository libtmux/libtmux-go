package tmux

import (
	"context"
	"errors"
)

// ErrMissingSubcommand identifies an object command with no tmux subcommand.
var ErrMissingSubcommand = errors.New("tmux: subcommand is required")

// Cmd executes a tmux subcommand targeted to the session's stable ID.
func (s Session) Cmd(ctx context.Context, args ...string) (CommandResult, error) {
	return scopedCommand(ctx, s.server, "session", s.sessionID.String(), args)
}

// Cmd executes a tmux subcommand targeted to the window's stable ID.
func (w Window) Cmd(ctx context.Context, args ...string) (CommandResult, error) {
	return scopedCommand(ctx, w.server, "window", w.windowID.String(), args)
}

// Cmd executes a tmux subcommand targeted to the pane's stable ID.
func (p Pane) Cmd(ctx context.Context, args ...string) (CommandResult, error) {
	return scopedCommand(ctx, p.server, "pane", p.paneID.String(), args)
}

func (s Session) literalCmd(ctx context.Context, args ...string) (CommandResult, error) {
	return literalScopedCommand(ctx, s.server, "session", s.sessionID.String(), args)
}

func (w Window) literalCmd(ctx context.Context, args ...string) (CommandResult, error) {
	return literalScopedCommand(ctx, w.server, "window", w.windowID.String(), args)
}

func (p Pane) literalCmd(ctx context.Context, args ...string) (CommandResult, error) {
	return literalPaneCommand(ctx, p, args)
}

func scopedCommand(
	ctx context.Context,
	server Server,
	object string,
	target string,
	args []string,
) (CommandResult, error) {
	if len(args) == 0 {
		return CommandResult{ExitCode: -1}, ErrMissingSubcommand
	}
	if err := validateTypedTarget(args[0], "Target", object, target); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	arguments := make([]string, 0, len(args)+2)
	arguments = append(arguments, args[0], "-t", target)
	arguments = append(arguments, args[1:]...)
	return server.Cmd(ctx, arguments...)
}

func literalScopedCommand(
	ctx context.Context,
	server Server,
	object string,
	target string,
	args []string,
) (CommandResult, error) {
	if len(args) == 0 {
		return CommandResult{ExitCode: -1}, ErrMissingSubcommand
	}
	if err := validateServerCommandArgument(
		"command", "Target", target, true,
	); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	for _, argument := range args {
		if err := validateServerCommandArgument(
			"command", "Arguments", argument, true,
		); err != nil {
			return CommandResult{ExitCode: -1}, err
		}
	}
	if err := validateTypedTarget(args[0], "Target", object, target); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	arguments := make([]string, 0, len(args)+2)
	arguments = append(arguments, args[0], "-t", target)
	arguments = append(arguments, args[1:]...)
	return server.literalCmd(ctx, arguments...)
}
