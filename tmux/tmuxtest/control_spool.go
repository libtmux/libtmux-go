package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// controlOutputSpool decouples tmux output from the control stream reader.
// Bytes are held in a private temporary file so unread output does not consume
// unbounded memory or block the subprocess on a fixed-capacity pipe.
type controlOutputSpool struct {
	mu    sync.Mutex
	ready chan struct{}
	file  *os.File
	path  string

	size       int64
	readOffset int64
	finished   bool
	closed     bool

	closeOnce sync.Once
	closeErr  error
}

func newControlOutputSpool() (*controlOutputSpool, error) {
	file, err := os.CreateTemp("", "libtmux-control-output-*")
	if err != nil {
		return nil, sanitizeControlSpoolError("create control output spool", err)
	}
	spool := &controlOutputSpool{
		ready: make(chan struct{}),
		file:  file,
		path:  file.Name(),
	}
	return spool, nil
}

func (s *controlOutputSpool) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, os.ErrClosed
	}
	if s.finished {
		return 0, io.ErrClosedPipe
	}
	n, err := s.file.WriteAt(data, s.size)
	s.size += int64(n)
	if n > 0 {
		s.signalLocked()
	}
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, sanitizeControlSpoolError("write control output spool", err)
}

func (s *controlOutputSpool) ReadContext(
	ctx context.Context,
	data []byte,
) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return 0, os.ErrClosed
		}
		if s.readOffset != s.size {
			available := s.size - s.readOffset
			if int64(len(data)) > available {
				data = data[:available]
			}
			n, err := s.file.ReadAt(data, s.readOffset)
			s.readOffset += int64(n)
			s.mu.Unlock()
			return n, sanitizeControlSpoolError("read control output spool", err)
		}
		if s.finished {
			s.mu.Unlock()
			return 0, io.EOF
		}
		ready := s.ready
		s.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func (s *controlOutputSpool) finish() {
	s.mu.Lock()
	s.finished = true
	s.signalLocked()
	s.mu.Unlock()
}

func (s *controlOutputSpool) signalLocked() {
	close(s.ready)
	s.ready = make(chan struct{})
}

func (s *controlOutputSpool) Close() error {
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
			sanitizeControlSpoolError("close control output spool", closeErr),
			sanitizeControlSpoolError("remove control output spool", removeErr),
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
