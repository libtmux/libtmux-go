package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// defaultWaitForTextInterval is the cadence [WaitForTextRequest.Interval]
// takes when it is zero.
const defaultWaitForTextInterval = 100 * time.Millisecond

// ErrPaneEnded identifies a wait that stopped because the pane or its server
// ended before the text appeared. It is matched by errors.Is for
// [PaneEndedError].
var ErrPaneEnded = errors.New("tmux: pane ended before the text appeared")

// ErrInvalidWaitForText identifies a [WaitForTextRequest] that cannot be
// waited on. It is matched by errors.Is.
var ErrInvalidWaitForText = errors.New("tmux: invalid text wait request")

// PaneEndReason says how a pane ended during [Pane.WaitForText].
type PaneEndReason uint8

const (
	// PaneExited is a pane whose program exited and which remain-on-exit kept
	// on screen.
	PaneExited PaneEndReason = iota + 1
	// PaneGone is a pane that no longer exists while its server does.
	PaneGone
	// ServerGone is a server that is not running, or that exited during the
	// wait.
	ServerGone
)

// String returns the reason's name.
func (r PaneEndReason) String() string {
	switch r {
	case PaneExited:
		return "PaneExited"
	case PaneGone:
		return "PaneGone"
	case ServerGone:
		return "ServerGone"
	default:
		return fmt.Sprintf("PaneEndReason(%d)", uint8(r))
	}
}

// PaneEndedError reports that [Pane.WaitForText] stopped because the pane or
// its server ended first. The text read before then is in the returned
// [PaneTextWait].
//
// It matches [ErrPaneEnded] through errors.Is, and [ErrNotFound] for
// [PaneGone] and [ErrNoServer] for [ServerGone]. It unwraps to the
// [CommandError] of the failed read when there was one.
type PaneEndedError struct {
	// Reason says how the pane ended.
	Reason PaneEndReason

	cause error
}

// Error implements error.
func (e *PaneEndedError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%v (%s): %v", ErrPaneEnded, e.Reason, e.cause)
	}
	return fmt.Sprintf("%v (%s)", ErrPaneEnded, e.Reason)
}

// Unwrap returns [ErrPaneEnded] and the failed read's error, if any.
func (e *PaneEndedError) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrPaneEnded}
	}
	return []error{ErrPaneEnded, e.cause}
}

// Is reports [ErrNotFound] for [PaneGone] and [ErrNoServer] for [ServerGone],
// including when no failed read is attached.
func (e *PaneEndedError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Reason == PaneGone
	case ErrNoServer:
		return e.Reason == ServerGone
	default:
		return false
	}
}

// WaitForTextRequest configures [Pane.WaitForText].
type WaitForTextRequest struct {
	// Text is the literal to find. It must be non-empty.
	Text string
	// Interval is the cadence between reads. Zero means 100ms. It is never a
	// correctness bound.
	Interval time.Duration
}

// PaneTextWait is the result of [Pane.WaitForText]. It is returned with every
// error, holding what the last read saw.
type PaneTextWait struct {
	// PaneText holds the visible screen of the last read, joined across
	// wrapped rows, without trailing spaces or trailing blank lines.
	PaneText
	// MatchedAtEntry reports that the first read already held Text.
	MatchedAtEntry bool
}

