package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Waiting is a tool, because polling is a client's way of paying for one
// answer many times.
//
// A client without these reads the pane, decides it is not done, and reads it
// again. Every look is a round trip, the looks are spaced by a guess, and what
// it matches is a screen: the shell's echo of the command it just sent looks
// exactly like the command having produced that text.
//
// run_command is for a command this client runs, and answers with its exit
// status, which no amount of screen-reading recovers. wait_for_text is for
// output the client did not author, and reads tmux's stream of what the pane
// produced rather than its screen. wait_for_channel is for anything else that
// can signal tmux.

const (
	runCommandBinary         = "tmux"
	runCommandDefaultTimeout = 120 * time.Second
	// waitProgressInterval is how often a long wait tells the client it is
	// still waiting. A client that asked for progress on a two-minute wait
	// should not go two minutes without hearing anything.
	waitProgressInterval = 2 * time.Second
	// waitBufferMax bounds what a wait keeps in order to match against it. A
	// pane writing continuously would otherwise grow this without limit, and
	// nothing beyond the last of it can match a pattern anyway.
	waitBufferMax = 256 * 1024
	// waitCeilingDefault bounds how long any one wait may be asked to run.
	// Longer than a build a person waits for, short enough that one wrong
	// pattern costs a wait rather than the conversation it was part of.
	waitCeilingDefault = 300 * time.Second
)

// WaitCeilingEnvironmentVariable names the variable that raises or lowers the
// longest wait this server will run, in seconds. It matches the Python server
// so an operator configuring both writes one thing.
const WaitCeilingEnvironmentVariable = "LIBTMUX_MCP_WAIT_MAX_SECONDS"

// A wait is the only thing here that runs for as long as a caller asks, so it
// is the only thing that can spend a caller's turn without producing anything.
//
// The ceiling bounds the caller rather than the transport. These tools await
// throughout, so a long wait blocks nothing else; what it costs is the turn it
// happens in, because MCP gives a caller no way to change its mind once a call
// is in flight. A model that guesses the wrong pattern and asks for an hour
// gets an hour of nothing. The ceiling makes that mistake cost a wait.
//
// Clamp rather than refuse. An over-large timeout is a caller that does not
// know the policy, and failing the call teaches it nothing it can act on; the
// wait runs at the ceiling instead and every reply carries the timeout it
// actually used, so the policy is learned from an answer rather than an error.
//
// Every reply carries it even when nothing was clamped, because the default is
// otherwise invisible: a caller that sent no timeout at all has no way to know
// what it is waiting for.
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

// resolveWaitTimeout turns the seconds a caller asked for into the wait this
// server will run, and reports whether the ceiling shortened it.
//
// Only a caller that asked for more than the ceiling is told it was clamped.
// A ceiling below the default shortens that too, but a call that named no
// timeout had nothing of its own shortened, and reporting one would send a
// caller looking for a request it never made.
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

var runCommandSequence atomic.Int64

