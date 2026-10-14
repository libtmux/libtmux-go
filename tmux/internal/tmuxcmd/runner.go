// Package tmuxcmd contains the private tmux process boundary.
package tmuxcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// defaultWaitDelay is how long a cancelled process, or one streaming into a
// caller's files, gets before exec closes its pipes under it.
// TestRunnerBoundsCancellationWhenDescendantHoldsOutputPipe pins it.
//
// Captured output does not go through a pipe at all, so the delay never
// decides whether a command that exited succeeded. Every tmux client hands a
// copy of its standard output to the server (client.c sends it with
// MSG_IDENTIFY_STDOUT), and the server closes that copy only once it has dealt
// with the client leaving (server_client_lost). On a loaded machine that came
// after the delay, so a pipe was still open when tmux had exited, and exec
// reported ErrWaitDelay for a command that had succeeded: about 2 runs in 100
// with 20 fresh servers at once on tmux 3.7d. A file has no other end to wait
// for.
const defaultWaitDelay = 100 * time.Millisecond

// Request describes one subprocess invocation.
type Request struct {
	Binary      string
	Arguments   []string
	Environment []string
	Directory   string
	// Stdio selects direct streaming when non-nil. Nil fields inherit the
	// corresponding process standard stream. A nil Stdio preserves captured
	// output behavior.
	Stdio *Stdio
}

// Stdio supplies concrete files for a streaming subprocess invocation.
type Stdio struct {
	Stdin         *os.File
	Stdout        *os.File
	Stderr        *os.File
	AttachSession bool
}

// Result contains a completed subprocess invocation.
type Result struct {
	Command   []string
	Stdout    []string
	Stderr    []string
	RawStdout []byte
	ExitCode  int
}

// OutcomeUnknownError reports a failure after tmux process execution began.
type OutcomeUnknownError struct {
	Err error
}

func (e *OutcomeUnknownError) Error() string { return e.Err.Error() }

func (e *OutcomeUnknownError) Unwrap() error { return e.Err }

// Runner executes tmux subprocess requests.
type Runner struct {
	WaitDelay time.Duration
}

// Run executes one request. Nonzero process exits are returned as Result data.
func (r Runner) Run(ctx context.Context, request Request) (Result, error) {
	binary := request.Binary
	defaultBinary := binary == ""
	if binary == "" {
		binary = "tmux"
	}
	arguments := slices.Clone(request.Arguments)
	result := Result{Command: append([]string{binary}, arguments...), ExitCode: -1}

	resolved, err := exec.LookPath(binary)
	if err != nil {
		return result, fmt.Errorf("resolve tmux executable %q: %w", binary, err)
	}
	if defaultBinary {
		result.Command[0] = resolved
	}

	attached := request.Stdio != nil && request.Stdio.AttachSession
	cmd := exec.CommandContext(ctx, resolved, arguments...)
	var interrupted atomic.Bool
	cancelProcess := cmd.Cancel
	cmd.Cancel = func() error {
		var err error
		if attached {
			err = cancelAttachedProcess(cmd.Process)
		} else {
			err = cancelProcess()
		}
		if err == nil {
			interrupted.Store(true)
		}
		return err
	}
	cmd.Env = request.Environment
	cmd.Dir = request.Directory
	cmd.WaitDelay = r.WaitDelay
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = defaultWaitDelay
	}
	var stdout, stderr *os.File
	if request.Stdio == nil {
		if stdout, err = captureFile(); err != nil {
			return result, fmt.Errorf("capture tmux output: %w", err)
		}
		defer releaseCaptureFile(stdout)
		if stderr, err = captureFile(); err != nil {
			return result, fmt.Errorf("capture tmux output: %w", err)
		}
		defer releaseCaptureFile(stderr)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
	} else {
		if request.Stdio.Stdin == nil {
			cmd.Stdin = os.Stdin
		} else {
			cmd.Stdin = request.Stdio.Stdin
		}
		if request.Stdio.Stdout == nil {
			cmd.Stdout = os.Stdout
		} else {
			cmd.Stdout = request.Stdio.Stdout
		}
		if request.Stdio.Stderr == nil {
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stderr = request.Stdio.Stderr
		}
	}

	runErr := cmd.Run()
	if request.Stdio == nil {
		printed, readErr := readCaptureFile(stdout)
		if readErr == nil {
			var warned []byte
			warned, readErr = readCaptureFile(stderr)
			result.Stderr = SplitStderr(warned)
		}
		if readErr != nil {
			return result, fmt.Errorf("read tmux output: %w", readErr)
		}
		result.RawStdout = printed
		result.Stdout = SplitStdout(printed)
	}

	outcome := processExitOutcome(cmd.ProcessState, interrupted.Load())
	if attached && interrupted.Load() && ctx.Err() != nil {
		outcome = processOutcomeCanceled
	}
	result.ExitCode = processResultExitCode(cmd.ProcessState, outcome)
	if err := classifyRunError(runErr, ctx.Err(), outcome); err != nil {
		return result, err
	}
	return result, nil
}

// captureFile returns an empty file for a process to write into.
func captureFile() (*os.File, error) {
	return os.CreateTemp("", "libtmux-go-tmux-*")
}

// readCaptureFile reads everything written to file since it was created.
func readCaptureFile(file *os.File) ([]byte, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

func releaseCaptureFile(file *os.File) {
	_ = file.Close()
	_ = os.Remove(file.Name())
}

func classifyRunError(runErr, contextErr error, outcome processOutcome) error {
	if runErr == nil {
		return nil
	}
	var exitError *exec.ExitError
	isExitError := errors.As(runErr, &exitError)
	if outcome == processOutcomeNatural &&
		(isExitError || errors.Is(runErr, contextErr)) {
		return nil
	}
	if isExitError && outcome != processOutcomeCanceled {
		return nil
	}
	if contextErr != nil {
		if outcome == processOutcomeCanceled {
			return &OutcomeUnknownError{Err: contextErr}
		}
		return contextErr
	}
	if isExitError {
		return nil
	}
	return fmt.Errorf("run tmux command: %w", runErr)
}

// SplitStdout decodes captured standard output into lines and drops the
// trailing blanks tmux's final delimiter produces. Transports other than this
// runner call it so one command reports the same decoded lines through any of
// them.
func SplitStdout(output []byte) []string {
	lines := strings.Split(DecodeBackslashReplace(output), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// SplitStderr decodes captured standard error into its nonempty lines.
func SplitStderr(output []byte) []string {
	lines := strings.Split(DecodeBackslashReplace(output), "\n")
	nonempty := lines[:0]
	for _, line := range lines {
		if line != "" {
			nonempty = append(nonempty, line)
		}
	}
	return nonempty
}

// DecodeBackslashReplace decodes UTF-8 and escapes each invalid byte as \xNN.
func DecodeBackslashReplace(output []byte) string {
	var decoded strings.Builder
	decoded.Grow(len(output))
	for len(output) > 0 {
		r, size := utf8.DecodeRune(output)
		if r == utf8.RuneError && size == 1 {
			const hexadecimal = "0123456789abcdef"
			decoded.WriteString(`\x`)
			decoded.WriteByte(hexadecimal[output[0]>>4])
			decoded.WriteByte(hexadecimal[output[0]&0x0f])
			output = output[1:]
			continue
		}
		decoded.WriteRune(r)
		output = output[size:]
	}
	return decoded.String()
}
