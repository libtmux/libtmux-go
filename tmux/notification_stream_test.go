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