// runCommandInput runs one command in a pane and waits for it to finish.
type runCommandInput struct {
	// PaneID is the tmux pane id, such as %1. Empty runs in the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to run the command in; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to run in when paneId is empty"`
	// Command is the shell command to run.
	Command string `json:"command" jsonschema:"the shell command to run"`
	// TimeoutSeconds bounds the wait. Zero uses a default.
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
	// Output is what the pane showed while the command ran, read from where
	// the pane stood when it was sent. It is the command's output as a person
	// would see it, so it includes whatever the program painted. Lines the
	// terminal wrapped are rejoined, so one line the command printed is one
	// entry here however wide the pane is.
	//
	// tmux before 3.6 left a tab in the grid as the spaces it moved the cursor
	// over, so a tab in the output arrives as spaces there and cannot be
	// recovered.
	Output []string `json:"output,omitempty"`
	// OutputUnavailable says why Output is missing, and is absent when the
	// command simply printed nothing. The status is the answer either way, so
	// failing to read the pane does not fail the call, but a caller branching
	// on empty output needs to know which of the two it has.
	OutputUnavailable string `json:"outputUnavailable,omitempty"`
	// EffectiveTimeoutSeconds is the wait this call actually ran, which is
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

// runCommand types a command into a pane and blocks until it exits.
//
// It exists so that a client does not have to read the pane in a loop to learn
// whether its command finished. Nothing is matched against the screen, so the
// shell's echo of the command cannot be mistaken for the command's own output,
// and the answer costs one call rather than one call per look.
//
// The command signals a tmux channel when it ends and writes its status to a
// file this process reads, because a tmux channel carries no payload. Both
// need a filesystem the tmux server and this process share, which is the same
// assumption a pane running a local shell already makes.
//
// What the command printed comes back with the status. The wrapper notes the
// pane's cursor on either side of the command, so the reply is exactly the rows
// the command wrote rather than whatever the screen held; a client that wanted
// only the status ignores it, and one that wanted the output no longer has to
// guess where in the pane its command began.
//
// The pane must be at a shell prompt. This types into whatever the pane is
// running, so a pane already busy receives the text as that program's input
// rather than running it, and the wait then reaches its deadline with nothing
// having happened. The result names what the pane was running when the wait
// ended, which is how a caller tells that case from a slow command. Send
// "C-c" with send_keys to get such a pane back.
func (t *tools) runCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input runCommandInput,
) (*mcp.CallToolResult, runCommandOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, runCommandOutput{}, err
	}
	started, err := t.startCommand(ctx, request, input)
	if err != nil {
		return nil, runCommandOutput{}, err
	}
	output := runCommandOutput{PaneID: started.paneID.String()}

	// A detached run is finished here. The handle is what collects it, and the
	// directory it records itself in outlives this call because of that.
	if input.Detach {
		t.jobs.keep(started)
		output.JobID = started.id
		output.Detached = true
		return nil, output, nil
	}
	defer func() { _ = os.RemoveAll(started.directory) }()

	pane, err := t.target.Pane(ctx, started.paneID)
	if err != nil {
		return nil, output, err
	}
	return t.awaitCommand(ctx, request, awaiting{
		server:     t.target,
		pane:       pane,
		channel:    started.channel,
		statusPath: started.statusAt,
		openedPath: started.openedAt,
		closedPath: started.closedAt,
		limits:     limits,
		requested:  input.TimeoutSeconds,
		output:     output,
	})
}

// startCommand types a command into a pane and returns the handle that
// identifies it, without waiting for anything.
//
// It is separate from waiting because the two are independent: the wrapper
// records the same status against the same channel whether or not this process
// is the one that reads it, so detaching changes who waits and nothing else.
func (t *tools) startCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input runCommandInput,
) (*job, error) {
	if strings.TrimSpace(input.Command) == "" {
		return nil, errors.New("command is required")
	}
	server := t.target
	pane, err := t.resolvePaneToWrite(
		ctx, request, input.PaneID, input.SessionName, "running a command")
	if err != nil {
		return nil, err
	}

	// A pane whose process has exited reads no keys, so the wrapper would never
	// run and the wait would reach its deadline having done nothing. Saying so
	// now costs one lookup and saves the caller the whole timeout.
	if dead, _ := pane.Formats().PaneDead(); dead {
		return nil, fmt.Errorf(
			"pane %s has no process: its program exited, so it reads no keys. "+
				"respawn_pane restarts it", pane.ID())
	}

	socket, err := server.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil {
		return nil, err
	}
	if len(socket.Stdout) == 0 || socket.Stdout[0] == "" {
		return nil, errors.New("tmux did not report its socket path")
	}

	directory, err := os.MkdirTemp("", "libtmux-mcp-run")
	if err != nil {
		return nil, err
	}

	// The channel names the process as well as the command, so that two of
	// these servers driving one tmux cannot signal each other's waits, and so
	// that a handle from a previous run is recognisable as one rather than
	// looking like a handle this run has forgotten.
	channel := fmt.Sprintf("libtmux-mcp-%d-%d",
		os.Getpid(), runCommandSequence.Add(1))
	statusPath := filepath.Join(directory, "status")
	openedPath := filepath.Join(directory, "opened")
	closedPath := filepath.Join(directory, "closed")

	// The wrapper notes where the pane's cursor stands on either side of the
	// command, so the output can be read back from exactly the rows the
	// command wrote.
	//
	// Noting it from inside the pane rather than before the keys are sent is
	// what makes it exact: by the time the wrapper runs, the shell has already
	// echoed the command and moved to the line where output will start.
	// Measuring from outside instead means guessing how many rows that echo
	// occupied, and an interactive shell redraws it as it goes, so the echo
	// cannot be recognised and removed afterwards either.
	//
	// The column is recorded with the row because it says whether the command's
	// last line ended. A cursor resting at column zero means the output ended
	// with a newline and the closing row belongs to whatever comes next, which
	// is the shell's own prompt; a cursor further along means that row holds the
	// tail of the output itself.
	//
	// Both writes are redirected to files, so nothing about this appears in
	// the pane. A marker printed into the pane would work as well and would be
	// visible to whoever is looking at it.
	//
	// The command runs in a subshell so that a command ending in "exit" ends
	// that subshell rather than the pane's shell, which would otherwise take
	// the status recording and the signal with it and leave the wait hanging
	// until its deadline.
	mark := fmt.Sprintf(
		"%s -S %s display-message -p -t %s '#{history_size} #{cursor_y} #{cursor_x}'",
		shellQuote(runCommandBinary),
		shellQuote(socket.Stdout[0]),
		shellQuote(pane.ID().String()),
	)
	script := fmt.Sprintf(
		"%s > %s; ( %s ); printf %%s $? > %s; %s > %s; %s -S %s wait-for -S %s\n",
		mark,
		shellQuote(openedPath),
		input.Command,
		shellQuote(statusPath),
		mark,
		shellQuote(closedPath),
		shellQuote(runCommandBinary),
		shellQuote(socket.Stdout[0]),
		shellQuote(channel),
	)

	// The script is sourced from a file rather than typed. A shell's line
	// editor reads what arrives as keys, so a command carrying a tab asked it
	// to complete a filename and ran as whatever that inserted, and one
	// carrying C-c or C-d was acted on rather than run. Only this path is
	// typed, and this package chose every character in it.
	//
	// It also keeps the pane readable: what a person sees is one short line
	// rather than the whole wrapper echoed across the screen.
	scriptPath := filepath.Join(directory, "script")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	sourceScript := ". " + shellQuote(scriptPath)
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command:         &sourceScript,
		SuppressHistory: input.SuppressHistory,
	}); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	return &job{
		id:        channel,
		paneID:    pane.ID(),
		channel:   channel,
		command:   input.Command,
		directory: directory,
		openedAt:  openedPath,
		closedAt:  closedPath,
		statusAt:  statusPath,
		started:   time.Now(),
	}, nil
}

