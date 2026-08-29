package tmux

import (
	"context"
	"slices"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// Engine returns an [Engine] that carries tmux commands over the receiver's
// persistent connection. Pass it to [Server.WithEngine]:
//
//	client, err := server.OpenControl(ctx, session)
//	if err != nil {
//		return err
//	}
//	defer client.Close()
//	connected := server.WithEngine(client.Engine())
//
// The engine borrows the client; [ControlClient.Close] owns shutdown.
//
// It supports [CommandServer] only. Process commands and exact-byte reads follow
// the server's subprocess fallback policy because control replies use different
// framing.
//
// A %error frame maps to exit status 1 and Stderr. Successful control replies
// cannot preserve subprocess stderr because control mode has no error stream.
//
// [ControlClient.Cmd] serializes requests to one in-flight command.
func (c *ControlClient) Engine() Engine { return controlEngine{client: c} }

type controlEngine struct {
	client *ControlClient
}

// InstanceBound reports that a control connection cannot reach a replacement
// server: tmux ends a client with the server it attached to.
func (e controlEngine) InstanceBound() bool { return true }

// Supports reports that a control connection carries server commands only.
func (e controlEngine) Supports(kind CommandKind) bool { return kind == CommandServer }

// Run executes one tmux command over the control connection.
func (e controlEngine) Run(
	ctx context.Context,
	_ CommandKind,
	request CommandRequest,
) (CommandResult, error) {
	results, err := e.client.cmd(ctx, request.CommandList, request.Arguments...)
	if err != nil {
		return CommandResult{
			Command:  slices.Clone(request.Arguments),
			ExitCode: -1,
		}, err
	}
	return controlCommandResults(request.Arguments, results), nil
}

// String implements fmt.Stringer.
func (e controlEngine) String() string { return "control-mode" }

// controlCommandResults maps %error to exit 1 and stderr, and joins command-list
// frames to match subprocess output. Frames after a failure are absent.
func controlCommandResults(
	arguments []string,
	frames []ControlCommandResult,
) CommandResult {
	result := CommandResult{Command: slices.Clone(arguments)}
	var stdout []byte
	for _, frame := range frames {
		if frame.Failed {
			result.ExitCode = 1
			result.Stderr = tmuxcmd.SplitStderr(frame.RawStdout)
			break
		}
		stdout = append(stdout, frame.RawStdout...)
	}
	if len(stdout) != 0 {
		result.RawStdout = stdout
		result.Stdout = tmuxcmd.SplitStdout(stdout)
	}
	return result
}
