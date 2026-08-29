package mcp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	statusTemp := statusPath + ".tmp"
	openedTemp := openedPath + ".tmp"
	closedTemp := closedPath + ".tmp"

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
	// Publish each record by rename so a zero-time poll cannot observe a file
	// after creation but before its small payload has been written. Brace groups
	// suppress wrapper errors before inner file redirections. After timeout
	// cleanup removes the directory, trailing stderr redirection is too late
	// because shells apply redirections left to right. Command stderr remains
	// captured.
	script := fmt.Sprintf(
		"(\n"+
			"case $- in *e*) __libtmux_errexit=1 ;; *) __libtmux_errexit=0 ;; esac\n"+
			"set +e\n"+
			"{ %s > %s && command mv %s %s; } 2>/dev/null\n"+
			"if [ \"$__libtmux_errexit\" -eq 1 ]; then\n"+
			"  ( set -e; . %s )\n"+
			"else\n"+
			"  ( set +e; . %s )\n"+
			"fi\n"+
			"__libtmux_status=$?\n"+
			"{ printf %%s \"$__libtmux_status\" > %s && command mv %s %s; } 2>/dev/null\n"+
			"{ %s > %s && command mv %s %s; } 2>/dev/null\n"+
			"exit 0\n"+
			")\n",
		mark,
		shellQuote(openedTemp),
		shellQuote(openedTemp),
		shellQuote(openedPath),
		shellQuote(commandPath),
		shellQuote(commandPath),
		shellQuote(statusTemp),
		shellQuote(statusTemp),
		shellQuote(statusPath),
		mark,
		shellQuote(closedTemp),
		shellQuote(closedTemp),
		shellQuote(closedPath),
	)

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
	name := strings.ToLower(strings.TrimPrefix(filepath.Base(command), "-"))
	switch name {
	case "csh", "elvish", "fish", "nu", "nushell", "powershell", "pwsh", "tcsh":
		return command
	default:
		return ""
	}
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

// attachCommandOutput preserves a recorded exit status when capture fails and
// reports why output is unavailable. A missing closing mark reads to screen end.
func (t *tools) attachCommandOutput(
	ctx context.Context,
	pane tmux.Pane,
	openedPath, closedPath string,
	limits bounds,
	output *runCommandOutput,
) error {
	processPane, err := t.processPane(ctx, pane)
	if err != nil {
		if t.runtime.isTerminalError(err) || isContextError(err) {
			return err
		}
		output.OutputUnavailable = err.Error()
		return nil
	}
	pane = processPane
	opened, err := readMark(openedPath)
	if err != nil {
		reason, reasonErr := t.markMissing(ctx, pane, err)
		if reasonErr != nil {
			return reasonErr
		}
		output.OutputUnavailable = reason
		return nil
	}
	now, err := readPaneState(ctx, pane)
	if err != nil {
		if t.runtime.isTerminalError(err) || isContextError(err) {
			return err
		}
		output.OutputUnavailable = err.Error()
		return nil
	}
	// Convert absolute marks to current grid rows; tmux renumbers trimmed history.
	request := tmux.CapturePaneRequest{
		Start:       tmux.CaptureLine(opened.row - now.historySize),
		JoinWrapped: true,
	}
	// rows is how many rows the command wrote, known only where the closing
	// mark bounded the read.
	rows := 0
	// The closing row belongs to the output only when the cursor stopped part
	// way along it. A cursor at column zero means the last line ended, so that
	// row holds whatever the shell drew next, and reading it returns a prompt
	// as though the command had printed one.
	if closed, err := readMark(closedPath); err == nil {
		end := closed.row
		if closed.column == 0 {
			end--
		}
		if closed.row < opened.row || closed.moved(opened) {
			// Renumbering invalidates the opening mark. Report loss only when
			// scrollback was erased rather than moved into history.
			output.LinesMissed = closed.erased(opened) || closed.row < opened.row
			request.Start = tmux.CaptureLine(-now.historySize)
			if end < 0 {
				output.Output = nil
				return nil
			}
			request.End = tmux.CaptureLine(end - now.historySize)
		} else {
			if end < opened.row {
				// The cursor finished where it started, so the command printed
				// nothing. That is an answer rather than a failure.
				output.Output = nil
				return nil
			}
			request.End = tmux.CaptureLine(end - now.historySize)
			rows = end - opened.row + 1
		}
	}
	lines, err := pane.Capture(ctx, request)
	if err != nil {
		if t.runtime.isTerminalError(err) || isContextError(err) {
			return err
		}
		output.OutputUnavailable = err.Error()
		return nil
	}
	// tmux collapses an all-blank capture, so recover its marked row count.
	if len(lines) == 0 && rows > 0 {
		lines = make([]string, rows)
	}
	// tmux before 3.4 keeps grid padding after rejoining wrapped rows.
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}
	// Grid movement can put the wrapper echo before captured output on any path.
	lines = afterTheWrapperEcho(lines, sourceScriptFor(openedPath))
	kept, report := limits.apply(lines)
	output.Output = kept
	output.truncation = report
	return nil
}

