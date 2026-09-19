package tmux

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// observationOver returns an observation of %1 fed the given control-mode
// stream, read to its end before the observation is used.
func observationOver(t *testing.T, stream string) *PaneObservation {
	t.Helper()
	queue := newControlNotificationQueue(defaultControlNotificationLimit)
	t.Cleanup(func() { _ = queue.Close() })
	client := &ControlClient{
		stdout:        io.NopCloser(strings.NewReader(stream)),
		notifications: queue,
		frames:        make(chan controlFrame, 1),
		closing:       make(chan struct{}),
		readDone:      make(chan struct{}),
	}
	client.readStream()
	return &PaneObservation{
		client: client, paneID: "%1", windowID: "@1", sessionID: "$1",
		baseline: []string{"ready already on screen"},
		state:    newPaneObservationState(),
	}
}

// WaitFor reads the pane's own writes as text: another pane's output is not
// this pane's, a character split across writes is one character, and writes
// already queued reach match together rather than one at a time.
func TestWaitForMatchesThePanesOwnWritesAsText(t *testing.T) {
	t.Parallel()

	observation := observationOver(t, strings.Join([]string{
		`%output %2 ready from elsewhere`,
		`%output %1 caf\303`,
		`%output %1 \251 \033[1mready\033[0m\015\012`,
		`%output %1 done\015\012`,
	}, "\n")+"\n")

	var calls []string
	got, err := observation.WaitFor(t.Context(), func(text string) bool {
		calls = append(calls, text)
		return strings.Contains(text, "ready")
	})
	if err != nil {
		t.Fatalf("WaitFor() error = %v", err)
	}
	const want = "café ready\ndone\n"
	if got.Text != want {
		t.Errorf("WaitFor() text = %q, want %q", got.Text, want)
	}
	if len(calls) != 1 {
		t.Errorf("match called %d times with %q, want once for writes queued together", len(calls), calls)
	}
}

// The baseline is what was on screen before the wait; matching it would call
// something that had already happened a new event.
func TestWaitForExcludesTheBaselineAndReturnsWhatItReadOnError(t *testing.T) {
	t.Parallel()

	observation := observationOver(t, "%output %1 partial\n%exit\n")
	got, err := observation.WaitFor(context.Background(), func(text string) bool {
		return strings.Contains(text, "ready")
	})
	if !errors.Is(err, ErrPaneObservationLost) {
		t.Fatalf("WaitFor() error = %v, want the stream's end as a lost observation", err)
	}
	if got.Text != "partial" {
		t.Errorf("WaitFor() text = %q, want what was read before the loss", got.Text)
	}
}

// A long wait on a chatty pane keeps the latest output and says what it let go.
func TestWaitForKeepsTheLatestTextWithinItsBound(t *testing.T) {
	t.Parallel()

	line := strings.Repeat("x", 1023)
	var stream strings.Builder
	for range 1100 {
		stream.WriteString("%output %1 " + line + `\012` + "\n")
	}
	stream.WriteString("%output %1 end\n")
	observation := observationOver(t, stream.String())

	got, err := observation.WaitFor(t.Context(), func(text string) bool {
		return strings.HasSuffix(text, "end")
	})
	if err != nil {
		t.Fatalf("WaitFor() error = %v", err)
	}
	if len(got.Text) > paneTextLimit {
		t.Errorf("text is %d bytes, want at most %d", len(got.Text), paneTextLimit)
	}
	if got.DroppedBytes+len(got.Text) != 1100*1024+len("end") {
		t.Errorf("dropped %d + kept %d bytes, want every byte accounted for",
			got.DroppedBytes, len(got.Text))
	}
	if got.DroppedLines == 0 || got.DroppedLines+strings.Count(got.Text, "\n") != 1100 {
		t.Errorf("dropped %d lines, kept %d, want 1100 in all",
			got.DroppedLines, strings.Count(got.Text, "\n"))
	}
}
