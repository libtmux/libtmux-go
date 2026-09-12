package tmux

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// streamIn runs one command with tmux reading arguments' stdin operand from
// source, so source's bytes never pass through this process's memory. The
// returned result carries the command's exit status and stderr; the error
// reports a transport failure first and a copy failure second, because a copy
// that failed after tmux exited zero means tmux stored only a prefix.
func (s Server) streamIn(
	ctx context.Context,
	source io.Reader,
	arguments []string,
) (CommandResult, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return CommandResult{ExitCode: -1}, fmt.Errorf(
			"%s: open stdin pipe: %w", arguments[0], err,
		)
	}
	copied := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(write, source)
		closeErr := write.Close()
		copied <- errors.Join(copyErr, closeErr)
	}()

	result, runErr := s.runPipeStreamCommand(ctx, read, nil, arguments)
	// Closing this end is what ends a copy blocked on writing to a tmux that
	// has already exited.
	_ = read.Close()
	copyErr := awaitCopy(ctx, copied)
	switch {
	case runErr != nil:
		return result, runErr
	case copyErr != nil:
		return result, fmt.Errorf("%s: copy stdin: %w", arguments[0], copyErr)
	default:
		return result, nil
	}
}

// streamOut runs one command with tmux writing arguments' stdout operand into
// destination, so the payload never passes through this process's memory. The
// error reports a transport failure first and a copy failure second, because a
// copy that failed after tmux exited zero means destination never saw
// everything tmux sent.
func (s Server) streamOut(
	ctx context.Context,
	destination io.Writer,
	arguments []string,
) (CommandResult, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return CommandResult{ExitCode: -1}, fmt.Errorf(
			"%s: open stdout pipe: %w", arguments[0], err,
		)
	}
	copied := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(destination, read)
		copied <- copyErr
	}()

	result, runErr := s.runPipeStreamCommand(ctx, nil, write, arguments)
	// Closing this end is what ends the copy with io.EOF once tmux has written
	// everything it had.
	_ = write.Close()
	copyErr := awaitCopy(ctx, copied)
	_ = read.Close()
	switch {
	case runErr != nil:
		return result, runErr
	case copyErr != nil:
		return result, fmt.Errorf("%s: copy stdout: %w", arguments[0], copyErr)
	default:
		return result, nil
	}
}

// streamOut runs one pane-scoped command with tmux writing its stdout into
// destination, addressed to this pane's exact target.
func (p Pane) streamOut(
	ctx context.Context,
	destination io.Writer,
	args []string,
) (CommandResult, error) {
	arguments, err := exactPaneArguments(p, args)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return p.server.requireProcess().streamOut(ctx, destination, arguments)
}

// awaitCopy waits for a finished copy, preferring its outcome over an expired
// context so a completed transfer is never reported as a cancellation. A
// source or destination that blocks regardless of cancellation cannot hold the
// call: ctx ends the wait, and the copy ends when that value does.
func awaitCopy(ctx context.Context, copied <-chan error) error {
	select {
	case err := <-copied:
		return err
	default:
	}
	select {
	case err := <-copied:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// runPipeStreamCommand runs one subprocess-bound command with stdin and
// stdout wired to the given pipe ends. A nil end is wired to the null device
// rather than inherited, so a tmux subprocess never reads or writes this
// process's own standard streams. It captures stderr through its own pipe
// because [tmuxcmd.Runner] collects none for a streaming invocation, which
// would otherwise reduce every streaming failure to a bare exit status.
func (s Server) runPipeStreamCommand(
	ctx context.Context,
	stdin, stdout *os.File,
	arguments []string,
) (CommandResult, error) {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return CommandResult{ExitCode: -1}, fmt.Errorf(
			"%s: open %s: %w", arguments[0], os.DevNull, err,
		)
	}
	defer func() { _ = null.Close() }()

	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return CommandResult{ExitCode: -1}, fmt.Errorf(
			"%s: open stderr pipe: %w", arguments[0], err,
		)
	}
	collected := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(stderrRead)
		collected <- data
	}()

	result, runErr := s.streamingLiteralCmd(ctx, tmuxcmd.Stdio{
		Stdin:  cmp.Or(stdin, null),
		Stdout: cmp.Or(stdout, null),
		Stderr: stderrWrite,
	}, arguments...)

	_ = stderrWrite.Close()
	result.Stderr = tmuxcmd.SplitStderr(<-collected)
	_ = stderrRead.Close()
	return result, runErr
}
