package mcp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func resourceUpdateNotification(uri string) (jsonrpc.Message, error) {
	params, err := json.Marshal(&sdk.ResourceUpdatedNotificationParams{URI: uri})
	if err != nil {
		return nil, err
	}
	return &jsonrpc.Request{
		Method: "notifications/resources/updated",
		Params: params,
	}, nil
}

// watchDelivery owns one client's ordered resource-update lane. Its mutable
// queue and routes are guarded by watchers.mutex.
type watchDelivery struct {
	scope      *sessionScope
	connection *sessionReadyConnection
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	done       chan struct{}
	queue      []*watchRoute
	pending    map[*watchRoute]struct{}
	routes     map[scopedSubscription]*watchRoute
	stopping   bool
}

// watchRoute is one lifetime of one exact client subscription. Canceling it
// retires queued or slot-waiting updates without affecting another route.
type watchRoute struct {
	ctx       context.Context
	cancel    context.CancelFunc
	canonical string
	spelling  string
}

func (w *watchers) route(
	scope *sessionScope,
	connection *sessionReadyConnection,
	canonical, spelling string,
) error {
	if scope == nil || connection == nil {
		return ErrInstanceClosed
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.closed {
		return ErrInstanceClosed
	}
	if w.routes == nil {
		w.routes = map[string]map[*watchDelivery]map[string]*watchRoute{}
	}
	if w.deliveries == nil {
		w.deliveries = map[*sessionScope]*watchDelivery{}
	}
	if w.deliveryWorkers == nil {
		w.deliveryWorkers = map[*watchDelivery]struct{}{}
	}
	delivery := w.deliveries[scope]
	if delivery == nil || delivery.stopping {
		ctx, cancel := context.WithCancel(scope.ctx)
		delivery = &watchDelivery{
			scope: scope, connection: connection, ctx: ctx, cancel: cancel,
			wake: make(chan struct{}, 1), done: make(chan struct{}),
			pending: map[*watchRoute]struct{}{},
			routes:  map[scopedSubscription]*watchRoute{},
		}
		w.deliveries[scope] = delivery
		w.deliveryWorkers[delivery] = struct{}{}
		go w.deliver(delivery)
	}
	key := scopedSubscription{canonical: canonical, spelling: spelling}
	if _, exists := delivery.routes[key]; exists {
		return nil
	}
	ctx, cancel := context.WithCancel(delivery.ctx)
	route := &watchRoute{
		ctx: ctx, cancel: cancel, canonical: canonical, spelling: spelling,
	}
	delivery.routes[key] = route
	if w.routes[canonical] == nil {
		w.routes[canonical] = map[*watchDelivery]map[string]*watchRoute{}
	}
	if w.routes[canonical][delivery] == nil {
		w.routes[canonical][delivery] = map[string]*watchRoute{}
	}
	w.routes[canonical][delivery][spelling] = route
	return nil
}

func (w *watchers) unroute(scope *sessionScope, canonical, spelling string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	delivery := w.deliveries[scope]
	if delivery == nil {
		return
	}
	key := scopedSubscription{canonical: canonical, spelling: spelling}
	route := delivery.routes[key]
	if route == nil {
		return
	}
	delete(delivery.routes, key)
	route.cancel()
	w.removeRouteLocked(delivery, route)
	w.removeQueuedLocked(delivery, route)
	if len(delivery.routes) == 0 {
		delivery.stopping = true
		delivery.cancel()
		if w.deliveries[scope] == delivery {
			delete(w.deliveries, scope)
		}
	}
}

func (w *watchers) removeRouteLocked(
	delivery *watchDelivery,
	route *watchRoute,
) {
	targets := w.routes[route.canonical]
	spellings := targets[delivery]
	if spellings[route.spelling] == route {
		delete(spellings, route.spelling)
	}
	if len(spellings) == 0 {
		delete(targets, delivery)
	}
	if len(targets) == 0 {
		delete(w.routes, route.canonical)
	}
}

func (w *watchers) removeQueuedLocked(delivery *watchDelivery, route *watchRoute) {
	if _, exists := delivery.pending[route]; !exists {
		return
	}
	delete(delivery.pending, route)
	kept := delivery.queue[:0]
	for _, queued := range delivery.queue {
		if queued != route {
			kept = append(kept, queued)
		}
	}
	delivery.queue = kept
}

func (w *watchers) deliver(delivery *watchDelivery) {
	defer func() {
		w.retireDelivery(delivery)
		close(delivery.done)
	}()
	for {
		if delivery.ctx.Err() != nil {
			return
		}
		route, ok := w.nextDelivery(delivery)
		if !ok {
			select {
			case <-delivery.ctx.Done():
				return
			case <-delivery.wake:
				continue
			}
		}
		if route.ctx.Err() != nil {
			continue
		}
		message, err := resourceUpdateNotification(route.spelling)
		if err == nil {
			err = delivery.connection.Write(route.ctx, message)
		}
		if err != nil {
			if route.ctx.Err() != nil {
				continue
			}
			if delivery.ctx.Err() == nil {
				delivery.connection.terminate(err)
				delivery.connection.startTransportClose()
			}
			return
		}
	}
}

func (w *watchers) nextDelivery(delivery *watchDelivery) (*watchRoute, bool) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if delivery.stopping || len(delivery.queue) == 0 {
		return nil, false
	}
	route := delivery.queue[0]
	delivery.queue = delivery.queue[1:]
	delete(delivery.pending, route)
	return route, true
}

func (w *watchers) retireDelivery(delivery *watchDelivery) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	delivery.stopping = true
	delivery.cancel()
	for key, route := range delivery.routes {
		route.cancel()
		w.removeRouteLocked(delivery, route)
		delete(delivery.routes, key)
	}
	if w.deliveries[delivery.scope] == delivery {
		delete(w.deliveries, delivery.scope)
	}
	delete(w.deliveryWorkers, delivery)
}