// awaiting is what a wait for one command needs to know, gathered so that the
// wait reads as one step rather than eight parameters.
type awaiting struct {
	server                             tmux.Server
	pane                               tmux.Pane
	channel                            string
	statusPath, openedPath, closedPath string
	limits                             bounds
	requested                          int
	output                             runCommandOutput
}

// awaitCommand blocks until a started command signals its channel, and reads
// back its status and the rows it wrote.
func (t *tools) awaitCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	waiting awaiting,
) (*mcp.CallToolResult, runCommandOutput, error) {
	output := waiting.output
	timeout, clamped := resolveWaitTimeout(waiting.requested)
	output.EffectiveTimeoutSeconds = int(timeout.Seconds())
	output.TimeoutClamped = clamped
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	reporter := newProgressReporter(request, timeout, "waiting for the command to finish")
	defer reporter.stop()

	// The wait runs on a handle with no engine, because a command that blocks
	// inside tmux holds a pooled connection for as long as it blocks.
	waiter := waiting.server.WithEngine(waiting.server.SubprocessEngine())
	if err := waiter.WaitFor(waitCtx, tmux.WaitForRequest{Channel: waiting.channel}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			running := ""
			if fresh, freshErr := waiting.server.Pane(ctx, waiting.pane.ID()); freshErr == nil {
				running, _ = fresh.Formats().PaneCurrentCommand()
			}
			// The result says the command did not finish; why is diagnostic
			// rather than part of the answer, so it goes to the log a client
			// asked for rather than into the reply.
			logToClient(ctx, request, "warning", map[string]any{
				"event":   "run_command timed out",
				"pane":    waiting.pane.ID().String(),
				"running": running,
				"seconds": int(timeout.Seconds()),
			})
			output.TimedOut = true
			output.Running = running
			attachCommandOutput(ctx, waiting.pane,
				waiting.openedPath, waiting.closedPath, waiting.limits, &output)
			return nil, output, nil
		}
		return nil, output, err
	}

	recorded, err := os.ReadFile(waiting.statusPath)
	if err != nil {
		return nil, output, fmt.Errorf("command finished without recording a status: %w", err)
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(recorded)))
	if err != nil {
		return nil, output, fmt.Errorf("unreadable exit status %q", recorded)
	}
	output.ExitStatus = &status
	attachCommandOutput(ctx, waiting.pane,
		waiting.openedPath, waiting.closedPath, waiting.limits, &output)
	return nil, output, nil
}

