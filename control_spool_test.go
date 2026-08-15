package tmux

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestControlRecordSpoolPreservesOwnedRecords(t *testing.T) {
	t.Parallel()

	spool, err := newControlRecordSpool()
	if err != nil {
		t.Fatalf("newControlRecordSpool() error = %v", err)
	}
	t.Cleanup(func() {
		if err := spool.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	first := []byte("first")
	if err := spool.append(first); err != nil {
		t.Fatalf("append(first) error = %v", err)
	}
	first[0] = 'X'
	if err := spool.append([]byte{}); err != nil {
		t.Fatalf("append(empty) error = %v", err)
	}
	if err := spool.append([]byte("third")); err != nil {
		t.Fatalf("append(third) error = %v", err)
	}

	for index, want := range [][]byte{[]byte("first"), {}, []byte("third")} {
		got, err := spool.next(context.Background())
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("next(%d) = (%q, %v), want %q", index, got, err, want)
		}
		if len(got) != 0 {
			got[0] = 'X'
		}
	}
}

func TestControlRecordSpoolNextHonorsContextWhileIdle(t *testing.T) {
	t.Parallel()

	spool, err := newControlRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = spool.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := spool.next(ctx)
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

func TestControlRecordSpoolFinishDrainsBeforeEOF(t *testing.T) {
	t.Parallel()

	spool, err := newControlRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = spool.Close() })

	if err := spool.append([]byte("final")); err != nil {
		t.Fatal(err)
	}
	spool.finish(nil)
	if got, err := spool.next(context.Background()); err != nil || string(got) != "final" {
		t.Fatalf("first next() = (%q, %v), want final", got, err)
	}
	if got, err := spool.next(context.Background()); got != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("second next() = (%q, %v), want EOF", got, err)
	}
}

func TestControlRecordSpoolDrainsBeforeTerminalError(t *testing.T) {
	t.Parallel()

	spool, err := newControlRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = spool.Close() })

	if err := spool.append([]byte("queued")); err != nil {
		t.Fatal(err)
	}
	terminalErr := controlProtocolError("stream", "reader failed")
	spool.finish(terminalErr)
	if got, err := spool.next(context.Background()); err != nil || string(got) != "queued" {
		t.Fatalf("first next() = (%q, %v), want queued", got, err)
	}
	if got, err := spool.next(context.Background()); got != nil || !errors.Is(err, ErrControlProtocol) {
		t.Fatalf("second next() = (%q, %v), want protocol error", got, err)
	}
}

func TestControlRecordSpoolCloseIsIdempotentAndRemovesFile(t *testing.T) {
	t.Parallel()

	spool, err := newControlRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	path := spool.path
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat live spool: %v", err)
	}
	if err := spool.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := spool.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat closed spool error = %v, want not exist", err)
	}
	if err := spool.append([]byte("late")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("append() after close error = %v, want os.ErrClosed", err)
	}
	if got, err := spool.next(context.Background()); got != nil || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("next() after close = (%q, %v), want os.ErrClosed", got, err)
	}
}

func TestControlRecordSpoolSupportsConcurrentProducerConsumer(t *testing.T) {
	t.Parallel()

	spool, err := newControlRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = spool.Close() })

	wants := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for _, want := range wants {
			if err := spool.append(want); err != nil {
				t.Errorf("append() error = %v", err)
				return
			}
		}
		spool.finish(nil)
	}()
	for index, want := range wants {
		got, err := spool.next(context.Background())
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("next(%d) = (%q, %v), want %q", index, got, err, want)
		}
	}
	wait.Wait()
}
