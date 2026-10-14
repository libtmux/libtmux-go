package tmux

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
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

// tmux 3.8 tightened refresh-client -B's target grammar (monitor_parse in
// monitor.c) to require an exactly empty target for session scope; any other
// non-pane, non-window string, including a literal session ID, is now a parse
// failure that silently drops the subscription instead of arming it. A
// session ID target used to work by accident on every earlier tested
// version, whose parser fell back to session scope for anything that was not
// pane- or window-shaped - verified directly against a real 3.7c and a real
// next-3.9 server. Session scope has never selected a session tmux did not
// already have attached, so rendering it empty changes nothing tmux acts on.
func TestSubscriptionRequestScopeRendersAnEmptyTargetForSessionScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request SubscriptionRequest
		want    string
	}{
		{name: "no scope", request: SubscriptionRequest{}, want: ""},
		{
			name:    "window scope",
			request: SubscriptionRequest{Window: "@3"},
			want:    "@3",
		},
		{
			name:    "pane scope",
			request: SubscriptionRequest{Pane: "%5"},
			want:    "%5",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.request.scope(); got != test.want {
				t.Fatalf("scope() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNotificationOptionsBacklog(t *testing.T) {
	t.Parallel()

	for _, bad := range []int{-1, 1, minNotificationBacklog - 1} {
		if err := (NotificationOptions{BacklogBytes: bad}).validate(); !errors.Is(err, ErrInvalidServerCommandRequest) {
			t.Errorf("BacklogBytes %d validate() = %v, want an invalid request", bad, err)
		}
	}
	for _, good := range []int{0, minNotificationBacklog, 1 << 30} {
		if err := (NotificationOptions{BacklogBytes: good}).validate(); err != nil {
			t.Errorf("BacklogBytes %d validate() = %v, want accepted", good, err)
		}
	}
	if got := (NotificationOptions{}).backlog(); got != defaultControlNotificationLimit {
		t.Errorf("zero BacklogBytes selects %d, want the default %d", got, defaultControlNotificationLimit)
	}
	if got := (NotificationOptions{BacklogBytes: 1 << 20}).backlog(); got != 1<<20 {
		t.Errorf("BacklogBytes 1 MiB selects %d", got)
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

// Two readers share one wake-up signal, and a record queued while both are
// about to wait wakes only one of them. A single reader leaves no second
// reader to be missed: the later one waits its turn and finds the record.
func TestNotificationStreamNextSerializesConcurrentReaders(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(1 << 10)
	t.Cleanup(func() { _ = queue.Close() })
	stream := &NotificationStream{client: &ControlClient{notifications: queue}}

	readers := [2]*gatedDoneContext{newGatedDoneContext(), newGatedDoneContext()}
	results := make(chan error, 2)
	for _, reader := range readers {
		go func() {
			_, err := stream.Next(reader)
			results <- err
		}()
		<-reader.reached
	}
	// Each reader is now at the point of waiting, with nothing yet queued.
	for sequence := uint64(1); sequence <= 2; sequence++ {
		if err := queue.append(sequence, []byte("%session-renamed $1 renamed")); err != nil {
			t.Fatal(err)
		}
	}
	for index, reader := range readers {
		close(reader.release)
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("reader %d error = %v, want a notification", index, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("reader %d still blocked with a record queued", index)
		}
	}
}

// gatedDoneContext holds its first Done call until release closes, so a test
// can park a reader exactly where it is about to wait.
type gatedDoneContext struct {
	context.Context
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedDoneContext() *gatedDoneContext {
	return &gatedDoneContext{
		Context: context.Background(),
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *gatedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() {
		close(c.reached)
		<-c.release
	})
	return c.Context.Done()
}

func TestNotificationStreamNextHonorsCancellationWhileWaitingForTheReader(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(1 << 10)
	t.Cleanup(func() { _ = queue.Close() })
	stream := &NotificationStream{client: &ControlClient{notifications: queue}}

	holder, stopHolder := context.WithCancel(context.Background())
	held := &doneObservedContext{Context: holder, observed: make(chan struct{})}
	holderResult := make(chan error, 1)
	go func() {
		_, err := stream.Next(held)
		holderResult <- err
	}()
	<-held.observed

	waiter, stopWaiter := context.WithCancel(context.Background())
	waiting := &doneObservedContext{Context: waiter, observed: make(chan struct{})}
	waiterResult := make(chan error, 1)
	go func() {
		_, err := stream.Next(waiting)
		waiterResult <- err
	}()
	<-waiting.observed
	stopWaiter()
	select {
	case err := <-waiterResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting reader error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting reader ignored its context")
	}
	stopHolder()
	if err := <-holderResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("holding reader error = %v, want context.Canceled", err)
	}
}

// A caller writes one errors.Is check for every way a read ends: ErrControlClosed
// for any end, os.ErrClosed kept for callers that already test it, and
// ErrControlStreamLost only when the caller did not ask for it.
func TestNotificationStreamReadsReportOneSentinelAfterEveryEnd(t *testing.T) {
	t.Parallel()

	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		stream := newClosableNotificationStream()
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := stream.Next(context.Background())
		if !errors.Is(err, ErrControlClosed) || !errors.Is(err, os.ErrClosed) ||
			errors.Is(err, ErrControlStreamLost) {
			t.Fatalf("Next() after Close() = %v, want ErrControlClosed and os.ErrClosed, no loss", err)
		}
	})
	for _, test := range []struct{ name, wire string }{
		{"lost naming a reason", "%exit server exited\n"},
		{"lost without a reason", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stream := &NotificationStream{client: newQueuedNotificationClient(t, test.wire)}
			if test.wire != "" {
				if _, err := stream.Next(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			_, err := stream.Next(context.Background())
			if !errors.Is(err, ErrControlClosed) || !errors.Is(err, ErrControlStreamLost) {
				t.Fatalf("Next() after the stream ended = %v, want ErrControlClosed and ErrControlStreamLost", err)
			}
		})
	}
}
