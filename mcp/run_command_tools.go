package mcp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// runCommandInput runs one command in a pane and waits for it to finish.
type runCommandInput struct {
	// PaneID is the tmux pane id, such as %1. Empty runs in the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to run the command in; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to run in when paneId is empty"`
	// Command is the POSIX-compatible shell command to run.
	Command string `json:"command" jsonschema:"the POSIX-compatible shell command to run"`
	// TimeoutSeconds bounds the non-detached operation. Zero uses a default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait before giving up"`
	// SuppressHistory keeps the command out of the shell's history, as it does
	// for send_keys. It covers the wrapper too, which is this package's own
	// bookkeeping and has no business in a person's history either.
	SuppressHistory bool `json:"suppressHistory,omitempty" jsonschema:"keep the command out of the shell's history by prefixing a space"`
	// Detach returns as soon as the command is typed, with a handle to collect
	// it by, instead of waiting for it to finish.
	Detach bool `json:"detach,omitempty" jsonschema:"return a jobId at once instead of waiting; collect it later with get_job"`
	// MaxLines caps the returned output, keeping the last lines.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines of output to return at most, keeping the last ones"`
	// MaxBytes caps the returned output's size, keeping the last lines.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes of output to return at most, keeping the last lines"`
}

// runCommandOutput reports how the command ended and what it wrote.
type runCommandOutput struct {
	// PaneID is the pane the command ran in.
	PaneID string `json:"paneId"`
	// ExitStatus is the command's exit status, absent when the command did not
	// finish. It is a pointer because zero is what a command reports when it
	// succeeded, so a timeout reported as zero would read as success to
	// anything branching on it.
	ExitStatus *int `json:"exitStatus,omitempty"`
	// TimedOut reports that the wait ended before the command did.
	TimedOut bool `json:"timedOut"`
	// Running is what the pane was running when the wait ended, reported only
	// on a timeout. A shell here means the command is still going; anything
	// else means the pane was busy and received the text as that program's
	// input instead of running it.
	Running string `json:"running,omitempty"`
	// Output is pane-rendered output between the command's cursor marks. Wrapped
	// terminal rows are rejoined and screen painting remains. Before tmux 3.6,
	// tabs are irreversibly expanded to spaces.
	Output []string `json:"output,omitempty"`
	// OutputUnavailable says why Output is missing, and is absent when the
	// command simply printed nothing. The status is the answer either way, so
	// failing to read the pane does not fail the call, but a caller branching
	// on empty output needs to know which of the two it has.
	OutputUnavailable string `json:"outputUnavailable,omitempty"`
	// LinesMissed reports that part of the output is gone rather than
	// truncated: the command erased tmux's scrollback, which renumbers the grid
	// the marks are recorded against, so whatever it printed before that cannot
	// be found. What it printed afterwards is still here. capture_since uses
	// the same word for the same thing.
	LinesMissed bool `json:"linesMissed,omitempty"`
	// EffectiveTimeoutSeconds is the budget this call actually used, which is
	// what timeoutSeconds asked for unless the server's ceiling was lower. It
	// is absent from a detached run, which waited for nothing.
	EffectiveTimeoutSeconds int `json:"effectiveTimeoutSeconds,omitempty"`
	// TimeoutClamped reports that the ceiling shortened the wait, so a caller
	// that asked for longer learns the policy from a reply rather than from a
	// failed call.
	TimeoutClamped bool `json:"timeoutClamped,omitempty"`
	// JobID is the handle a detached run is collected by, and is absent from
	// one that waited.
	JobID string `json:"jobId,omitempty"`
	// Detached reports that the command was left running, so nothing here
	// describes how it ended yet.
	Detached bool `json:"detached,omitempty"`
	// truncation reports what the bounds dropped from Output.
	truncation
}

