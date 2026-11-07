package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// paneContentCoalesce is the throttle window: the first output after a
	// quiet spell arms one notification this far ahead, and output arriving
	// inside the window rides on it. A pane is therefore announced at most once
	// per window, and never with a change left unannounced.
	paneContentCoalesce = 100 * time.Millisecond
	// maxPaneWatches bounds the control clients subscriptions can hold open.
	maxPaneWatches = 16
)

// paneWatches owns one control-mode pane observation per subscribed pane,
// shared by every MCP session subscribed to it.
type paneWatches struct {
	mutex sync.Mutex
	panes map[tmux.PaneID]*paneWatch
	// coalesce and limit are paneContentCoalesce and maxPaneWatches; a test
	// shortens the first and lowers the second.
	coalesce time.Duration
	limit    int
}

type paneWatch struct {
	pane   tmux.PaneID
	uri    string
	cancel context.CancelFunc
	// done closes once the watch has released its control client.
	done chan struct{}
	// sessions maps each subscribed MCP session to the stop of its
	// disconnect hook.
	sessions map[*mcp.ServerSession]func() bool
}

func paneContentURI(pane tmux.PaneID) string {
	return "tmux://panes/" + escapeID(pane.String()) + "/content"
}

// subscribeResource is ServerOptions.SubscribeHandler. The SDK calls it for
// resources/subscribe and for every URI of a subscriptions/listen alike. A
// refusal is invalid params, which names the request as the thing to change.
func (t *tools) subscribeResource(ctx context.Context, request *mcp.SubscribeRequest) error {
	uri := request.Params.URI
	route, ok := t.surface.subscribableRoute()
	if !ok {
		return mcp.ResourceNotFoundError(uri)
	}
	id, ok := route.matches(uri)
	if !ok {
		return invalidParams(uri, fmt.Errorf("%s cannot be subscribed to; only %s can", uri, route.template))
	}
	// A notification names the URI the client subscribed with, so one spelling
	// of it is accepted.
	if canonical := paneContentURI(tmux.PaneID(id)); uri != canonical {
		return invalidParams(uri, fmt.Errorf("subscribe to %s, the percent-encoded form of %s", canonical, uri))
	}
	process, err := t.runtime.process(ctx)
	if err != nil {
		return explainVersionRefusal(err)
	}
	pane, err := process.Pane(ctx, tmux.PaneID(id))
	if err != nil {
		return resourceReadError(uri, notFound(err, "pane", id, "list_panes"))
	}
	// Below the control-mode floor there is nothing to push from. Polling
	// would make a notification an inference from a timer, so it is refused.
	if unavailable, err := controlObservationUnavailable(ctx, pane); err != nil {
		return err
	} else if unavailable {
		return invalidParams(uri, fmt.Errorf("subscribing to %s needs tmux %s or newer (control mode); "+
			"read it, or use wait_for_text, which polls", uri, tmux.MinimumConnectionVersion))
	}
	scope, err := t.instance.scope(request.Session)
	if err != nil {
		return err
	}

	w := &t.watches
	// The lock spans opening the observation so two sessions subscribing to
	// one pane share a single control client.
	w.mutex.Lock()
	defer w.mutex.Unlock()
	watch := w.panes[pane.ID()]
	if watch == nil {
		if len(w.panes) >= w.limit {
			return invalidParams(uri, fmt.Errorf("at most %d pane contents can be subscribed to at once", w.limit))
		}
		observation, err := t.runtime.openObservation(ctx, pane)
		if err != nil {
			return explainVersionRefusal(err)
		}
		watchCtx, cancel := context.WithCancel(t.instance.ctx)
		watch = &paneWatch{
			pane: pane.ID(), uri: uri, cancel: cancel, done: make(chan struct{}),
			sessions: map[*mcp.ServerSession]func() bool{},
		}
		if w.panes == nil {
			w.panes = map[tmux.PaneID]*paneWatch{}
		}
		w.panes[pane.ID()] = watch
		go t.followPane(watchCtx, watch, observation)
	}
	if stop := watch.sessions[request.Session]; stop != nil {
		stop()
	}
	// The SDK never calls UnsubscribeHandler for a client that goes away; the
	// session scope ends on disconnect, so tie the reference to it.
	watch.sessions[request.Session] = context.AfterFunc(scope.ctx, func() {
		t.releaseWatch(pane.ID(), request.Session)
	})
	return nil
}

// unsubscribeResource is ServerOptions.UnsubscribeHandler.
func (t *tools) unsubscribeResource(_ context.Context, request *mcp.UnsubscribeRequest) error {
	route, ok := t.surface.subscribableRoute()
	if !ok {
		return nil
	}
	id, ok := route.matches(request.Params.URI)
	if !ok {
		return nil
	}
	t.releaseWatch(tmux.PaneID(id), request.Session)
	return nil
}

func (t *tools) releaseWatch(pane tmux.PaneID, session *mcp.ServerSession) {
	w := &t.watches
	w.mutex.Lock()
	defer w.mutex.Unlock()
	watch := w.panes[pane]
	if watch == nil {
		return
	}
	if stop := watch.sessions[session]; stop != nil {
		stop()
	}
	delete(watch.sessions, session)
	if len(watch.sessions) == 0 {
		delete(w.panes, pane)
		watch.cancel()
	}
}

// followPane turns a pane's output into coalesced resources/updated
// notifications until it is cancelled, the pane dies, or tmux goes away. In
// the last two cases it announces once more, so a subscriber re-reads and
// learns the pane is gone instead of waiting on a silent URI.
func (t *tools) followPane(ctx context.Context, watch *paneWatch, observation *tmux.PaneObservation) {
	defer func() {
		t.runtime.releaseObservation(observation)
		close(watch.done)
	}()
	var (
		mutex sync.Mutex
		armed bool
		timer *time.Timer
	)
	announce := func() {
		mutex.Lock()
		armed = false
		mutex.Unlock()
		if ctx.Err() != nil {
			return
		}
		_ = t.instance.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: watch.uri})
	}
	defer func() {
		mutex.Lock()
		if timer != nil {
			timer.Stop()
		}
		mutex.Unlock()
	}()
	for {
		notification, err := observation.NextNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if _, unreadable := errors.AsType[*tmux.ControlNotificationError](err); unreadable {
				continue
			}
			// Pane death, topology loss, or the server exiting.
			t.endWatch(watch)
			_ = t.instance.server.ResourceUpdated(context.Background(),
				&mcp.ResourceUpdatedNotificationParams{URI: watch.uri})
			return
		}
		if pane, _, ok := notification.Output(); !ok || pane != watch.pane {
			continue
		}
		mutex.Lock()
		if !armed {
			armed = true
			timer = time.AfterFunc(t.watches.coalesce, announce)
		}
		mutex.Unlock()
	}
}

// endWatch forgets a watch whose pane is gone, and the hooks that would have
// released it.
func (t *tools) endWatch(watch *paneWatch) {
	w := &t.watches
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.panes[watch.pane] == watch {
		delete(w.panes, watch.pane)
	}
	for _, stop := range watch.sessions {
		stop()
	}
	clear(watch.sessions)
}
