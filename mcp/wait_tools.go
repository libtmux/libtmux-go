package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Wait tools replace screen polling. run_command tracks authored commands,
// wait_for_text follows external output, and wait_for_channel follows tmux signals.

const (
	runCommandDefaultTimeout = 120 * time.Second
	// waitProgressInterval is how often a long wait tells the client it is
	// still waiting. A client that asked for progress on a two-minute wait
	// should not go two minutes without hearing anything.
	waitProgressInterval = 2 * time.Second
	// waitBufferMax bounds both matching and returned observation text. Prefix
	// loss is reported, and matching is defined over this retained tail.
	waitBufferMax = ceilingMaxBytes
	// waitCeilingDefault bounds how long any one wait may be asked to run.
	// Longer than a build a person waits for, short enough that one wrong
	// pattern costs a wait rather than the conversation it was part of.
	waitCeilingDefault = 300 * time.Second
)

// WaitCeilingEnvironmentVariable names the variable that raises or lowers the
// longest wait this server will run, in seconds. It matches the Python server
// so an operator configuring both writes one thing.
const WaitCeilingEnvironmentVariable = "LIBTMUX_MCP_WAIT_MAX_SECONDS"

// waitCeiling bounds caller latency without blocking other requests. Oversized
// waits are clamped, and each reply reports the effective timeout.
func waitCeiling() time.Duration {
	raw := strings.TrimSpace(os.Getenv(WaitCeilingEnvironmentVariable))
	if raw == "" {
		return waitCeilingDefault
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		// An unreadable ceiling selects the default, on the same reasoning as
		// the safety level: refusing to start over a misspelled variable is
		// worse than running at the value it would have run at anyway.
		return waitCeilingDefault
	}
	return time.Duration(seconds) * time.Second
}

// resolveWaitTimeout reports clamping only when the caller's explicit timeout
// exceeded the ceiling.
func resolveWaitTimeout(requested int) (timeout time.Duration, clamped bool) {
	ceiling := waitCeiling()
	timeout = time.Duration(requested) * time.Second
	if timeout <= 0 {
		return min(runCommandDefaultTimeout, ceiling), false
	}
	if timeout > ceiling {
		return ceiling, true
	}
	return timeout, false
}

func isOwnWaitDeadline(ctx, waitCtx context.Context, err error) bool {
	return ctx.Err() == nil &&
		errors.Is(waitCtx.Err(), context.DeadlineExceeded) &&
		errors.Is(err, context.DeadlineExceeded)
}

