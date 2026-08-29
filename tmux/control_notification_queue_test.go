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

func TestControlNotificationQueueOwnsAndOrdersRecords(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(64)
	t.Cleanup(func() { _ = queue.Close() })

	first := []byte("first")
	if err := queue.append(1, first); err != nil {
		t.Fatalf("append(first) error = %v", err)
	}
	first[0] = 'X'
	if err := queue.append(2, []byte("second")); err != nil {
		t.Fatalf("append(second) error = %v", err)
	}

	for index, want := range [][]byte{[]byte("first"), []byte("second")} {
		got, err := queue.next(context.Background(), 0)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("next(%d) = (%q, %v), want %q", index, got, err, want)
		}
	}
}

func TestControlNotificationQueueNextHonorsContextWhileIdle(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(64)
	t.Cleanup(func() { _ = queue.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := queue.next(ctx, 0)
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("next() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("next() did not unblock after context cancellation")
	}
}

func TestControlNotificationQueueSkipsThroughWireBoundary(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(96)
	t.Cleanup(func() { _ = queue.Close() })
	for sequence, record := range []string{"before", "at", "after"} {
		if err := queue.append(uint64(sequence+4), []byte(record)); err != nil {
			t.Fatalf("append(%q) error = %v", record, err)
		}
	}

	got, err := queue.next(context.Background(), 5)
	if err != nil || string(got) != "after" {
		t.Fatalf("next(after 5) = (%q, %v), want after", got, err)
	}
}

func TestPaneObservationOwnsBaselineAndWireBoundary(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(128)
	t.Cleanup(func() { _ = queue.Close() })
	if err := queue.append(2, []byte("%output %1 before")); err != nil {
		t.Fatal(err)
	}
	if err := queue.append(4, []byte("%output %1 after")); err != nil {
		t.Fatal(err)
	}
	baseline := []string{"visible"}
	observation := newTestPaneObservation(queue)
	observation.after = 3
	observation.baseline = baseline
	gotBaseline := observation.Baseline()
	gotBaseline[0] = "also mutated"
	if again := observation.Baseline(); !slices.Equal(again, []string{"visible"}) {
		t.Fatalf("Baseline() = %q, want owned visible line", again)
	}

	notification, err := observation.NextNotification(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	paneID, output, ok := notification.Output()
	if !ok || paneID != "%1" || string(output) != "after" {
		t.Fatalf("NextNotification() = (%q, %q, %t), want post-boundary output", paneID, output, ok)
	}
}

func TestPaneObservationReportsTopologyLoss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record string
	}{
		{name: "window unlinked", record: "%unlinked-window-close @1"},
		{name: "session changed", record: "%session-changed $2 other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			queue := newControlNotificationQueue(128)
			t.Cleanup(func() { _ = queue.Close() })
			if err := queue.append(4, []byte(test.record)); err != nil {
				t.Fatal(err)
			}
			if err := queue.append(5, []byte("%output %1 must-not-escape")); err != nil {
				t.Fatal(err)
			}
			observation := newTestPaneObservation(queue)
			observation.after = 3
			copied := *observation
			for read, reader := range []*PaneObservation{observation, &copied} {
				if _, err := reader.NextNotification(context.Background()); !errors.Is(err, ErrPaneObservationLost) {
					t.Fatalf("NextNotification() read %d error = %v, want terminal ErrPaneObservationLost", read+1, err)
				}
			}
		})
	}
}

func TestPaneObservationSerializesReadersAtLoss(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(128)
	t.Cleanup(func() { _ = queue.Close() })
	for sequence, record := range []string{
		"%unlinked-window-close @1",
		"%output %1 must-not-escape",
	} {
		if err := queue.append(uint64(sequence+1), []byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	observation := newTestPaneObservation(queue)
	start := make(chan struct{})
	results := make(chan error, 2)
	var readers sync.WaitGroup
	for range 2 {
		readers.Go(func() {
			<-start
			_, err := observation.NextNotification(context.Background())
			results <- err
		})
	}
	close(start)
	readers.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, ErrPaneObservationLost) {
			t.Fatalf("NextNotification() error = %v, want shared ErrPaneObservationLost", err)
		}
	}
}