// attachCommandOutput reads back the rows the command wrote.
//
// Failing to read them is not failing the call: the exit status is the answer,
// and a client that got a status with no output is better served than one
// whose call failed because the pane went away after its command finished. A
// command that timed out never wrote its closing mark, so it is read to the
// bottom of the screen instead, which is as much as there is to know. Why the
// output is missing is reported rather than swallowed, because a caller
// otherwise cannot tell a command that printed nothing from one whose output
// could not be read.
func attachCommandOutput(
	ctx context.Context,
	pane tmux.Pane,
	openedPath, closedPath string,
	limits bounds,
	output *runCommandOutput,
) {
	opened, err := readMark(openedPath)
	if err != nil {
		output.OutputUnavailable = markMissing(ctx, pane, err)
		return
	}
	now, err := readPaneState(ctx, pane)
	if err != nil {
		output.OutputUnavailable = err.Error()
		return
	}
	// The marks are absolute positions in tmux's grid; a capture addresses
	// rows relative to the top of the screen, and tmux renumbers every row
	// when it trims the oldest.
	//
	// Wrapped rows are rejoined, so a line longer than the pane arrives as the
	// one line the command printed rather than as one entry per screen row.
	request := tmux.CapturePaneRequest{
		Start:       tmux.CaptureLine(opened.row - now.historySize),
		JoinWrapped: true,
	}
	// The closing row belongs to the output only when the cursor stopped part
	// way along it. A cursor at column zero means the last line ended, so that
	// row holds whatever the shell drew next, and reading it returns a prompt
	// as though the command had printed one.
	if closed, err := readMark(closedPath); err == nil {
		end := closed.row
		if closed.column == 0 {
			end--
		}
		if end < opened.row {
			output.Output = nil
			return
		}
		request.End = tmux.CaptureLine(end - now.historySize)
	}
	lines, err := pane.Capture(ctx, request)
	if err != nil {
		output.OutputUnavailable = err.Error()
		return
	}
	// Rejoining preserves the spaces a row is padded with, and tmux only trims
	// them itself from 3.4. Trimming here rather than leaving it to tmux is
	// what makes one command's output read the same on every supported
	// version. Nothing is lost: a terminal grid pads every row to its width, so
	// a space at the end of a row is the grid's rather than the command's.
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}
	kept, report := limits.apply(lines)
	output.Output = kept
	output.truncation = report
}

// mark is where the pane's cursor stood when the wrapper recorded it.
type mark struct {
	// row is an absolute position in tmux's grid, being the history size plus
	// the cursor's row, which does not move when tmux renumbers the grid.
	row int
	// column is the cursor's column. Zero means the line before it ended.
	column int
}

// readMark reads one position the wrapper recorded, as tmux printed it: a
// history size, a cursor row, and a cursor column.
// markMissing explains an absent opening mark, which is the ordinary way this
// fails and the one a caller can act on.
//
// The wrapper writes that mark as its first act, so its absence means the pane
// never ran the wrapper: the keys went to whatever the pane is running as that
// program's input. Reporting the failed open names a path inside this server
// and leaves the reader with a filesystem error for a tmux problem.
func markMissing(ctx context.Context, pane tmux.Pane, err error) string {
	if !errors.Is(err, os.ErrNotExist) {
		return err.Error()
	}
	running := ""
	if fresh, freshErr := pane.Refresh(ctx); freshErr == nil {
		running, _ = fresh.Formats().PaneCurrentCommand()
	}
	if running == "" {
		return "the pane never ran the command: it went to whatever the pane " +
			"is running as that program's input rather than to a shell"
	}
	return fmt.Sprintf("the pane never ran the command: it is running %s, "+
		"which took the text as its own input rather than running it; "+
		"respawn_pane gives the pane a shell again", running)
}

