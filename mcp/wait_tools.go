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

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tmux "github.com/tmux-python/libtmux/golang"
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
)

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
	// would see it, so it includes whatever the program painted.
	Output []string `json:"output,omitempty"`
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
	if strings.TrimSpace(input.Command) == "" {
		return nil, runCommandOutput{}, fmt.Errorf("command is required")
	}
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, runCommandOutput{}, err
	}
	server := t.strict()
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, runCommandOutput{}, err
	}
	output := runCommandOutput{PaneID: pane.ID().String()}

	socket, err := server.Cmd(ctx, "display-message", "-p", "#{socket_path}")
	if err != nil {
		return nil, output, err
	}
	if len(socket.Stdout) == 0 || socket.Stdout[0] == "" {
		return nil, output, fmt.Errorf("tmux did not report its socket path")
	}

	directory, err := os.MkdirTemp("", "libtmux-mcp-run")
	if err != nil {
		return nil, output, err
	}
	defer func() { _ = os.RemoveAll(directory) }()

	channel := "libtmux-mcp-" + strconv.FormatInt(runCommandSequence.Add(1), 10)
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
	// Both writes are redirected to files, so nothing about this appears in
	// the pane. A marker printed into the pane would work as well and would be
	// visible to whoever is looking at it.
	//
	// The command runs in a subshell so that a command ending in "exit" ends
	// that subshell rather than the pane's shell, which would otherwise take
	// the status recording and the signal with it and leave the wait hanging
	// until its deadline.
	mark := fmt.Sprintf(
		"%s -S %s display-message -p -t %s '#{history_size} #{cursor_y}'",
		shellQuote(runCommandBinary),
		shellQuote(socket.Stdout[0]),
		shellQuote(pane.ID().String()),
	)
	script := fmt.Sprintf(
		"%s > %s; ( %s ); printf %%s $? > %s; %s > %s; %s -S %s wait-for -S %s",
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
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command:         &script,
		SuppressHistory: input.SuppressHistory,
	}); err != nil {
		return nil, output, err
	}

	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = runCommandDefaultTimeout
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	reporter := newProgressReporter(request, timeout, "waiting for the command to finish")
	defer reporter.stop()

	// The wait runs on a handle with no engine, because a command that blocks
	// inside tmux holds a pooled connection for as long as it blocks.
	waiter := server.WithEngine(server.SubprocessEngine())
	if err := waiter.WaitFor(waitCtx, tmux.WaitForRequest{Channel: channel}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			running := ""
			if fresh, freshErr := server.Pane(ctx, pane.ID()); freshErr == nil {
				running, _ = fresh.Formats().PaneCurrentCommand()
			}
			// The result says the command did not finish; why is diagnostic
			// rather than part of the answer, so it goes to the log a client
			// asked for rather than into the reply.
			logToClient(ctx, request, "warning", map[string]any{
				"event":   "run_command timed out",
				"pane":    pane.ID().String(),
				"running": running,
				"seconds": int(timeout.Seconds()),
			})
			output.TimedOut = true
			output.Running = running
			attachCommandOutput(ctx, pane, openedPath, closedPath, limits, &output)
			return nil, output, nil
		}
		return nil, output, err
	}

	recorded, err := os.ReadFile(statusPath)
	if err != nil {
		return nil, output, fmt.Errorf("command finished without recording a status: %w", err)
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(recorded)))
	if err != nil {
		return nil, output, fmt.Errorf("unreadable exit status %q", recorded)
	}
	output.ExitStatus = &status
	attachCommandOutput(ctx, pane, openedPath, closedPath, limits, &output)
	return nil, output, nil
}

// attachCommandOutput reads back the rows the command wrote.
//
// Failing to read them is not failing the call: the exit status is the answer,
// and a client that got a status with no output is better served than one
// whose call failed because the pane went away after its command finished. A
// command that timed out never wrote its closing mark, so it is read to the
// bottom of the screen instead, which is as much as there is to know.
func attachCommandOutput(
	ctx context.Context,
	pane tmux.Pane,
	openedPath, closedPath string,
	limits bounds,
	output *runCommandOutput,
) {
	opened, err := readMarkedRow(openedPath)
	if err != nil {
		return
	}
	now, err := readPaneState(ctx, pane)
	if err != nil {
		return
	}
	// The marks are absolute positions in tmux's grid; a capture addresses
	// rows relative to the top of the screen, and tmux renumbers every row
	// when it trims the oldest.
	request := tmux.CapturePaneRequest{Start: tmux.CaptureLine(opened - now.historySize)}
	if closed, err := readMarkedRow(closedPath); err == nil {
		request.End = tmux.CaptureLine(closed - now.historySize)
	}
	lines, err := pane.Capture(ctx, request)
	if err != nil {
		return
	}
	// A command whose output ended in a newline leaves the cursor on the row
	// below it, which is blank.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	kept, report := limits.apply(lines)
	output.Output = kept
	output.truncation = report
}