// runCommandInput runs one command in a pane and waits for it to finish.
type runCommandInput struct {
	// PaneID is the tmux pane id, such as %1. Empty runs in the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to run the command in; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to run in when paneId is empty"`
	// Command is the shell command to run.
	Command string `json:"command" jsonschema:"the shell command to run"`
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
		timeout, clamped := resolveWaitTimeout(input.TimeoutSeconds)
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
		"{ %s > %s && command mv %s %s; } 2>/dev/null; ( . %s ); "+
			"{ printf %%s $? > %s && command mv %s %s; } 2>/dev/null; "+
			"{ %s > %s && command mv %s %s; } 2>/dev/null\n",
		mark,
		shellQuote(openedTemp),
		shellQuote(openedTemp),
		shellQuote(openedPath),
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
	if _, openedErr := readMark(completion.openedAt); errors.Is(openedErr, os.ErrNotExist) {
		output.OutputUnavailable = commandNeverRanReason(running)
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
		return "the pane never ran the command: it went to whatever the pane " +
			"is running as that program's input rather than to a shell"
	}
	return fmt.Sprintf("the pane never ran the command: it is running %s, "+
		"which took the text as its own input rather than running it; "+
		"respawn_pane gives the pane a shell again", running)
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

// waitForTextInput waits for a pane to write something.
type waitForTextInput struct {
	// PaneID is the tmux pane id, such as %1. Empty watches the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to watch; empty watches the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to watch when paneId is empty"`
	// Patterns are what to wait for; the first one to appear ends the wait.
	// An empty list waits for the pane to write anything at all.
	Patterns []string `json:"patterns,omitempty" jsonschema:"any one of these ends the wait; empty waits for any output"`
	// Stop are the markers of failure. One of these ends the wait too, and
	// says so, which is how a run that failed in a second is not waited out to
	// the deadline.
	Stop []string `json:"stop,omitempty" jsonschema:"markers of failure that end the wait early, such as \"error:\""`
	// Regex reads Patterns and Stop as regular expressions. They are matched
	// across the retained output tail with ^ and $ anchoring at line ends.
	Regex bool `json:"regex,omitempty" jsonschema:"read the patterns as regular expressions"`
	// MatchCase requires the capitalisation to match too.
	MatchCase bool `json:"matchCase,omitempty" jsonschema:"require the capitalisation to match"`
	// SinceEntry ignores the exact screen baseline captured after the watcher
	// attaches, so only later output can match. Use it when the pattern is
	// something the pane may have said before and the question is whether it
	// says it again.
	SinceEntry bool `json:"sinceEntry,omitempty" jsonschema:"ignore what the pane already shows and match only new output"`
	// IdleSeconds ends the wait when the pane has written nothing for that
	// long. It is the ending for a program whose output cannot be predicted:
	// a caller that does not know what "done" prints still knows that done
	// means quiet. Zero waits for a pattern or the deadline instead.
	IdleSeconds int `json:"idleSeconds,omitempty" jsonschema:"end the wait once the pane has written nothing for this many seconds"`
	// TimeoutSeconds bounds the wait. Zero uses a default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait before giving up"`
	// MaxLines caps the returned output, keeping the last lines.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines of output to return at most, keeping the last ones"`
	// MaxBytes caps the returned output's size, keeping the last lines.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes of output to return at most, keeping the last lines"`
}

// Wait outcomes, which say why the wait ended rather than only whether it
// succeeded. A client branching on a boolean cannot tell a failure marker from
// a deadline, and those call for opposite next steps.
const (
	// outcomeMatched means one of Patterns appeared.
	outcomeMatched = "matched"
	// outcomeStopped means one of Stop appeared, so the thing being waited for
	// is not going to happen.
	outcomeStopped = "stopped"
	// outcomeOutput means the pane wrote something, which is what an empty
	// Patterns waits for.
	outcomeOutput = "output"
	// outcomeIdle means the pane went quiet for idleSeconds. Lines reports
	// what it wrote before it did, and an empty Lines with this outcome means
	// the pane never wrote at all, which is what a command that was never
	// started looks like.
	outcomeIdle = "idle"
	// outcomeTimeout means the deadline arrived first.
	outcomeTimeout = "timeout"
)

// waitForTextOutput reports how the wait ended.
type waitForTextOutput struct {
	// PaneID is the pane that was watched.
	PaneID string `json:"paneId"`
	// Outcome is why the wait ended: matched, stopped, output, or timeout.
	Outcome string `json:"outcome"`
	// Found reports whether one of Patterns appeared, which is the common
	// question and would otherwise mean comparing Outcome against two values.
	Found bool `json:"found"`
	// Matched is the pattern that ended the wait, whether it came from
	// Patterns or from Stop.
	Matched string `json:"matched,omitempty"`
	// MatchedAtEntry reports that a match was already on the attached screen
	// baseline rather than written after it. A client that cares whether
	// something just happened, as opposed to having happened, checks this.
	MatchedAtEntry bool `json:"matchedAtEntry"`
	// EntryNote says what happened when a wait ran its whole deadline with the
	// text it was waiting for already on the pane. That pairing is the one
	// shape here that reads as a hang, and a client cannot be expected to
	// reason it out from two other fields: sinceEntry ignored what was already
	// there, and the same call without it would have returned at once.
	EntryNote string `json:"entryNote,omitempty"`
	// Lines are what the pane wrote while waiting, or what it already showed
	// when the match was there on entry.
	Lines []string `json:"lines,omitempty"`
	// ElapsedSeconds is how long the wait took.
	ElapsedSeconds float64 `json:"elapsedSeconds"`
	// EffectiveTimeoutSeconds is the wait this call actually ran, which is
	// what timeoutSeconds asked for unless the server's ceiling was lower.
	EffectiveTimeoutSeconds int `json:"effectiveTimeoutSeconds"`
	// TimeoutClamped reports that the ceiling shortened the wait, so a caller
	// that asked for longer learns the policy from a reply rather than from a
	// failed call.
	TimeoutClamped bool `json:"timeoutClamped,omitempty"`
	// truncation reports what the bounds dropped from Lines.
	truncation
}

// waitForText follows pane output without polling. Existing screen text counts
// unless SinceEntry is set. Shell echo may match, so authored commands should
// use run_command.
func (t *tools) waitForText(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input waitForTextInput,
) (*mcp.CallToolResult, waitForTextOutput, error) {
	if input.TimeoutSeconds < 0 || input.IdleSeconds < 0 {
		return nil, waitForTextOutput{}, errors.New(
			"timeoutSeconds and idleSeconds must not be negative",
		)
	}
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, waitForTextOutput{}, err
	}
	patterns, err := compileNamedMatchers(input.Patterns, input.Regex, input.MatchCase)
	if err != nil {
		return nil, waitForTextOutput{}, err
	}
	stops, err := compileNamedMatchers(input.Stop, input.Regex, input.MatchCase)
	if err != nil {
		return nil, waitForTextOutput{}, err
	}

	timeout, clamped := resolveWaitTimeout(input.TimeoutSeconds)
	started := time.Now()
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	output := waitForTextOutput{
		Outcome:                 outcomeTimeout,
		EffectiveTimeoutSeconds: int(timeout.Seconds()),
		TimeoutClamped:          clamped,
	}
	finishTimeout := func(err error) (*mcp.CallToolResult, waitForTextOutput, error) {
		if isOwnWaitDeadline(ctx, waitCtx, err) {
			return finishWait(
				&output, outcomeTimeout, "", false, nil, limits, truncation{}, started,
			)
		}
		return nil, output, err
	}
	reporter := newProgressReporter(waitCtx, request, timeout, "watching the pane")
	defer reporter.stop()

	process, err := t.runtime.process(waitCtx)
	if err != nil {
		return finishTimeout(err)
	}
	pane, err := t.resolvePane(waitCtx, input.PaneID, input.SessionName)
	if err != nil {
		return finishTimeout(err)
	}
	output.PaneID = pane.ID().String()
	processPane, err := process.Pane(waitCtx, pane.ID())
	if err != nil {
		return finishTimeout(err)
	}

	observation, err := t.runtime.openObservation(waitCtx, processPane)
	if err != nil {
		return finishTimeout(err)
	}
	defer t.runtime.releaseObservation(observation)
	entry := observation.Baseline()

	// Read entry text even when ignored so a timeout can report that the match
	// was already present rather than implying the pattern failed.
	presentAtEntry := false
	if len(patterns) > 0 || len(stops) > 0 {
		shown := strings.Join(entry, "\n")
		stopName, stopped := firstMatch(stops, shown)
		patternName, matchedNow := firstMatch(patterns, shown)
		presentAtEntry = stopped || matchedNow
		if !input.SinceEntry {
			if stopped {
				return finishWait(
					&output, outcomeStopped, stopName, true, entry, limits, truncation{}, started,
				)
			}
			if matchedNow {
				return finishWait(
					&output, outcomeMatched, patternName, true, entry, limits, truncation{}, started,
				)
			}
		}
	}
	idle := time.Duration(input.IdleSeconds) * time.Second
	watched := watchPane(
		waitCtx, observation, pane.ID(), patterns, stops, idle,
	)
	if watched.err != nil {
		if !isOwnWaitDeadline(ctx, waitCtx, watched.err) {
			return nil, output, watched.err
		}
		watched.outcome = outcomeTimeout
	}
	if watched.outcome == outcomeTimeout {
		logToClient(ctx, request, "warning", map[string]any{
			"event":    "wait_for_text timed out",
			"pane":     pane.ID().String(),
			"patterns": input.Patterns,
			"written":  len(watched.written),
			"seconds":  int(timeout.Seconds()),
		})
	}
	return finishWait(
		&output,
		watched.outcome,
		watched.matched,
		presentAtEntry,
		splitWritten(watched.written),
		limits,
		watched.truncation,
		started,
	)
}

