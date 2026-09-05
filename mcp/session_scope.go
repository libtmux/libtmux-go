package mcp

import (
	"context"
	"sync"
)

// sessionScope owns state that one MCP client must never share with another.
type sessionScope struct {
	mutex       sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	closed      bool
	terminalErr error
	consent     map[string]bool
}

func newSessionScope(parent context.Context) *sessionScope {
	ctx, cancel := context.WithCancel(parent)
	return &sessionScope{
		ctx:     ctx,
		cancel:  cancel,
		consent: map[string]bool{},
	}
}

func (s *sessionScope) close() {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	clear(s.consent)
	s.mutex.Unlock()
}

func (s *sessionScope) stop() {
	s.terminate(nil)
}

func (s *sessionScope) terminate(err error) {
	if s == nil {
		return
	}
	s.mutex.Lock()
	if s.terminalErr == nil && err != nil {
		s.terminalErr = err
	}
	s.cancel()
	s.mutex.Unlock()
}

func (s *sessionScope) terminalCause() error {
	if s == nil {
		return nil
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.terminalErr
}