func readMark(path string) (mark, error) {
	recorded, err := os.ReadFile(path)
	if err != nil {
		return mark{}, err
	}
	fields := strings.Fields(string(recorded))
	if len(fields) != 3 {
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
	return mark{row: numbers[0] + numbers[1], column: numbers[2]}, nil
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
	// across the pane's whole output with ^ and $ anchoring at line ends.
	Regex bool `json:"regex,omitempty" jsonschema:"read the patterns as regular expressions"`
	// MatchCase requires the capitalisation to match too.
	MatchCase bool `json:"matchCase,omitempty" jsonschema:"require the capitalisation to match"`
	// SinceEntry ignores what the pane already shows, so only output written
	// after the call began can match. Use it when the pattern is something the
	// pane may have said before and the question is whether it says it again.
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
	// MatchedAtEntry reports that a match was already on the screen when the
	// wait began rather than written during it. A client that cares whether
	// something just happened, as opposed to having happened, checks this.
	//
	// True beside a timeout is the answer to the puzzle it otherwise poses:
	// sinceEntry ignored text that was there all along, and the same call
	// without it would have matched at once.
	MatchedAtEntry bool `json:"matchedAtEntry"`
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

// waitForText blocks until a pane writes something, reading tmux's stream of
// what the pane produced rather than its screen.
//
// This is the tool for output the client did not author: a program that
// announces itself when it is ready. Nothing is polled, so the answer costs
// one call and arrives when the text does rather than at the next look.
//
// Text the pane has already shown counts as found, because a client cannot
// start a program and wait for it in the same request: by the time the wait
// begins, a quick program has already said its piece. The reply says so, and
// sinceEntry turns it off for a client asking whether something happened again
// rather than whether it has happened.
//
// A shell still echoes what was typed into it. Waiting for text that appears in
// a command the client itself just sent will match that echo, which is why
// run_command exists for commands this client runs.
func (t *tools) waitForText(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input waitForTextInput,
) (*mcp.CallToolResult, waitForTextOutput, error) {
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

	server := t.target
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, waitForTextOutput{}, err
	}
	output := waitForTextOutput{PaneID: pane.ID().String(), Outcome: outcomeTimeout}
	started := time.Now()
	// Resolved before the entry check rather than beside the wait it bounds,
	// so that a match already on the screen reports the same budget a match
	// arriving a minute later would have.
	timeout, clamped := resolveWaitTimeout(input.TimeoutSeconds)
	output.EffectiveTimeoutSeconds = int(timeout.Seconds())
	output.TimeoutClamped = clamped

	// What the pane already shows counts unless the caller said otherwise. A
	// client cannot start a program and wait for it in the same request, so by
	// the time the wait begins the announcement it is waiting for may already
	// have been made, and a wait that only watched the stream would miss it
	// and time out while the text sat on the screen.
	//
	// The screen is read even when sinceEntry says to ignore it, because a
	// caller that asked to ignore it still needs to be told when that is why
	// the wait found nothing. A timeout on text that sat on the screen
	// throughout otherwise reads as a pattern that does not work.
	presentAtEntry := false
	if len(patterns) > 0 || len(stops) > 0 {
		if lines, captureErr := pane.Capture(ctx, tmux.CapturePaneRequest{}); captureErr == nil {
			shown := strings.Join(lines, "\n")
			stopName, stopped := firstMatch(stops, shown)
			patternName, matchedNow := firstMatch(patterns, shown)
			presentAtEntry = stopped || matchedNow
			if !input.SinceEntry {
				if stopped {
					return finishWait(&output, outcomeStopped, stopName, true, lines, limits, started)
				}
				if matchedNow {
					return finishWait(&output, outcomeMatched, patternName, true, lines, limits, started)
				}
			}
		}
	}

	session, err := server.Session(ctx, pane.SessionID())
	if err != nil {
		return nil, output, err
	}
	// A connection of its own, closed with the wait, so that watching a pane
	// does not hold a client attached for longer than the client asked for.
	control, err := server.WithEngine(server.SubprocessEngine()).OpenControl(ctx, session)
	if err != nil {
		return nil, output, err
	}
	defer func() { _ = control.Close() }()

	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	reporter := newProgressReporter(request, timeout, "watching the pane")
	defer reporter.stop()

	idle := time.Duration(input.IdleSeconds) * time.Second
	written, outcome, matched, err := watchPane(waitCtx, control, pane.ID(), patterns, stops, idle)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, output, err
	}
	if outcome == outcomeTimeout {
		logToClient(ctx, request, "warning", map[string]any{
			"event":    "wait_for_text timed out",
			"pane":     pane.ID().String(),
			"patterns": input.Patterns,
			"written":  len(written),
			"seconds":  int(timeout.Seconds()),
		})
	}
	return finishWait(&output, outcome, matched, presentAtEntry, splitWritten(written), limits, started)
}

