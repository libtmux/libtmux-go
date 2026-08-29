package tmux

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestControlLanePoolSignalsEveryClientBeforeWaiting(t *testing.T) {
	tests := []struct {
		name  string
		close func(*controlLanePool) error
	}{
		{name: "Close", close: (*controlLanePool).close},
		{name: "CloseContext", close: func(pool *controlLanePool) error {
			return pool.closeContext(context.Background())
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstEntered := make(chan struct{})
			releaseFirst := make(chan struct{})
			first := newControlPoolCloseTestClient(controlPoolCloseWriter{
				entered: firstEntered,
				release: releaseFirst,
			})
			second := newControlPoolCloseTestClient(controlPoolCloseWriter{})
			pool := newControlLanePool([]*ControlClient{first, second})
			closed := make(chan error, 1)
			go func() { closed <- test.close(pool) }()

			released := false
			release := func() {
				if !released {
					close(releaseFirst)
					released = true
				}
			}
			defer release()
			select {
			case <-firstEntered:
			case <-time.After(time.Second):
				t.Fatal("first client did not begin resource close")
			}
			select {
			case <-second.stopRequests:
			case <-time.After(time.Second):
				t.Fatal("second client was not signaled while the first waited")
			}
			release()
			if err := <-closed; err != nil {
				t.Fatalf("close error = %v", err)
			}
		})
	}
}

type controlPoolCloseWriter struct {
	entered chan struct{}
	release <-chan struct{}
}

func (w controlPoolCloseWriter) Write(data []byte) (int, error) { return len(data), nil }

func (w controlPoolCloseWriter) Close() error {
	if w.entered != nil {
		close(w.entered)
	}
	if w.release != nil {
		<-w.release
	}
	return nil
}

func newControlPoolCloseTestClient(stdin io.WriteCloser) *ControlClient {
	requestDone := make(chan struct{})
	close(requestDone)
	done := make(chan struct{})
	close(done)
	return &ControlClient{
		stdin:         stdin,
		stdout:        io.NopCloser(strings.NewReader("")),
		notifications: newControlNotificationQueue(128),
		stopRequests:  make(chan struct{}),
		requestDone:   requestDone,
		closing:       make(chan struct{}),
		done:          done,
		closeDone:     make(chan struct{}),
	}
}