// runCommand types a command at a shell prompt and waits for its commit record.
// A shared filesystem carries its exit status and cursor marks bound its output.
// If the pane is busy, the text reaches that program instead; timeout results
// report the pane's running command.
func (t *tools) runCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input runCommandInput,
) (*mcp.CallToolResult, runCommandOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, runCommandOutput{}, err
	}
	var owned *jobs
	if input.Detach {
		owned, err = t.sessionJobs(request)
		if err != nil {
			return nil, runCommandOutput{}, err
		}
	}
	output := runCommandOutput{}
	runCtx := ctx
	if !input.Detach {
		timeout, clamped := t.resolveWaitTimeout(input.TimeoutSeconds)
		output.EffectiveTimeoutSeconds = int(timeout.Seconds())
		output.TimeoutClamped = clamped
		var runCancel context.CancelFunc
		runCtx, runCancel = context.WithTimeout(ctx, timeout)
		defer runCancel()
		reporter := newProgressReporter(
			runCtx, request, timeout, "waiting for the command to finish")
		defer reporter.stop()
	}
	started, err := t.startCommand(runCtx, request, input, owned)
	if err != nil {
		if ctx.Err() != nil {
			return nil, output, ctx.Err()
		}
		if !input.Detach && isOwnWaitDeadline(ctx, runCtx, err) {
			output.TimedOut = true
			output.OutputUnavailable = "the effective timeout ended during command setup, " +
				"before pane output was collected"
			return nil, output, nil
		}
		return nil, output, err
	}
	output.PaneID = started.paneID.String()

	// A detached run is finished here. The handle is what collects it, and the
	// directory it records itself in outlives this call because of that.
	if input.Detach {
		output.JobID = started.id
		output.Detached = true
		return nil, output, nil
	}
	defer func() { _ = os.RemoveAll(started.directory) }()

	pane, err := t.tmux(runCtx).Pane(runCtx, started.paneID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, output, ctx.Err()
		}
		if isOwnWaitDeadline(ctx, runCtx, err) {
			return finishRunCommandDeadline(*started, output, "")
		}
		return nil, output, err
	}
	running, _ := pane.Formats().PaneCurrentCommand()
	return t.awaitCommand(ctx, runCtx, awaiting{
		pane:       pane,
		statusPath: started.statusAt,
		openedPath: started.openedAt,
		closedPath: started.closedAt,
		limits:     limits,
		running:    running,
		output:     output,
	})
}

// startCommand types a command into a pane and returns the handle that
// identifies it, without waiting for anything.
//
// It is separate from waiting because the two are independent: the wrapper
// records the same status whether or not this process is the one that reads it,
// so detaching changes who waits and nothing else.
func (t *tools) startCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input runCommandInput,
	owned *jobs,
) (*job, error) {
	if strings.TrimSpace(input.Command) == "" {
		return nil, errors.New("command is required")
	}
	server := t.tmux(ctx)
	// Resolved for delivery, which refuses a pane that cannot read the keys
	// before the pane is asked to do anything. Either refusal saves this tool
	// the whole timeoutSeconds the caller set: a mode reads the command as key
	// bindings, one of which takes a pending key and never answers the sending
	// client, and a pane with no process never runs the wrapper at all.
	pane, err := t.resolvePaneToDeliver(
		ctx, request, input.PaneID, input.SessionName, "running a command", "run_command")
	if err != nil {
		return nil, err
	}
	if shell := incompatibleRunCommandShell(pane); shell != "" {
		return nil, fmt.Errorf(
			"run_command requires a POSIX-compatible pane shell; pane %s is running %s; "+
				"use send_keys or respawn_pane with a compatible shell",
			pane.ID(), shell,
		)
	}

	socket, err := server.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil {
		return nil, err
	}
	if len(socket.Stdout) == 0 || socket.Stdout[0] == "" {
		return nil, errors.New("tmux did not report its socket path")
	}
	tmuxExecutable := server.Executable()

	directory, err := os.MkdirTemp("", "libtmux-mcp-run")
	if err != nil {
		return nil, err
	}

	jobID := "libtmux-mcp-" + rand.Text()
	statusPath := filepath.Join(directory, "status")
	openedPath := filepath.Join(directory, "opened")
	closedPath := filepath.Join(directory, "closed")
	commandPath := filepath.Join(directory, "command")

	// In-pane marks exclude shell echo; the closing column distinguishes a
	// newline from output ending mid-row. Files hide markers from the pane, and
	// sourcing the caller's script inside a subshell keeps its syntax and an
	// `exit` command from changing the bookkeeping wrapper's structure.
	mark := fmt.Sprintf(
		"%s -S %s display-message -p -t %s "+
			"'#{history_size} #{cursor_y} #{cursor_x} #{pane_width} #{pane_height}'",
		shellQuote(tmuxExecutable),
		shellQuote(socket.Stdout[0]),
		shellQuote(pane.ID().String()),
	)
	script := wrapperScript(mark, openedPath, commandPath, statusPath, closedPath)

	if err := os.WriteFile(commandPath, []byte(input.Command+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	// Source the wrapper so tabs and control bytes bypass the shell's line
	// editor; only this package-controlled path is typed into the pane.
	scriptPath := filepath.Join(directory, "script")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	started := &job{
		id:        jobID,
		paneID:    pane.ID(),
		command:   input.Command,
		directory: directory,
		openedAt:  openedPath,
		closedAt:  closedPath,
		statusAt:  statusPath,
		started:   time.Now(),
	}
	if owned != nil {
		if err := owned.keep(started); err != nil {
			_ = os.RemoveAll(directory)
			return nil, err
		}
	}
	sourceScript := ". " + shellQuote(scriptPath)
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command:         &sourceScript,
		SuppressHistory: input.SuppressHistory,
	}); err != nil {
		if owned != nil {
			owned.discard(started.id)
		} else {
			_ = os.RemoveAll(directory)
		}
		return nil, err
	}
	return started, nil
}