type paneWatchResult struct {
	written string
	outcome string
	matched string
	truncation
	err error
}

// watchPane reads a pane's output until something matches or the wait ends.
//
// A nonzero idle ends the wait once the pane has been quiet for that long. The
// window is measured from this pane's output alone: tmux reports structural
// changes on the same connection, and a window opening elsewhere is not this
// pane saying something, so it must not count as the pane still working.
func watchPane(
	ctx context.Context,
	notifications paneNotificationSource,
	paneID tmux.PaneID,
	patterns, stops []namedMatcher,
	idle time.Duration,
) paneWatchResult {
	var result paneWatchResult
	var normalizer terminalTextNormalizer
	buffer := make([]byte, 0, min(waitBufferMax, 4096))
	quiet := time.Now().Add(idle)
	consume := func(data []byte) (string, string, bool) {
		buffer = normalizer.appendChunk(buffer, data)
		if len(buffer) > waitBufferMax {
			start := len(buffer) - waitBufferMax
			for start < len(buffer) && !utf8.RuneStart(buffer[start]) {
				start++
			}
			dropped := buffer[:start]
			result.TruncatedBytes += len(dropped)
			result.TruncatedLines += bytes.Count(dropped, []byte{'\n'})
			result.Truncated = true
			buffer = append(buffer[:0], buffer[start:]...)
		}
		seen := string(buffer)
		if name, hit := firstMatch(stops, seen); hit {
			return outcomeStopped, name, true
		}
		if name, hit := firstMatch(patterns, seen); hit {
			return outcomeMatched, name, true
		}
		if len(patterns) == 0 && len(stops) == 0 && idle == 0 {
			return outcomeOutput, "", true
		}
		return "", "", false
	}
	for {
		readCtx, cancelRead := ctx, context.CancelFunc(func() {})
		if idle > 0 {
			readCtx, cancelRead = context.WithDeadline(ctx, quiet)
		}
		notification, notifyErr := notifications.NextNotification(readCtx)
		readErr := readCtx.Err()
		cancelRead()
		if notifyErr != nil {
			// The idle window closing is an answer; the whole wait running out
			// is not. Only the outer context being live tells them apart.
			if idle > 0 && errors.Is(notifyErr, context.DeadlineExceeded) &&
				errors.Is(readErr, context.DeadlineExceeded) && ctx.Err() == nil {
				result.written = string(buffer)
				result.outcome = outcomeIdle
				return result
			}
			result.written = string(buffer)
			result.err = paneObservationError(notifyErr)
			return result
		}
		id, data, isOutput := notification.Output()
		if !isOutput || id != paneID {
			continue
		}
		if len(data) == 0 {
			continue
		}
		quiet = time.Now().Add(idle)
		if ending, name, done := consume(data); done {
			result.written = string(buffer)
			result.outcome = ending
			result.matched = name
			return result
		}
	}
}

