// Package tmuxcmd contains the private tmux process boundary.
package tmuxcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

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
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

// Result contains a completed subprocess invocation.
type Result struct {
	Command   []string
	Stdout    []string
	Stderr    []string
	RawStdout []byte
	ExitCode  int
}

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

	cmd := exec.CommandContext(ctx, resolved, arguments...)
	var interrupted atomic.Bool
	cancelProcess := cmd.Cancel
	cmd.Cancel = func() error {
		err := cancelProcess()
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
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if request.Stdio == nil {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
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
		result.RawStdout = bytes.Clone(stdout.Bytes())
		result.Stdout = SplitStdout(stdout.Bytes())
		result.Stderr = SplitStderr(stderr.Bytes())
	}

	outcome := processExitOutcome(cmd.ProcessState, interrupted.Load())
	result.ExitCode = processResultExitCode(cmd.ProcessState, outcome)
	if err := classifyRunError(runErr, ctx.Err(), outcome); err != nil {
		return result, err
	}
	return result, nil
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
