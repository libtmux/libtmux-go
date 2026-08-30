package tmux

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNotificationStreamReturnsTheNextNotification(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(128)
	t.Cleanup(func() { _ = queue.Close() })
	if err := queue.append(1, []byte("%session-renamed $1 renamed")); err != nil {
		t.Fatal(err)
	}
	stream := &NotificationStream{
		client: &ControlClient{notifications: queue},
	}

	notification, err := stream.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if notification.Kind() != ControlNotificationSessionRenamed ||
		!slices.Equal(notification.Arguments(), []string{"$1", "renamed"}) {
		t.Fatalf("Next() = %#v, want the queued session rename", notification)
	}
}

func TestNotificationStreamCloseClosesItsNotifications(t *testing.T) {
	t.Parallel()

	stream := newClosableNotificationStream()

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Next() after Close() error = %v, want os.ErrClosed", err)
	}
}

func TestNotificationStreamCloseContextStartsShutdownWhenCanceled(t *testing.T) {
	t.Parallel()

	stream := newClosableNotificationStream()
	t.Cleanup(func() { _ = stream.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := stream.CloseContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext() error = %v, want context.Canceled", err)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	if _, err := stream.Next(readCtx); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Next() after canceled CloseContext() error = %v, want os.ErrClosed", err)
	}
}

func TestOpenNotificationsRejectsAnUnmaterializedSession(t *testing.T) {
	t.Parallel()

	runner := &versionQueueRunner{}
	server := serverWithRunner(runner)
	stream, err := (Session{server: server}).OpenNotifications(
		context.Background(),
		NotificationOptions{},
	)
	if stream != nil || !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("OpenNotifications() = (%p, %v), want nil request error", stream, err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("OpenNotifications() started %d processes, want none", calls)
	}

	stream, err = server.OpenNotifications(
		context.Background(),
		Session{server: server},
		NotificationOptions{},
	)
	if stream != nil || !errors.Is(err, ErrInvalidServerCommandRequest) {
		t.Fatalf("Server.OpenNotifications() = (%p, %v), want nil request error", stream, err)
	}
	if calls := runner.callCount(); calls != 0 {
		t.Fatalf("Server.OpenNotifications() started %d processes, want none", calls)
	}
}

type notificationStreamWriteCloser struct{}

func (notificationStreamWriteCloser) Write(data []byte) (int, error) {
	return len(data), nil
}

func (notificationStreamWriteCloser) Close() error { return nil }

func newClosableNotificationStream() *NotificationStream {
	requestDone := make(chan struct{})
	close(requestDone)
	done := make(chan struct{})
	close(done)
	return &NotificationStream{client: &ControlClient{
		stdin:         notificationStreamWriteCloser{},
		stdout:        io.NopCloser(strings.NewReader("")),
		notifications: newControlNotificationQueue(128),
		stopRequests:  make(chan struct{}),
		requestDone:   requestDone,
		closing:       make(chan struct{}),
		done:          done,
		closeDone:     make(chan struct{}),
	}}
}

func TestNotificationOptionsRejectAnUnusableHold(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		options NotificationOptions
	}{
		{
			name:    "below tmux's resolution",
			options: NotificationOptions{IncludePaneOutput: true, PauseAfter: time.Millisecond},
		},
		{
			name:    "without the output it holds",
			options: NotificationOptions{PauseAfter: time.Minute},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.options.validate(); !errors.Is(err, ErrInvalidServerCommandRequest) {
				t.Fatalf("validate() = %v, want an invalid request", err)
			}
		})
	}
	usable := NotificationOptions{IncludePaneOutput: true, PauseAfter: time.Minute}
	if err := usable.validate(); err != nil {
		t.Fatalf("validate() = %v, want a usable hold accepted", err)
	}
}

func TestResumablePaneIDMatchesTmuxsParser(t *testing.T) {
	t.Parallel()

	// tmux resumes only "%" followed by digits, and discards anything else
	// without reporting it.
	for id, want := range map[PaneID]bool{
		"%0": true, "%12": true,
		"": false, "%": false, "0": false, "@1": false, "%1a": false, "%1:continue": false,
	} {
		if got := resumablePaneID(id); got != want {
			t.Errorf("resumablePaneID(%q) = %t, want %t", id, got, want)
		}
	}
}