func TestPaneObservationReaderCancellationIsRetryable(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(128)
	t.Cleanup(func() { _ = queue.Close() })
	observation := newTestPaneObservation(queue)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := observation.NextNotification(firstCtx)
		firstDone <- err
	}()
	select {
	case err := <-firstDone:
		t.Fatalf("first reader returned before cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := observation.NextNotification(secondCtx)
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second reader returned before cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancelSecond()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second reader error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued reader did not honor context cancellation")
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first reader error = %v, want context canceled", err)
	}
	if err := queue.append(1, []byte("%output %1 retry")); err != nil {
		t.Fatal(err)
	}
	if _, err := observation.NextNotification(context.Background()); err != nil {
		t.Fatalf("NextNotification() after cancellation error = %v, want retry", err)
	}
}

func TestPaneObservationClassifiesTerminalStreamLoss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		finish error
		cause  error
	}{
		{name: "clean stream end", cause: io.EOF},
		{
			name:   "protocol failure",
			finish: controlProtocolError("stream", "reader failed"),
			cause:  ErrControlProtocol,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			queue := newControlNotificationQueue(128)
			t.Cleanup(func() { _ = queue.Close() })
			queue.finish(test.finish)
			observation := newTestPaneObservation(queue)
			for read := range 2 {
				_, err := observation.NextNotification(context.Background())
				if !errors.Is(err, ErrPaneObservationLost) || !errors.Is(err, test.cause) {
					t.Fatalf("NextNotification() read %d error = %v, want observation loss and %v", read+1, err, test.cause)
				}
			}
		})
	}
}

func TestPaneObservationKeepsNotificationErrorsRetryable(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(128)
	t.Cleanup(func() { _ = queue.Close() })
	for sequence, record := range []string{
		"%future-notification argument",
		"%output %1 retry",
	} {
		if err := queue.append(uint64(sequence+1), []byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	observation := newTestPaneObservation(queue)
	if _, err := observation.NextNotification(context.Background()); !errors.Is(err, ErrUnknownControlNotification) || errors.Is(err, ErrPaneObservationLost) {
		t.Fatalf("NextNotification() error = %v, want retryable unknown notification", err)
	}
	if _, err := observation.NextNotification(context.Background()); err != nil {
		t.Fatalf("NextNotification() after parse error = %v, want retry", err)
	}
}

func TestPaneObservationCloseIsNotLoss(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(128)
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	observation := newTestPaneObservation(queue)
	if _, err := observation.NextNotification(context.Background()); !errors.Is(err, os.ErrClosed) || errors.Is(err, ErrPaneObservationLost) {
		t.Fatalf("NextNotification() error = %v, want closed without observation loss", err)
	}
}

func newTestPaneObservation(queue *controlNotificationQueue) *PaneObservation {
	return &PaneObservation{
		client: &ControlClient{notifications: queue},
		paneID: "%1", windowID: "@1", sessionID: "$1",
		state: newPaneObservationState(),
	}
}

func TestControlNotificationQueueFinishDrainsBeforeEOF(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })

	if err := queue.append(1, []byte("final")); err != nil {
		t.Fatal(err)
	}
	queue.finish(nil)
	if got, err := queue.next(context.Background(), 0); err != nil || string(got) != "final" {
		t.Fatalf("first next() = (%q, %v), want final", got, err)
	}
	if got, err := queue.next(context.Background(), 0); got != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("second next() = (%q, %v), want EOF", got, err)
	}
}

func TestControlNotificationQueueDrainsBeforeTerminalError(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })

	if err := queue.append(1, []byte("queued")); err != nil {
		t.Fatal(err)
	}
	queue.finish(controlProtocolError("stream", "reader failed"))
	if got, err := queue.next(context.Background(), 0); err != nil || string(got) != "queued" {
		t.Fatalf("first next() = (%q, %v), want queued", got, err)
	}
	if got, err := queue.next(context.Background(), 0); got != nil || !errors.Is(err, ErrControlProtocol) {
		t.Fatalf("second next() = (%q, %v), want protocol error", got, err)
	}
}

func TestControlNotificationQueueCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	if err := queue.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := queue.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := queue.append(1, []byte("late")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("append() after close error = %v, want closed", err)
	}
	if got, err := queue.next(context.Background(), 0); got != nil ||
		!errors.Is(err, os.ErrClosed) {
		t.Fatalf("next() after close = (%q, %v), want closed", got, err)
	}
}