// finishWait fills in the parts of the reply that every ending shares.
func finishWait(
	output *waitForTextOutput,
	outcome, matched string,
	atEntry bool,
	lines []string,
	limits bounds,
	earlier truncation,
	started time.Time,
) (*mcp.CallToolResult, waitForTextOutput, error) {
	kept, report := limits.apply(lines)
	output.Outcome = outcome
	output.Found = outcome == outcomeMatched
	output.Matched = matched
	output.MatchedAtEntry = atEntry
	// Only on the pairing that puzzles, so a note that appears on every wait is
	// not a note anybody reads.
	if atEntry && outcome == outcomeTimeout {
		output.EntryNote = "the text was already on the pane's attached " +
			"baseline, and sinceEntry ignored it, so the " +
			"deadline ran out waiting " +
			"for it to be written again. The same call without sinceEntry " +
			"returns at once."
	}
	output.Lines = kept
	output.truncation = addTruncation(report, earlier)
	output.ElapsedSeconds = time.Since(started).Seconds()
	return textResult(kept), *output, nil
}

// splitWritten turns the normalized pane stream into reply lines.
func splitWritten(written string) []string {
	if written == "" {
		return nil
	}
	return strings.Split(written, "\n")
}

// namedMatcher is a test on some text together with the pattern that made it,
// so a reply can say which one matched rather than only that one did.
type namedMatcher struct {
	name  string
	match func(string) bool
}

