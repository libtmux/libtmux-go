package tmux

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/tmux/internal/termtext"
)

// paneTextLimit bounds [PaneText.Text]; a wait on a chatty pane keeps the most
// recent output rather than all of it.
const paneTextLimit = 1 << 20

// PaneText is what a pane wrote after an observation's baseline, as the text a
// person would read: escape sequences removed, UTF-8 joined across writes, and
// each carriage return or backspace that would overwrite text starting a new
// line, so nothing the program wrote is hidden.
type PaneText struct {
	// Text is the most recent output, up to a mebibyte.
	Text string
	// DroppedBytes counts what was dropped from the front of Text to keep it
	// within that bound.
	DroppedBytes int
	// DroppedLines counts the line ends among those dropped bytes.
	DroppedLines int
}

// WaitFor reads what the pane writes after the observation's baseline and
// calls match with all of it each time the pane writes, until match reports
// true, ctx ends, or the observation is lost. It returns the text match
// accepted, or what was read before the error. tmux pushes each write as it
// happens, so nothing is polled.
//
// The baseline is not part of the text: check [PaneObservation.Baseline] for
// what was on screen already. A shell echoes what is typed into it, and the
// echo is output like any other, so open the observation before typing and
// match on what the command prints rather than on the command itself.
//
// Writes that arrived together reach match once. Exactly one read may run at
// a time, as for [PaneObservation.NextNotification], and a malformed
// notification is skipped as [PaneObservation.Reader] skips it.
func (o *PaneObservation) WaitFor(
	ctx context.Context,
	match func(text string) bool,
) (PaneText, error) {
	if o == nil || o.client == nil || o.state == nil {
		return PaneText{}, ErrControlClosed
	}
	if err := o.state.acquireReadToken(ctx); err != nil {
		return PaneText{}, err
	}
	defer o.state.releaseReadToken()

	var result PaneText
	var normalizer termtext.Normalizer
	// text only ever grows, so the string it reports is immutable and match
	// is handed a view of it rather than a fresh copy of up to a mebibyte on
	// every batch. start is where the retained text begins; trimming moves it,
	// and compaction moves what is kept into a new builder, so no string
	// already handed out is ever written to.
	text := new(strings.Builder)
	start := 0
	batch := make([]byte, 0, 4096)
	for {
		written := false
		batch = batch[:0]
		// Block for one write, then take whatever else is already queued, so
		// a pane writing faster than match returns is read in batches. An
		// error behind that batch waits until match has seen it.
		var ended error
		for block := true; ; block = false {
			notification, ok, err := o.read(ctx, block)
			if _, unreadable := errors.AsType[*ControlNotificationError](err); unreadable {
				continue
			}
			if err != nil {
				ended = err
				break
			}
			if !ok {
				break
			}
			pane, output, isOutput := notification.Output()
			if !isOutput || pane != o.paneID || len(output) == 0 {
				continue
			}
			batch = normalizer.AppendChunk(batch, output)
			written = true
		}
		if !written {
			if ended != nil {
				result.Text = strings.Clone(text.String()[start:])
				return result, ended
			}
			continue
		}
		text.Write(batch)
		view := text.String()[start:]
		if excess := len(view) - paneTextLimit; excess > 0 {
			for excess < len(view) && !utf8.RuneStart(view[excess]) {
				excess++
			}
			result.DroppedBytes += excess
			result.DroppedLines += strings.Count(view[:excess], "\n")
			start += excess
			view = view[excess:]
			if start >= paneTextLimit {
				// A copy once per mebibyte dropped, instead of one per batch.
				kept := new(strings.Builder)
				kept.Grow(len(view) + paneTextLimit)
				kept.WriteString(view)
				text, start = kept, 0
				view = text.String()
			}
		}
		if match(view) {
			result.Text = strings.Clone(view)
			return result, nil
		}
		if ended != nil {
			result.Text = strings.Clone(view)
			return result, ended
		}
	}
}
