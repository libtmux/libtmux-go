package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/libtmux/libtmux-go/tmux"
)

type watchNotificationStream interface {
	Next(context.Context) (tmux.ControlNotification, error)
	CloseContext(context.Context) error
}

type watchObserverSet struct {
	ctx       context.Context
	cancel    context.CancelFunc
	plan      watchPlan
	mutex     sync.Mutex
	observers []*watchObserver
	stopping  bool
	stopOnce  sync.Once
}

type watchObserver struct {
	stream         watchNotificationStream
	initialSession tmux.SessionID
}

func newWatchObserverSet(ctx context.Context, plan watchPlan) *watchObserverSet {
	observerCtx, cancel := context.WithCancel(ctx)
	return &watchObserverSet{ctx: observerCtx, cancel: cancel, plan: plan}
}

func (set *watchObserverSet) start(w *watchers, generation *watchGeneration) {
	for _, observer := range set.snapshot() {
		go w.readWatchNotifications(generation, set, observer)
	}
}

func (set *watchObserverSet) add(observer watchObserver) {
	set.mutex.Lock()
	set.observers = append(set.observers, &observer)
	stopping := set.stopping
	set.mutex.Unlock()
	if stopping {
		signalWatchStream(observer.stream)
	}
}

func (set *watchObserverSet) snapshot() []*watchObserver {
	set.mutex.Lock()
	defer set.mutex.Unlock()
	return append([]*watchObserver(nil), set.observers...)
}

func (set *watchObserverSet) stop(ctx context.Context) error {
	if set == nil {
		return nil
	}
	set.signalStop()
	var closeErr error
	for _, observer := range set.snapshot() {
		closeErr = errors.Join(closeErr, observer.stream.CloseContext(ctx))
	}
	return closeErr
}

func (set *watchObserverSet) signalStop() {
	if set == nil {
		return
	}
	set.stopOnce.Do(func() {
		set.cancel()
		set.mutex.Lock()
		set.stopping = true
		observers := append([]*watchObserver(nil), set.observers...)
		set.mutex.Unlock()
		for _, observer := range observers {
			signalWatchStream(observer.stream)
		}
	})
}

func signalWatchStream(stream watchNotificationStream) {
	signal, cancel := context.WithCancel(context.Background())
	cancel()
	_ = stream.CloseContext(signal)
}