// readMarkedRow reads one position the wrapper recorded, as tmux printed it:
// a history size and a cursor row, whose sum is a position that does not move
// when tmux renumbers the grid.
func readMarkedRow(path string) (int, error) {
	recorded, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	history, cursor, found := strings.Cut(strings.TrimSpace(string(recorded)), " ")
	if !found {
		return 0, fmt.Errorf("unreadable pane position %q", recorded)
	}
	top, err := strconv.Atoi(history)
	if err != nil {
		return 0, fmt.Errorf("unreadable pane position %q", recorded)
	}
	row, err := strconv.Atoi(cursor)
	if err != nil {
		return 0, fmt.Errorf("unreadable pane position %q", recorded)
	}
	return top + row, nil
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
	// MatchedAtEntry reports that the match was already on the screen when the
	// wait began rather than written during it. A client that cares whether
	// something just happened, as opposed to having happened, checks this.
	MatchedAtEntry bool `json:"matchedAtEntry"`
	// Lines are what the pane wrote while waiting, or what it already showed
	// when the match was there on entry.
	Lines []string `json:"lines,omitempty"`
	// ElapsedSeconds is how long the wait took.
	ElapsedSeconds float64 `json:"elapsedSeconds"`
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

	server := t.strict()
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, waitForTextOutput{}, err
	}
	output := waitForTextOutput{PaneID: pane.ID().String(), Outcome: outcomeTimeout}
	started := time.Now()

	// What the pane already shows counts unless the caller said otherwise. A
	// client cannot start a program and wait for it in the same request, so by
	// the time the wait begins the announcement it is waiting for may already
	// have been made, and a wait that only watched the stream would miss it
	// and time out while the text sat on the screen.
	if !input.SinceEntry && (len(patterns) > 0 || len(stops) > 0) {
		if lines, captureErr := pane.Capture(ctx, tmux.CapturePaneRequest{}); captureErr == nil {
			shown := strings.Join(lines, "\n")
			if name, hit := firstMatch(stops, shown); hit {
				return finishWait(&output, outcomeStopped, name, true, lines, limits, started)
			}
			if name, hit := firstMatch(patterns, shown); hit {
				return finishWait(&output, outcomeMatched, name, true, lines, limits, started)
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

	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = runCommandDefaultTimeout
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	reporter := newProgressReporter(request, timeout, "watching the pane")
	defer reporter.stop()

	written, outcome, matched, err := watchPane(waitCtx, control, pane.ID(), patterns, stops)
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
	return finishWait(&output, outcome, matched, false, splitWritten(written), limits, started)
}

// watchPane reads a pane's output until something matches or the wait ends.
func watchPane(
	ctx context.Context,
	control *tmux.ControlClient,
	paneID tmux.PaneID,
	patterns, stops []namedMatcher,
) (written string, outcome, matched string, err error) {
	var buffer strings.Builder
	for {
		notification, notifyErr := control.NextNotification(ctx)
		if notifyErr != nil {
			return buffer.String(), outcomeTimeout, "", notifyErr
		}
		id, data, isOutput := notification.Output()
		if !isOutput || id != paneID {
			continue
		}
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
		if len(patterns) == 0 && len(stops) == 0 {
			// Nothing to match means the caller is waiting for the pane to say
			// anything at all, and it just has.
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
			return nil, fmt.Errorf("a pattern must not be empty")
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
			"no capture recovers.",
	}, t.runCommand)
	register(server, t, &mcp.Tool{
		Name:        "wait_for_text",
		Annotations: readOnly("Wait for Pane Output"),
		Description: "Wait until a pane writes one of several patterns, reading " +
			"what the pane produces rather than its screen. Use it for output the " +
			"client did not author, such as a service announcing it is ready. " +
			"Pass stop with the markers of failure you already know, so a run " +
			"that failed returns at once instead of at the deadline. Omit " +
			"patterns to wait for any output at all.",
	}, t.waitForText)
}
