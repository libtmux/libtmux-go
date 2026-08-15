package tmux

import (
	"context"
	"slices"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// Engine returns an [Engine] that carries tmux commands over the receiver's
// persistent control-mode connection instead of starting a tmux process for
// each one. Pass it to [Server.WithEngine] to make the object API use it:
//
//	client, err := server.OpenControl(ctx, session)
//	if err != nil {
//		return err
//	}
//	defer client.Close()
//	connected := server.WithEngine(client.Engine())
//
// The engine borrows the client rather than owning it: [ControlClient.Close]
// stops the underlying process, and commands issued afterwards report
// [ErrControlClosed] as transport failures, which a lenient collection read
// normalizes exactly as it normalizes a dead tmux server.
//
// It supports [CommandServer] only. Interactive attachment and the tmux -V
// version probe need their own process and keep starting one, as do the reads
// whose documented result is tmux's exact stdout bytes: [Pane.Capture],
// [Pane.CaptureBytes], and [Server.ShowBufferBytes] preserve tmux's process
// output, while [ControlCommandResult.RawStdout] preserves tmux's control
// rendering of a reply, and this package does not translate one into the other.
//
// A tmux %error frame becomes exit status 1 with the tmux message in
// [CommandResult.Stderr], which is what the same failure looks like through a
// tmux process, so [CommandError] and its missing-target classification behave
// identically through either transport. Control mode has no separate error
// stream, so a command that succeeds while writing a diagnostic reports empty
// Stderr here and a nonempty one through a process.
//
// [ControlClient.Cmd] serializes concurrent requests, so concurrent callers of
// a Server holding this engine see one in-flight tmux command at a time.
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

// controlCommandResults renders the frames tmux returned for one command line
// as a process-shaped result. tmux exits 1 for every command error and reports
// the reason on stderr, so a %error frame is mapped to that pair rather than to
// a distinct signal.
//
// A command list produces one frame per command, which a tmux process would
// have merged into one stdout with no boundary between them. They are joined
// here for the same reason: an operation reads the same result through either
// transport. The frames tmux stopped short of sending, after a failure, are
// simply absent -- their commands did not run.
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