// WaitForText polls the receiver's visible screen until it holds req.Text,
// the pane or server ends, or ctx ends. It polls on every supported tmux
// release and never opens a control client; [PaneObservation.WaitFor] is the
// push-based wait for tmux 3.7 and newer.
//
// The wait is level-triggered. Text already on screen matches at once, with
// [PaneTextWait.MatchedAtEntry] set, and so does a shell's echo of a typed
// command that contains req.Text. Match on what the command prints, not on
// the command: printf 'NEE%s' DLE never puts NEEDLE in the typed line.
// [PaneObservation.WaitFor] differs by reporting only output after its
// baseline. A caller who needs only new output on tmux 3.3a through 3.6
// checks the screen before typing and picks text the screen lacks.
//
// Only the visible screen is read, never history, so text that has scrolled
// off is not seen. Wrapped rows are joined, so text may straddle the wrap
// column. A poll sees the final state of text a program overwrote with a
// carriage return, and text printed before a full-screen program started is
// not on the screen being read.
//
// Every read is one ordinary tmux process, so a server bound to a
// [Connection] is refused with [ErrConnectionRequiresProcess].
// Each read runs the owner's after-capture-pane hook, and a hook that prints
// adds its output to the text.
// The read asks for liveness through run-shell, which fires no hook.
//
// The text is matched before the pane's liveness is acted on, so a complete
// read of a pane that has since died still matches. A pane or server that
// ends first returns a [*PaneEndedError]. A deadline or cancel returns
// ctx.Err, with the text of the last complete read. An empty req.Text or a
// negative interval fails before any tmux command runs with
// [ErrInvalidWaitForText].
func (p Pane) WaitForText(
	ctx context.Context,
	req WaitForTextRequest,
) (PaneTextWait, error) {
	var result PaneTextWait
	if req.Text == "" {
		return result, fmt.Errorf("%w: Text is empty", ErrInvalidWaitForText)
	}
	interval := req.Interval
	switch {
	case interval < 0:
		return result, fmt.Errorf("%w: Interval %s is negative", ErrInvalidWaitForText, interval)
	case interval == 0:
		interval = defaultWaitForTextInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for first := true; ; first = false {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		read, err := p.readScreen(ctx)
		if read.complete {
			result.Text = read.text
		}
		if read.complete && strings.Contains(read.text, req.Text) {
			result.MatchedAtEntry = first
			return result, nil
		}
		if err != nil {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-ticker.C:
		}
	}
}

// screenRead is one invocation's answer. text is meaningful only when
// complete.
type screenRead struct {
	text     string
	complete bool
}

// readScreen runs one capture and liveness report and classifies the answer.
// A nil error means the pane is still live.
func (p Pane) readScreen(ctx context.Context) (screenRead, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return screenRead{}, err
	}
	marker := "libtmux-text-wait-" + hex.EncodeToString(random) + "-"
	target := p.paneID.String()
	result, err := p.server.requireProcess().Cmd(
		ctx,
		"capture-pane", "-p", "-J", "-t", target,
		";",
		"run-shell", "-C", "-t", target, marker+"#{q:pane_id}-#{q:pane_dead}|",
	)
	if err != nil {
		return screenRead{}, err
	}

	var read screenRead
	if len(result.RawStdout) > 0 && result.RawStdout[len(result.RawStdout)-1] == '\n' {
		read = screenRead{text: trimWaitScreen(string(result.RawStdout)), complete: true}
	}
	for _, line := range result.Stderr {
		report, ok := strings.CutPrefix(line, "unknown command: "+marker)
		if !ok {
			continue
		}
		report, ok = strings.CutSuffix(report, "|")
		id, dead, found := strings.Cut(report, "-")
		if !ok || !found || !read.complete {
			return read, fmt.Errorf("pane %s text wait report %q is unreadable", p.paneID, report)
		}
		switch {
		case id != target:
			return read, &PaneEndedError{Reason: PaneGone}
		case dead == "1":
			return read, &PaneEndedError{Reason: PaneExited}
		}
		return read, nil
	}

	for _, line := range result.Stderr {
		switch {
		case strings.HasPrefix(line, "can't find pane"),
			strings.HasPrefix(line, "can't find window"),
			strings.HasPrefix(line, "can't find session"):
			return read, &PaneEndedError{Reason: PaneGone, cause: waitCommandError(result, false)}
		case strings.HasPrefix(line, "no server running"),
			strings.HasPrefix(line, "error connecting to"),
			line == "server exited unexpectedly",
			line == "lost server":
			return read, &PaneEndedError{Reason: ServerGone, cause: waitCommandError(result, true)}
		}
	}
	return read, fmt.Errorf("pane %s text wait read answered %q", p.paneID, result.Stderr)
}

func waitCommandError(result CommandResult, noServer bool) *CommandError {
	err := newCommandError("capture-pane", result)
	if noServer {
		err.noServer = true
	}
	return err
}

// trimWaitScreen drops the spaces -J leaves at each line end and the blank lines
// after the last row that holds text.
func trimWaitScreen(captured string) string {
	lines := strings.Split(captured, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
