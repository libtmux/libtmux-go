package tmux

import (
	"slices"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

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
