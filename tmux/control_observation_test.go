package tmux

import (
	"io"
	"strings"
	"testing"
)

// A malformed notification belongs to whatever else shares the connection, so
// it must not end one pane's reader. PaneObservation.Notifications skips it and
// the reader is documented to agree.
func TestPaneObservationReaderSkipsAMalformedNotification(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(defaultControlNotificationLimit)
	t.Cleanup(func() { _ = queue.Close() })
	client := &ControlClient{
		stdout: io.NopCloser(strings.NewReader(
			"%not-a-notification-this-build-knows\n" +
				"%output %1 heard\n",
		)),
		notifications: queue,
		frames:        make(chan controlFrame, 1),
		closing:       make(chan struct{}),
		readDone:      make(chan struct{}),
	}
	client.readStream()

	observation := &PaneObservation{
		client: client,
		paneID: "%1",
		state:  newPaneObservationState(),
	}
	buffer := make([]byte, 16)
	n, err := observation.Reader(t.Context()).Read(buffer)
	if err != nil {
		t.Fatalf("Read() error = %v, want the malformed notification skipped", err)
	}
	if got := string(buffer[:n]); got != "heard" {
		t.Errorf("Read() = %q, want %q", got, "heard")
	}
}
