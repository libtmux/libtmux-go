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

const controlRecordHeaderSize = 8

type controlRecordSpool struct {
	mu    sync.Mutex
	ready chan struct{}
	file  *os.File
	path  string

	size        int64
	readOffset  int64
	finished    bool
	terminalErr error
	closed      bool

	closeOnce sync.Once
	closeErr  error
}

func newControlRecordSpool() (*controlRecordSpool, error) {
	file, err := os.CreateTemp("", "libtmux-control-notifications-*")
	if err != nil {
		return nil, sanitizeControlSpoolError("create control notification spool", err)
	}
	return &controlRecordSpool{
		ready: make(chan struct{}),
		file:  file,
		path:  file.Name(),
	}, nil
}

func (s *controlRecordSpool) append(record []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	if s.finished {
		return io.ErrClosedPipe
	}

	stored := make([]byte, controlRecordHeaderSize+len(record))
	binary.BigEndian.PutUint64(stored, uint64(len(record)))
	copy(stored[controlRecordHeaderSize:], record)
	written, err := s.file.WriteAt(stored, s.size)
	if err == nil && written != len(stored) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return sanitizeControlSpoolError("write control notification spool", err)
	}
	s.size += int64(written)
	s.signalLocked()
	return nil
}

func (s *controlRecordSpool) next(ctx context.Context) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, os.ErrClosed
		}
		if s.readOffset != s.size {
			record, err := s.readRecordLocked()
			s.mu.Unlock()
			return record, err
		}
		if s.finished {
			terminalErr := s.terminalErr
			s.mu.Unlock()
			if terminalErr != nil {
				return nil, terminalErr
			}
			return nil, io.EOF
		}
		ready := s.ready
		s.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *controlRecordSpool) readRecordLocked() ([]byte, error) {
	var header [controlRecordHeaderSize]byte
	if _, err := s.file.ReadAt(header[:], s.readOffset); err != nil {
		return nil, sanitizeControlSpoolError("read control notification spool header", err)
	}
	length := binary.BigEndian.Uint64(header[:])
	maxInt := int(^uint(0) >> 1)
	if length > uint64(maxInt) || length > uint64(s.size-s.readOffset-controlRecordHeaderSize) {
		return nil, errors.New("control notification spool record length is invalid")
	}
	record := make([]byte, int(length))
	if len(record) != 0 {
		if _, err := s.file.ReadAt(record, s.readOffset+controlRecordHeaderSize); err != nil {
			return nil, sanitizeControlSpoolError("read control notification spool record", err)
		}
	}
	s.readOffset += controlRecordHeaderSize + int64(length)
	return record, nil
}

func (s *controlRecordSpool) finish(terminalErr error) {
	s.mu.Lock()
	if !s.closed && !s.finished {
		s.finished = true
		s.terminalErr = terminalErr
		s.signalLocked()
	}
	s.mu.Unlock()
}

func (s *controlRecordSpool) signalLocked() {
	close(s.ready)
	s.ready = make(chan struct{})
}

func (s *controlRecordSpool) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.signalLocked()
		file := s.file
		path := s.path
		s.mu.Unlock()

		closeErr := file.Close()
		if errors.Is(closeErr, os.ErrClosed) {
			closeErr = nil
		}
		removeErr := os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		s.closeErr = errors.Join(
			sanitizeControlSpoolError("close control notification spool", closeErr),
			sanitizeControlSpoolError("remove control notification spool", removeErr),
		)
	})
	return s.closeErr
}

func sanitizeControlSpoolError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if pathErr, ok := errors.AsType[*os.PathError](err); ok {
		err = pathErr.Err
	}
	return fmt.Errorf("%s: %w", operation, err)
}