func TestControlNotificationQueueSupportsConcurrentProducerConsumer(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(64)
	t.Cleanup(func() { _ = queue.Close() })

	wants := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	var wait sync.WaitGroup
	wait.Go(func() {
		for index, want := range wants {
			if err := queue.append(uint64(index+1), want); err != nil {
				t.Errorf("append() error = %v", err)
				return
			}
		}
		queue.finish(nil)
	})
	for index, want := range wants {
		got, err := queue.next(context.Background(), 0)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("next(%d) = (%q, %v), want %q", index, got, err, want)
		}
	}
	wait.Wait()
}

func TestControlNotificationQueueDrainsBeforeOverflow(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(controlNotificationHeaderSize + 5)
	t.Cleanup(func() { _ = queue.Close() })

	if err := queue.append(1, []byte("first")); err != nil {
		t.Fatalf("append(first) error = %v", err)
	}
	if err := queue.append(2, []byte("overflow")); !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("append(overflow) error = %v, want overflow", err)
	}
	if err := queue.append(3, []byte("late")); !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("append(late) error = %v, want the terminal overflow", err)
	}

	got, err := queue.next(context.Background(), 0)
	if err != nil || string(got) != "first" {
		t.Fatalf("first next() = (%q, %v), want first", got, err)
	}
	got, err = queue.next(context.Background(), 0)
	if got != nil || !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("second next() = (%q, %v), want overflow", got, err)
	}
	var overflow *ControlNotificationOverflowError
	if !errors.As(err, &overflow) ||
		overflow.LimitBytes != controlNotificationHeaderSize+5 ||
		overflow.PendingBytes != controlNotificationHeaderSize+5 ||
		overflow.NotificationBytes != 8 {
		t.Fatalf("overflow detail = %#v", overflow)
	}
}

func TestControlNotificationQueueReusesConsumedCapacity(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(2 * (controlNotificationHeaderSize + 4))
	t.Cleanup(func() { _ = queue.Close() })
	for index, record := range []string{"one", "two2"} {
		if err := queue.append(uint64(index+1), []byte(record)); err != nil {
			t.Fatalf("append(%q) error = %v", record, err)
		}
	}
	if got, err := queue.next(context.Background(), 0); err != nil || string(got) != "one" {
		t.Fatalf("first next() = (%q, %v), want one", got, err)
	}
	if err := queue.append(3, []byte("thre")); err != nil {
		t.Fatalf("append(reused capacity) error = %v", err)
	}
	for index, want := range []string{"two2", "thre"} {
		got, err := queue.next(context.Background(), 0)
		if err != nil || string(got) != want {
			t.Fatalf("next(%d) = (%q, %v), want %q", index, got, err, want)
		}
	}
	if len(queue.data) > queue.limit {
		t.Fatalf("queue capacity = %d, limit = %d", len(queue.data), queue.limit)
	}
}

func TestControlClientOverflowDoesNotBlockLaterFrames(t *testing.T) {
	t.Parallel()

	first := "%session-renamed $1 first"
	queue := newControlNotificationQueue(controlNotificationHeaderSize + len(first))
	t.Cleanup(func() { _ = queue.Close() })
	closing := make(chan struct{})
	client := &ControlClient{
		stdout: io.NopCloser(strings.NewReader(
			first + "\n" +
				"%session-renamed $1 overflow\n" +
				"%begin 1 2 1\nreply\n%end 1 2 1\n",
		)),
		notifications: queue,
		frames:        make(chan controlFrame, 1),
		closing:       closing,
		readDone:      make(chan struct{}),
	}
	client.readStream()

	frame, err := client.nextFrame(context.Background())
	if err != nil || string(frame.rawStdout) != "reply\n" {
		t.Fatalf("nextFrame() = (%#v, %v), want reply", frame, err)
	}
	notification, err := client.NextNotification(context.Background())
	if err != nil || notification.Kind() != ControlNotificationSessionRenamed {
		t.Fatalf("first NextNotification() = (%#v, %v)", notification, err)
	}
	if _, err := client.NextNotification(context.Background()); !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("second NextNotification() error = %v, want overflow", err)
	}
}

func BenchmarkControlNotificationQueueAppendAndDrain(b *testing.B) {
	queue := newControlNotificationQueue(defaultControlNotificationLimit)
	payload := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		if err := queue.append(uint64(index+1), payload); err != nil {
			b.Fatal(err)
		}
		if _, err := queue.next(context.Background(), 0); err != nil {
			b.Fatal(err)
		}
	}
}
