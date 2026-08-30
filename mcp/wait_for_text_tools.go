package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// waitBufferMax bounds both matching and returned observation text. Prefix
// loss is reported, and matching is defined over this retained tail.
const waitBufferMax = ceilingMaxBytes

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

	timeout, clamped := t.resolveWaitTimeout(input.TimeoutSeconds)
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
