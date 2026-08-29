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

const controlNotificationHeaderSize = 4

// ErrControlNotificationOverflow identifies a watcher that did not consume
// notifications before its bounded backlog filled.
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
}

func newControlNotificationQueue(limit int) *controlNotificationQueue {
	return &controlNotificationQueue{
		ready: make(chan struct{}, 1),
		limit: limit,
	}
}

func (q *controlNotificationQueue) append(record []byte) error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return os.ErrClosed
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
	binary.BigEndian.PutUint32(header[:], uint32(len(record)))
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

func (q *controlNotificationQueue) next(ctx context.Context) ([]byte, error) {
	if q == nil {
		return nil, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return nil, os.ErrClosed
		}
		if q.used != 0 {
			var header [controlNotificationHeaderSize]byte
			q.readLocked(0, header[:])
			length := int(binary.BigEndian.Uint32(header[:]))
			record := make([]byte, length)
			q.readLocked(controlNotificationHeaderSize, record)
			storedBytes := controlNotificationHeaderSize + length
			q.clearLocked(storedBytes)
			q.head = (q.head + storedBytes) % len(q.data)
			q.used -= storedBytes
			if q.used == 0 {
				q.head = 0
			}
			q.mu.Unlock()
			return record, nil
		}
		if q.finished {
			terminalErr := q.err
			q.mu.Unlock()
			if terminalErr != nil {
				return nil, terminalErr
			}
			return nil, io.EOF
		}
		ready := q.ready
		q.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
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
