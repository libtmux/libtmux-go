package tmux

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
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

// %window-close for the observed window is ambiguous: tmux sends it to every
// client whose own attached session still lists the window, regardless of
// which session actually lost it (verified live against a real 3.7c and a
// real next-3.9 server). NextNotification must resolve that with a live
// membership check rather than declaring loss outright - otherwise an
// unrelated session unlinking a window this one still holds would end every
// observation that happens to share it.
func TestPaneObservationVerifiesAmbiguousWindowCloseBeforeDeclaringLoss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		attachedWindows string
		wantLoss        bool
	}{
		{
			name:            "window gone from the attached session: loss",
			attachedWindows: "@9\n",
			wantLoss:        true,
		},
		{
			name:            "window still linked elsewhere's close is a false alarm",
			attachedWindows: "@1\n@9\n",
			wantLoss:        false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, reader := newRequestLoopTestClient(t)
			client.notifications = newControlNotificationQueue(defaultControlNotificationLimit)
			t.Cleanup(func() { _ = client.notifications.Close() })
			if err := client.notifications.append(1, []byte("%window-close @1")); err != nil {
				t.Fatal(err)
			}

			observation := &PaneObservation{
				client:    client,
				paneID:    "%1",
				windowID:  "@1",
				sessionID: "$0",
				state:     newPaneObservationState(),
			}

			type outcome struct {
				notification ControlNotification
				err          error
			}
			result := make(chan outcome, 1)
			go func() {
				notification, err := observation.NextNotification(context.Background())
				result <- outcome{notification: notification, err: err}
			}()

			_ = readRequestLoopLine(t, reader)
			readRequestLoopFence(t, reader)
			completeControlRequest(
				client,
				controlFrame{rawStdout: []byte(test.attachedWindows)},
			)

			select {
			case got := <-result:
				if test.wantLoss {
					if !errors.Is(got.err, ErrPaneObservationLost) {
						t.Fatalf("NextNotification() = (%#v, %v), want ErrPaneObservationLost",
							got.notification, got.err)
					}
					return
				}
				if got.err != nil {
					t.Fatalf("NextNotification() error = %v, want the window-close notification passed through",
						got.err)
				}
				if got.notification.Kind() != ControlNotificationWindowClose {
					t.Fatalf("NextNotification() = %#v, want the window-close notification", got.notification)
				}
			case <-time.After(time.Second):
				t.Fatal("NextNotification() did not return")
			}
		})
	}
}
