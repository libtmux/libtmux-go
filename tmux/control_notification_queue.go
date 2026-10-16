package tmux

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const defaultControlNotificationLimit = 16 << 20

const controlNotificationHeaderSize = 12

// errControlNotificationsClosed is what a read reports once the caller closed
// the client. It matches [ErrControlClosed], the one sentinel for every end of
// a control client, and [os.ErrClosed], which earlier callers already test.
var errControlNotificationsClosed = fmt.Errorf("%w: %w", ErrControlClosed, os.ErrClosed)

// ErrControlNotificationOverflow identifies a watcher that did not consume
// notifications before its bounded backlog filled. Reads return the
// notifications queued before the overflow and then this error; everything
// tmux sent after it is dropped, so the stream is no longer a faithful record
// even though the process is still running.
//
// Recover by closing the stream and opening another: a stream cannot be
// resumed. The new one starts at tmux's present with no subscriptions armed
// and nothing from the gap, so read the state that mattered directly from tmux
// and Subscribe again; [NotificationStream] describes the steps and
// [NotificationOptions.BacklogBytes] sizes the bound.
var ErrControlNotificationOverflow = errors.New("tmux: control notification backlog overflow")

// ControlNotificationOverflowError reports the backlog limit and the record
// that would have crossed it. The queued prefix remains available first.
type ControlNotificationOverflowError struct {
	// LimitBytes is the maximum retained payload and queue framing.
	LimitBytes int
	// PendingBytes is the unread payload and queue framing retained at overflow.
	PendingBytes int
	// NotificationBytes is the size of the first notification not retained.
	NotificationBytes int
}

// Error describes the bounded backlog without disclosing notification data.
func (e *ControlNotificationOverflowError) Error() string {
	return fmt.Sprintf(
		"%v: limit %d bytes, pending %d bytes, next notification %d bytes",
		ErrControlNotificationOverflow,
		e.LimitBytes,
		e.PendingBytes,
		e.NotificationBytes,
	)
}

// Unwrap makes ControlNotificationOverflowError compatible with
// ErrControlNotificationOverflow.
func (e *ControlNotificationOverflowError) Unwrap() error {
	return ErrControlNotificationOverflow
}

type controlNotificationQueue struct {
	mu       sync.Mutex
	ready    chan struct{}
	limit    int
	data     []byte
	head     int
	used     int
	finished bool
	err      error
	closed   bool

	// reader holds the one token that admits a caller of
	// [ControlClient.NextNotification]. The queue's wake-up signal is a single
	// buffered value, so two readers parked on it can leave a queued record
	// unread; one reader at a time cannot.
	reader chan struct{}
}

func newControlNotificationQueue(limit int) *controlNotificationQueue {
	q := &controlNotificationQueue{
		ready:  make(chan struct{}, 1),
		reader: make(chan struct{}, 1),
		limit:  limit,
	}
	q.releaseReader()
	return q
}

// acquireReader waits for the reader token, or for ctx to end.
func (q *controlNotificationQueue) acquireReader(ctx context.Context) error {
	if q == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-q.reader:
		if err := ctx.Err(); err != nil {
			q.releaseReader()
			return err
		}
		return nil
	}
}

func (q *controlNotificationQueue) releaseReader() {
	if q == nil {
		return
	}
	q.reader <- struct{}{}
}

func (q *controlNotificationQueue) append(sequence uint64, record []byte) error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errControlNotificationsClosed
	}
	if q.finished {
		if q.err != nil {
			return q.err
		}
		return io.ErrClosedPipe
	}
	storedBytes := controlNotificationHeaderSize + len(record)
	pendingBytes := q.used
	if storedBytes > q.limit-pendingBytes {
		q.err = &ControlNotificationOverflowError{
			LimitBytes:        q.limit,
			PendingBytes:      pendingBytes,
			NotificationBytes: len(record),
		}
		q.finished = true
		q.signalLocked()
		return q.err
	}
	q.reserveLocked(storedBytes)
	var header [controlNotificationHeaderSize]byte
	binary.BigEndian.PutUint64(header[:8], sequence)
	binary.BigEndian.PutUint32(header[8:], uint32(len(record)))
	q.writeLocked(header[:])
	q.writeLocked(record)
	q.signalLocked()
	return nil
}

func (q *controlNotificationQueue) reserveLocked(storedBytes int) {
	if q.used+storedBytes <= len(q.data) {
		return
	}
	wanted := q.used + storedBytes
	capacity := max(64, 2*len(q.data), wanted)
	capacity = min(capacity, q.limit)
	data := make([]byte, capacity)
	if q.used != 0 {
		first := min(q.used, len(q.data)-q.head)
		copy(data, q.data[q.head:q.head+first])
		copy(data[first:], q.data[:q.used-first])
	}
	q.data = data
	q.head = 0
}

func (q *controlNotificationQueue) writeLocked(data []byte) {
	tail := (q.head + q.used) % len(q.data)
	first := copy(q.data[tail:], data)
	copy(q.data, data[first:])
	q.used += len(data)
}

func (q *controlNotificationQueue) readLocked(offset int, data []byte) {
	start := (q.head + offset) % len(q.data)
	first := copy(data, q.data[start:])
	copy(data[first:], q.data[:len(data)-first])
}

func (q *controlNotificationQueue) clearLocked(length int) {
	first := min(length, len(q.data)-q.head)
	clear(q.data[q.head : q.head+first])
	clear(q.data[:length-first])
}

func (q *controlNotificationQueue) next(
	ctx context.Context,
	after uint64,
) ([]byte, error) {
	if q == nil {
		return nil, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, ready, err := q.take(after)
		if err != nil || record != nil {
			return record, err
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// nextReady is next without the wait: it returns nil at once when nothing
// after sequence is queued.
func (q *controlNotificationQueue) nextReady(after uint64) ([]byte, error) {
	if q == nil {
		return nil, io.EOF
	}
	record, _, err := q.take(after)
	return record, err
}

// take removes and returns the first queued record after sequence. With none
// queued it returns the queue's end, or the channel that signals the next.
func (q *controlNotificationQueue) take(after uint64) ([]byte, <-chan struct{}, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if q.closed {
			return nil, nil, errControlNotificationsClosed
		}
		if q.used == 0 {
			if !q.finished {
				return nil, q.ready, nil
			}
			if q.err != nil {
				return nil, nil, q.err
			}
			return nil, nil, io.EOF
		}
		var header [controlNotificationHeaderSize]byte
		q.readLocked(0, header[:])
		sequence := binary.BigEndian.Uint64(header[:8])
		length := int(binary.BigEndian.Uint32(header[8:]))
		record := make([]byte, length)
		q.readLocked(controlNotificationHeaderSize, record)
		storedBytes := controlNotificationHeaderSize + length
		q.clearLocked(storedBytes)
		q.head = (q.head + storedBytes) % len(q.data)
		q.used -= storedBytes
		if q.used == 0 {
			q.head = 0
		}
		if sequence > after {
			return record, nil, nil
		}
	}
}

func (q *controlNotificationQueue) finish(terminalErr error) {
	if q == nil {
		return
	}
	q.mu.Lock()
	if !q.closed && !q.finished {
		q.finished = true
		q.err = terminalErr
		q.signalLocked()
	}
	q.mu.Unlock()
}

func (q *controlNotificationQueue) signalLocked() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *controlNotificationQueue) Close() error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		clear(q.data)
		q.data = nil
		q.used = 0
		q.signalLocked()
	}
	q.mu.Unlock()
	return nil
}
