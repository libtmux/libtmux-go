package mcp

import (
	"context"
	"sync"
)

// sessionScope owns state that one MCP client must never share with another.
type sessionScope struct {
	mutex         sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	closed        bool
	consent       map[string]bool
	subscriptions map[scopedSubscription]struct{}
	jobs          *jobs
}

type scopedSubscription struct {
	canonical string
	spelling  string
}

func newSessionScope(parent context.Context) *sessionScope {
	ctx, cancel := context.WithCancel(parent)
	return &sessionScope{
		ctx:           ctx,
		cancel:        cancel,
		consent:       map[string]bool{},
		subscriptions: map[scopedSubscription]struct{}{},
		jobs:          newJobs(),
	}
}

func (s *sessionScope) subscribe(
	watchers *watchers,
	canonical, spelling string,
) (<-chan struct{}, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		return nil, ErrInstanceClosed
	}
	key := scopedSubscription{canonical: canonical, spelling: spelling}
	if _, exists := s.subscriptions[key]; exists {
		ready := make(chan struct{})
		close(ready)
		return ready, nil
	}
	s.subscriptions[key] = struct{}{}
	return watchers.add(canonical, spelling), nil
}

func (s *sessionScope) unsubscribe(
	watchers *watchers,
	canonical, spelling string,
) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := scopedSubscription{canonical: canonical, spelling: spelling}
	if _, exists := s.subscriptions[key]; !exists {
		return
	}
	delete(s.subscriptions, key)
	watchers.remove(canonical, spelling)
}

func (s *sessionScope) close(watchers *watchers) {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	clear(s.consent)
	for subscription := range s.subscriptions {
		watchers.remove(subscription.canonical, subscription.spelling)
		delete(s.subscriptions, subscription)
	}
	s.mutex.Unlock()
	s.jobs.close()
}

func (s *sessionScope) stop() {
	if s != nil {
		s.cancel()
	}
}