// compileNamedMatchers builds the tests for a list of patterns.
func compileNamedMatchers(patterns []string, asRegex, matchCase bool) ([]namedMatcher, error) {
	matchers := make([]namedMatcher, 0, len(patterns))
	for _, pattern := range patterns {
		if strings.TrimSpace(pattern) == "" {
			return nil, errors.New("a pattern must not be empty")
		}
		match, err := compileWaitMatcher(pattern, asRegex, matchCase)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, namedMatcher{name: pattern, match: match})
	}
	return matchers, nil
}

// compileWaitMatcher is compileMatcher with ^ and $ anchored at line ends,
// which is what they mean to someone writing a pattern for terminal output.
func compileWaitMatcher(pattern string, asRegex, matchCase bool) (func(string) bool, error) {
	if !asRegex {
		return compileMatcher(pattern, false, matchCase)
	}
	expression := "(?m)" + pattern
	if !matchCase {
		expression = "(?i)" + expression
	}
	compiled, err := regexp.Compile(expression)
	if err != nil {
		return nil, fmt.Errorf("%q is not a valid regular expression: %w", pattern, err)
	}
	return compiled.MatchString, nil
}

// firstMatch reports the first pattern that matches, in the order given, so a
// caller listing its patterns from most to least specific gets the answer it
// ordered them for.
func firstMatch(matchers []namedMatcher, text string) (string, bool) {
	for _, matcher := range matchers {
		if matcher.match(text) {
			return matcher.name, true
		}
	}
	return "", false
}

// addWaitTools advertises the tools that wait instead of polling.
func addWaitTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityPaneControl, &mcp.Tool{
		Name:        "run_command",
		Annotations: mutating("Run a Command in a Pane"),
		Description: "Run a shell command in one pane, wait for it to finish, and " +
			"return its exit status and its output. Prefer this to send_keys " +
			"followed by capture_pane: it does not read the screen to decide the " +
			"command is done, so the shell's echo of the command cannot be " +
			"mistaken for the command's output, and an exit status is something " +
			"no capture recovers. Pass detach for a command you do not need the " +
			"answer to yet, such as a build: it returns a jobId at once, you do " +
			"other work, and get_job collects the status and the output later.",
	}, t.runCommand)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "get_job",
		Annotations: readOnly("Collect a Detached Command"),
		Description: "Collect a command started with run_command and detach. " +
			"Without timeoutSeconds it reports whether the command has finished " +
			"and returns at once, which is what to call between other work; with " +
			"one it waits that long. A finished job reports its exit status and " +
			"its output, and answers the same way however often you ask.",
	}, t.getJob)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "wait_for_text",
		Annotations: readOnly("Wait for Pane Output"),
		Description: "Wait until a pane writes one of several patterns. It takes " +
			"one exact screen baseline after attaching, then reads what the pane " +
			"produces without polling. Use it for output the " +
			"client did not author, such as a service announcing it is ready. " +
			"Pass stop with the markers of failure you already know, so a run " +
			"that failed returns at once instead of at the deadline. Omit " +
			"patterns to wait for any output at all. Matching retains the most recent " +
			"one megabyte and reports any older prefix as truncation. When you cannot predict what " +
			"finishing prints, set idleSeconds and wait for the pane to go quiet " +
			"instead.",
	}, t.waitForText)
}
