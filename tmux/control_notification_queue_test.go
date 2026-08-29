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

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })

	first := []byte("first")
	if err := queue.append(first); err != nil {
		t.Fatalf("append(first) error = %v", err)
	}
	first[0] = 'X'
	if err := queue.append([]byte("second")); err != nil {
		t.Fatalf("append(second) error = %v", err)
	}

	for index, want := range [][]byte{[]byte("first"), []byte("second")} {
		got, err := queue.next(context.Background())
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("next(%d) = (%q, %v), want %q", index, got, err, want)
		}
	}
}

func TestControlNotificationQueueNextHonorsContextWhileIdle(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := queue.next(ctx)
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

func TestControlNotificationQueueFinishDrainsBeforeEOF(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })

	if err := queue.append([]byte("final")); err != nil {
		t.Fatal(err)
	}
	queue.finish(nil)
	if got, err := queue.next(context.Background()); err != nil || string(got) != "final" {
		t.Fatalf("first next() = (%q, %v), want final", got, err)
	}
	if got, err := queue.next(context.Background()); got != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("second next() = (%q, %v), want EOF", got, err)
	}
}

func TestControlNotificationQueueDrainsBeforeTerminalError(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })

	if err := queue.append([]byte("queued")); err != nil {
		t.Fatal(err)
	}
	queue.finish(controlProtocolError("stream", "reader failed"))
	if got, err := queue.next(context.Background()); err != nil || string(got) != "queued" {
		t.Fatalf("first next() = (%q, %v), want queued", got, err)
	}
	if got, err := queue.next(context.Background()); got != nil || !errors.Is(err, ErrControlProtocol) {
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
	if err := queue.append([]byte("late")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("append() after close error = %v, want closed", err)
	}
	if got, err := queue.next(context.Background()); got != nil ||
		!errors.Is(err, os.ErrClosed) {
		t.Fatalf("next() after close = (%q, %v), want closed", got, err)
	}
}

func TestControlNotificationQueueSupportsConcurrentProducerConsumer(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(32)
	t.Cleanup(func() { _ = queue.Close() })

	wants := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	var wait sync.WaitGroup
	wait.Go(func() {
		for _, want := range wants {
			if err := queue.append(want); err != nil {
				t.Errorf("append() error = %v", err)
				return
			}
		}
		queue.finish(nil)
	})
	for index, want := range wants {
		got, err := queue.next(context.Background())
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

	if err := queue.append([]byte("first")); err != nil {
		t.Fatalf("append(first) error = %v", err)
	}
	if err := queue.append([]byte("overflow")); !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("append(overflow) error = %v, want overflow", err)
	}
	if err := queue.append([]byte("late")); !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("append(late) error = %v, want the terminal overflow", err)
	}

	got, err := queue.next(context.Background())
	if err != nil || string(got) != "first" {
		t.Fatalf("first next() = (%q, %v), want first", got, err)
	}
	got, err = queue.next(context.Background())
	if got != nil || !errors.Is(err, ErrControlNotificationOverflow) {
		t.Fatalf("second next() = (%q, %v), want overflow", got, err)
	}
	var overflow *ControlNotificationOverflowError
	if !errors.As(err, &overflow) || overflow.LimitBytes != 9 ||
		overflow.PendingBytes != 9 || overflow.NotificationBytes != 8 {
		t.Fatalf("overflow detail = %#v", overflow)
	}
}

func TestControlNotificationQueueReusesConsumedCapacity(t *testing.T) {
	t.Parallel()

	queue := newControlNotificationQueue(2 * (controlNotificationHeaderSize + 4))
	t.Cleanup(func() { _ = queue.Close() })
	for _, record := range []string{"one", "two2"} {
		if err := queue.append([]byte(record)); err != nil {
			t.Fatalf("append(%q) error = %v", record, err)
		}
	}
	if got, err := queue.next(context.Background()); err != nil || string(got) != "one" {
		t.Fatalf("first next() = (%q, %v), want one", got, err)
	}
	if err := queue.append([]byte("thre")); err != nil {
		t.Fatalf("append(reused capacity) error = %v", err)
	}
	for index, want := range []string{"two2", "thre"} {
		got, err := queue.next(context.Background())
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
	for range b.N {
		if err := queue.append(payload); err != nil {
			b.Fatal(err)
		}
		if _, err := queue.next(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