// watchPane reads a pane's output until something matches or the wait ends.
//
// A nonzero idle ends the wait once the pane has been quiet for that long. The
// window is measured from this pane's output alone: tmux reports structural
// changes on the same connection, and a window opening elsewhere is not this
// pane saying something, so it must not count as the pane still working.
func watchPane(
	ctx context.Context,
	control *tmux.ControlClient,
	paneID tmux.PaneID,
	patterns, stops []namedMatcher,
	idle time.Duration,
) (written string, outcome, matched string, err error) {
	var buffer strings.Builder
	quiet := time.Now().Add(idle)
	for {
		readCtx, cancelRead := ctx, context.CancelFunc(func() {})
		if idle > 0 {
			readCtx, cancelRead = context.WithDeadline(ctx, quiet)
		}
		notification, notifyErr := control.NextNotification(readCtx)
		cancelRead()
		if notifyErr != nil {
			// The idle window closing is an answer; the whole wait running out
			// is not. Only the outer context being live tells them apart.
			if idle > 0 && ctx.Err() == nil {
				return buffer.String(), outcomeIdle, "", nil
			}
			return buffer.String(), outcomeTimeout, "", notifyErr
		}
		id, data, isOutput := notification.Output()
		if !isOutput || id != paneID {
			continue
		}
		quiet = time.Now().Add(idle)
		buffer.Write(data)
		if buffer.Len() > waitBufferMax {
			// Only the tail can still complete a match, and a pane writing
			// without stopping would otherwise be kept in full.
			trimmed := buffer.String()
			buffer.Reset()
			buffer.WriteString(trimmed[len(trimmed)-waitBufferMax:])
		}
		seen := buffer.String()
		if name, hit := firstMatch(stops, seen); hit {
			return seen, outcomeStopped, name, nil
		}
		if name, hit := firstMatch(patterns, seen); hit {
			return seen, outcomeMatched, name, nil
		}
		if len(patterns) == 0 && len(stops) == 0 && idle == 0 {
			// Nothing to match means the caller is waiting for the pane to say
			// anything at all, and it just has. An idle window is the other
			// way round: the caller is waiting for the pane to stop, so the
			// first byte is the start of what it waits out rather than an end.
			return seen, outcomeOutput, "", nil
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
	started time.Time,
) (*mcp.CallToolResult, waitForTextOutput, error) {
	kept, report := limits.apply(lines)
	output.Outcome = outcome
	output.Found = outcome == outcomeMatched
	output.Matched = matched
	output.MatchedAtEntry = atEntry
	output.Lines = kept
	output.truncation = report
	output.ElapsedSeconds = time.Since(started).Seconds()
	return textResult(kept), *output, nil
}

// splitWritten turns a pane's byte stream into lines a caller can read.
//
// Carriage returns are dropped rather than kept: a terminal writes them to
// move the cursor, and a client reading the result as text would find them
// embedded in the middle of lines that look correct on a screen.
func splitWritten(written string) []string {
	if written == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(written, "\r\n", "\n"), "\n")
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
	register(server, t, &mcp.Tool{
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
	register(server, t, &mcp.Tool{
		Name:        "get_job",
		Annotations: readOnly("Collect a Detached Command"),
		Description: "Collect a command started with run_command and detach. " +
			"Without timeoutSeconds it reports whether the command has finished " +
			"and returns at once, which is what to call between other work; with " +
			"one it waits that long. A finished job reports its exit status and " +
			"its output, and answers the same way however often you ask.",
	}, t.getJob)
	register(server, t, &mcp.Tool{
		Name:        "wait_for_text",
		Annotations: readOnly("Wait for Pane Output"),
		Description: "Wait until a pane writes one of several patterns, reading " +
			"what the pane produces rather than its screen. Use it for output the " +
			"client did not author, such as a service announcing it is ready. " +
			"Pass stop with the markers of failure you already know, so a run " +
			"that failed returns at once instead of at the deadline. Omit " +
			"patterns to wait for any output at all. When you cannot predict what " +
			"finishing prints, set idleSeconds and wait for the pane to go quiet " +
			"instead.",
	}, t.waitForText)
}