// moved reports grid renumbering at a stable pane size. Resize-induced history
// changes are not comparable.
func (f mark) moved(opened mark) bool {
	if f.width != opened.width || f.height != opened.height {
		return false
	}
	return f.historySize != opened.historySize && f.row <= opened.row
}

// erased reports scrollback loss only while pane dimensions remain comparable.
func (f mark) erased(opened mark) bool {
	return f.moved(opened) && f.historySize < opened.historySize
}

// afterTheWrapperEcho removes the sourced wrapper line, including an echo split
// across wrapped rows.
func afterTheWrapperEcho(lines []string, echo string) []string {
	// Compared without spaces. A wrapped row that breaks on one of the echo's
	// own spaces loses it to the padding trim, which cannot tell a space the
	// grid added from a space the shell wrote.
	compacted := make([]string, len(lines))
	joined := strings.Builder{}
	ends := make([]int, len(lines))
	for i, line := range lines {
		compacted[i] = withoutSpaces(line)
		joined.WriteString(compacted[i])
		ends[i] = joined.Len()
	}
	wanted := withoutSpaces(echo)
	last := -1
	if at := strings.LastIndex(joined.String(), wanted); at >= 0 {
		for i, end := range ends {
			if end >= at+len(wanted) {
				last = i
				break
			}
		}
	}
	// Drop only full-row echo suffixes; suffixes inside ordinary output remain.
	for i := last + 1; i < len(lines); i++ {
		row := compacted[i]
		if len(row) >= echoRemnant && strings.HasSuffix(wanted, row) {
			last = i
		}
	}
	if last < 0 {
		// A shell that did not echo leaves no wrapper line to remove.
		return lines
	}
	// Recovery-only blank rows precede the command output.
	rest := lines[last+1:]
	for len(rest) > 0 && rest[0] == "" {
		rest = rest[1:]
	}
	return rest
}

// echoRemnant is the shortest observed shell-redraw suffix, including its
// closing quote.
const echoRemnant = 6

// withoutSpaces is the form rows and the echo are compared in.
func withoutSpaces(text string) string {
	return strings.ReplaceAll(text, " ", "")
}

// sourceScriptFor rebuilds the line the wrapper typed into the pane, so what is
// looked for in the grid is what was sent rather than a second spelling of it.
func sourceScriptFor(openedPath string) string {
	return ". " + shellQuote(filepath.Join(filepath.Dir(openedPath), "script"))
}

// mark is where the pane's cursor stood when the wrapper recorded it.
type mark struct {
	// historySize is how much scrollback stood above the screen, kept apart
	// from the row because an erase can drop a line of history while the
	// cursor moves down one: the sum is unchanged and the erase invisible.
	historySize int
	// width and height are the pane's size, which says whether historySize is
	// comparable at all. tmux rewraps the scrollback when a pane changes
	// width and moves rows between it and the screen when the height changes,
	// so the count moves on its own with nothing erased.
	width, height int
	// row is an absolute position in tmux's grid, being the history size plus
	// the cursor's row, which does not move when tmux renumbers the grid.
	row int
	// column is the cursor's column. Zero means the line before it ended.
	column int
}

// markMissing translates an absent opening mark into a pane-level diagnostic;
// the wrapper never ran.
func (t *tools) markMissing(ctx context.Context, pane tmux.Pane, err error) (string, error) {
	if !errors.Is(err, os.ErrNotExist) {
		return err.Error(), nil
	}
	running := ""
	if fresh, freshErr := pane.Refresh(ctx); freshErr == nil {
		running, _ = fresh.Formats().PaneCurrentCommand()
	} else if t.runtime.isTerminalError(freshErr) || isContextError(freshErr) {
		return "", freshErr
	}
	return commandNeverRanReason(running), nil
}

func commandNeverRanReason(running string) string {
	if running == "" {
		return "the command recorded no start: the pane has not read the keys. " +
			"A shell that is still starting reads them late; a program holding " +
			"the pane never does"
	}
	return fmt.Sprintf("the pane never ran the command: it is running %s, "+
		"which took the text as its own input rather than running it; "+
		"respawn_pane gives the pane a shell again", running)
}

// unstartedReason explains a job whose wrapper recorded no opening mark. It is
// empty once the wrapper has run, so a caller can tell a slow command from one
// the pane never took.
func unstartedReason(openedAt, running string) string {
	if _, err := readMark(openedAt); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return commandNeverRanReason(running)
}

// readMark parses one cursor position recorded by the wrapper.
func readMark(path string) (mark, error) {
	recorded, err := os.ReadFile(path)
	if err != nil {
		return mark{}, err
	}
	fields := strings.Fields(string(recorded))
	if len(fields) != 5 {
		return mark{}, fmt.Errorf("unreadable pane position %q", recorded)
	}
	numbers := make([]int, 0, len(fields))
	for _, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil {
			return mark{}, fmt.Errorf("unreadable pane position %q", recorded)
		}
		numbers = append(numbers, number)
	}
	return mark{
		historySize: numbers[0],
		row:         numbers[0] + numbers[1],
		column:      numbers[2],
		width:       numbers[3],
		height:      numbers[4],
	}, nil
}

// shellQuote wraps a value so a POSIX shell reads it as one word, which the
// paths and channel names below need because this process chooses them and a
// temporary directory may contain anything the platform allows.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
