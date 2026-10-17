package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// waitPollInterval is how often a poll-backed wait reads the pane. It reads
// only below tmux 3.7, where no control observation exists.
const waitPollInterval = 100 * time.Millisecond

// waitTextLimit bounds the text a poll-backed wait keeps, like a control
// observation's mebibyte.
const waitTextLimit = 1 << 20

// pollingObservation follows pane output with capture-pane reads from a
// cursor, for daemons that cannot host the control observation wait_for_text
// otherwise uses. It reports only rows written after it attached.
type pollingObservation struct {
	pane     tmux.Pane
	baseline []string
	cursor   captureCursor
}

func openPollingObservation(ctx context.Context, pane tmux.Pane) (*pollingObservation, error) {
	read, err := readVisible(ctx, pane, 0)
	if err != nil {
		return nil, err
	}
	return &pollingObservation{
		pane:     pane,
		baseline: read.lines,
		cursor:   cursorForRead(pane.ID().String(), read.state, read),
	}, nil
}

// Baseline returns the screen at attachment.
func (o *pollingObservation) Baseline() []string { return append([]string(nil), o.baseline...) }

// WaitFor reads new rows until match accepts the text so far, ctx ends or the
// pane goes away. match runs only when new rows arrived, as it does for a
// control observation that runs only on a write.
func (o *pollingObservation) WaitFor(
	ctx context.Context,
	match func(string) bool,
) (tmux.PaneText, error) {
	var text strings.Builder
	var result tmux.PaneText
	ticker := time.NewTicker(waitPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Text = text.String()
			return result, ctx.Err()
		case <-ticker.C:
		}
		read, err := readDelta(ctx, o.pane, o.cursor)
		if err != nil {
			result.Text = text.String()
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, err
		}
		o.cursor = cursorForRead(o.pane.ID().String(), read.state, read)
		if len(read.lines) == 0 {
			continue
		}
		if text.Len() > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(strings.Join(read.lines, "\n"))
		if text.Len() > waitTextLimit {
			kept := text.String()
			cut := len(kept) - waitTextLimit
			result.DroppedBytes += cut
			result.DroppedLines += strings.Count(kept[:cut], "\n")
			text.Reset()
			text.WriteString(kept[cut:])
		}
		result.Text = text.String()
		if match(result.Text) {
			return result, nil
		}
	}
}

// controlObservationUnavailable reports that the daemon is too old to host
// the control observation, so the wait polls instead.
func controlObservationUnavailable(ctx context.Context, pane tmux.Pane) (bool, error) {
	minimum, err := tmux.ParseVersion(tmux.MinimumConnectionVersion)
	if err != nil {
		return false, err
	}
	err = pane.Server().RequireVersion(ctx, minimum)
	var tooLow *tmux.VersionTooLowError
	if errors.As(err, &tooLow) {
		return true, nil
	}
	return false, err
}
