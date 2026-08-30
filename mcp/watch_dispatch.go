package mcp

import (
	"errors"
	"time"
)

var errWatchDeliveryOverflow = errors.New("resource update delivery queue overflow")

func (w *watchers) enqueueLocked(delivery *watchDelivery, route *watchRoute) bool {
	if delivery.stopping || route.ctx.Err() != nil {
		return false
	}
	if _, pending := delivery.pending[route]; pending {
		return false
	}
	limit := w.deliveryLimit
	if limit <= 0 {
		limit = watchSubscriptionSpellingLimit
	}
	if len(delivery.queue) >= limit {
		delivery.stopping = true
		delivery.cancel()
		return true
	}
	delivery.pending[route] = struct{}{}
	delivery.queue = append(delivery.queue, route)
	select {
	case delivery.wake <- struct{}{}:
	default:
	}
	return false
}

// notify defers the final update inside a coalescing interval rather than
// dropping it.
func (w *watchers) notify(uri string) {
	w.mutex.Lock()
	if w.closed {
		w.mutex.Unlock()
		return
	}
	failed := map[*watchDelivery]struct{}{}
	watched := w.subscribed[uri] > 0
	since := time.Since(w.notified[uri])
	recent := since < watchNotifyInterval
	if watched && !recent {
		w.notified[uri] = time.Now()
		for delivery, routes := range w.routes[uri] {
			for _, route := range routes {
				if w.enqueueLocked(delivery, route) {
					failed[delivery] = struct{}{}
				}
			}
		}
	}
	if watched && recent && w.owed[uri] == nil {
		var timer *time.Timer
		timer = time.AfterFunc(watchNotifyInterval-since, func() {
			w.mutex.Lock()
			if w.owed[uri] != timer {
				w.mutex.Unlock()
				return
			}
			delete(w.owed, uri)
			w.mutex.Unlock()
			w.notify(uri)
		})
		w.owed[uri] = timer
	}
	w.mutex.Unlock()
	for delivery := range failed {
		delivery.connection.terminate(errWatchDeliveryOverflow)
		delivery.connection.startTransportClose()
	}
}

// tellEveryone invalidates subscriptions after a rebuild and resets live-stream
// coalescing so writes during the gap are not hidden.
func (w *watchers) tellEveryone() {
	w.mutex.Lock()
	for uri, timer := range w.owed {
		timer.Stop()
		delete(w.owed, uri)
	}
	clear(w.notified)
	watched := make([]string, 0, len(w.subscribed))
	for uri := range w.subscribed {
		watched = append(watched, uri)
	}
	w.mutex.Unlock()
	for _, uri := range watched {
		w.notify(uri)
	}
}