// Unknown commands retain the public busy-pane timeout behavior. Only shells
// whose syntax is known to reject the wrapper fail before delivery.
func incompatibleRunCommandShell(pane tmux.Pane) string {
	command, ok := pane.Formats().PaneCurrentCommand()
	if !ok || command == "" {
		return ""
	}
	if slices.Contains(nonPOSIXShells, shellName(command)) {
		return command
	}
	return ""
}

// nonPOSIXShells reject the bookkeeping wrapper's syntax; posixShells run it
// once they are reading keys. Both are shells, so both explain a pane that has
// not started a command yet rather than one that swallowed it.
var (
	nonPOSIXShells = []string{
		"csh", "elvish", "fish", "nu", "nushell", "powershell", "pwsh", "tcsh",
	}
	posixShells = []string{
		"ash", "bash", "dash", "ksh", "ksh93", "mksh", "pdksh", "sh", "zsh",
	}
)

// shellName reduces a pane's current command to a comparable shell name,
// dropping the login shell's leading hyphen.
func shellName(command string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Base(command), "-"))
}

// runsAShell reports whether a pane's current command reads typed keys once it
// is ready. Anything else has taken them as its own input.
func runsAShell(running string) bool {
	name := shellName(running)
	return slices.Contains(posixShells, name) || slices.Contains(nonPOSIXShells, name)
}

// awaiting is what a wait for one command needs to know, gathered so that the
// wait reads as one step rather than eight parameters.
type awaiting struct {
	pane                               tmux.Pane
	statusPath, openedPath, closedPath string
	limits                             bounds
	running                            string
	output                             runCommandOutput
}

// awaitCommand blocks until a started command publishes its commit record, and
// reads back its status and the rows it wrote.
func (t *tools) awaitCommand(
	ctx context.Context,
	waitCtx context.Context,
	waiting awaiting,
) (*mcp.CallToolResult, runCommandOutput, error) {
	output := waiting.output

	completion := job{
		openedAt: waiting.openedPath,
		statusAt: waiting.statusPath,
		closedAt: waiting.closedPath,
	}
	status, ready, err := waitForCompletedJob(waitCtx, completion)
	if err == nil && ready {
		return t.finishAwaitedCommand(ctx, waitCtx, waiting, output, status)
	}
	if ctx.Err() != nil {
		return nil, output, ctx.Err()
	}
	if !isOwnWaitDeadline(ctx, waitCtx, err) {
		return nil, output, err
	}
	return finishRunCommandDeadline(completion, output, waiting.running)
}

func (t *tools) finishAwaitedCommand(
	ctx context.Context,
	waitCtx context.Context,
	waiting awaiting,
	output runCommandOutput,
	status int,
) (*mcp.CallToolResult, runCommandOutput, error) {
	output.ExitStatus = &status
	if outputErr := t.attachCommandOutput(waitCtx, waiting.pane,
		waiting.openedPath, waiting.closedPath, waiting.limits, &output); outputErr != nil {
		if isOwnWaitDeadline(ctx, waitCtx, outputErr) {
			output.Output = nil
			output.OutputUnavailable = "the effective timeout ended before pane output was collected"
			return nil, output, nil
		}
		return nil, output, outputErr
	}
	return nil, output, nil
}

func finishRunCommandDeadline(
	completion job,
	output runCommandOutput,
	running string,
) (*mcp.CallToolResult, runCommandOutput, error) {
	status, ready, err := readCompletedJob(completion)
	if err != nil {
		return nil, output, err
	}
	if ready {
		output.ExitStatus = &status
	} else {
		output.TimedOut = true
		output.Running = running
	}
	output.OutputUnavailable = "the effective timeout ended before pane output was collected"
	if reason := unstartedReason(completion.openedAt, running); reason != "" {
		output.OutputUnavailable = reason
	}
	return nil, output, nil
}
