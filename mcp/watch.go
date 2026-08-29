package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Watchers translate tmux control notifications into coalesced MCP resource
// updates. Connections exist only while subscriptions do.

// watchNotifyInterval bounds updates per resource.
const watchNotifyInterval = 250 * time.Millisecond

// watchReadyWait bounds how long subscribe waits for watcher readiness.
const watchReadyWait = 10 * time.Second

// watchRetryInterval paces initial connection retries.
const watchRetryInterval = time.Second

// watchRebuildInterval paces retries after selecting a session set.
const watchRebuildInterval = 10 * time.Millisecond

// watchSubscriptionSpellingLimit bounds the SDK's retained subscription keys.
// go-sdk v1.6.1 removes disconnected sessions from each key but does not prune
// the empty keys themselves, so disconnects consume this Instance-wide budget;
// a successful explicit unsubscribe reclaims its key.
const watchSubscriptionSpellingLimit = 4096

// watchShutdownWait is one global budget for the watcher generation and every
// per-session delivery worker.
const watchShutdownWait = time.Second

type watchers struct {
	runtime *tmuxRuntime

	mutex sync.Mutex
	// subscribed counts subscribers per canonical URI.
	subscribed map[string]int
	// spelled counts exact client spellings for admission and cleanup.
	spelled map[string]map[string]int
	// admitted mirrors distinct SDK subscription keys for this Instance. A
	// successful explicit unsubscribe prunes it; disconnect cleanup cannot,
	// because the SDK retains that empty key.
	admitted map[string]struct{}
	owed     map[string]*time.Timer
	notified map[string]time.Time
	active   *watchGeneration
	// nextReady belongs to subscriptions accepted while active is stopping.
	nextReady       chan struct{}
	routes          map[string]map[*watchDelivery]map[string]*watchRoute
	deliveries      map[*sessionScope]*watchDelivery
	deliveryWorkers map[*watchDelivery]struct{}
	deliveryLimit   int
	shutdownWait    time.Duration
	shutdownBy      time.Time
	closed          bool
}

type watchGeneration struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	ready     chan struct{}
	rebuild   *watchRebuild
	attaching int
	restoring bool
	stopping  bool
}

type watchRebuild struct {
	cancel context.CancelFunc
}

func newWatchers(runtime *tmuxRuntime) *watchers {
	return &watchers{
		runtime:         runtime,
		subscribed:      map[string]int{},
		spelled:         map[string]map[string]int{},
		admitted:        map[string]struct{}{},
		notified:        map[string]time.Time{},
		owed:            map[string]*time.Timer{},
		routes:          map[string]map[*watchDelivery]map[string]*watchRoute{},
		deliveries:      map[*sessionScope]*watchDelivery{},
		deliveryWorkers: map[*watchDelivery]struct{}{},
		deliveryLimit:   watchSubscriptionSpellingLimit,
		shutdownWait:    watchShutdownWait,
	}
}

// subscribe accepts every served static URI shape because MCP has no separate
// result for a subscription that will never emit.
func (t *tools) subscribe(ctx context.Context, request *mcp.SubscribeRequest) error {
	if request == nil || request.Params == nil || request.Session == nil {
		return ErrInstanceClosed
	}
	uri := request.Params.URI
	if !subscribableResourceURI(uri) {
		return fmt.Errorf("%q is not a tmux resource this server serves", uri)
	}
	required := CapabilityMetadataRead
	if _, content := paneOfContentURI(watchedURI(uri)); content {
		required = CapabilityContentRead
	}
	if !t.capabilities.permits(required) {
		return fmt.Errorf("subscribing to %s requires the %s capability", uri, required)
	}
	_, acquired, err := t.acquireRequestRuntime(ctx)
	if err != nil {
		return err
	}
	acquired.release()
	scope, connection, err := t.watchSession(request.Session)
	if err != nil {
		return err
	}
	ready, err := scope.subscribe(t.watchers, connection, watchedURI(uri), uri)
	if err != nil {
		return err
	}
	// Wait briefly for a watcher connection. An unavailable watcher is accepted
	// so it can attach after a session appears.
	select {
	case <-ready:
	case <-ctx.Done():
	case <-time.After(watchReadyWait):
	}
	return nil
}

func (t *tools) watchSession(
	session *mcp.ServerSession,
) (*sessionScope, *sessionReadyConnection, error) {
	t.instance.mutex.Lock()
	defer t.instance.mutex.Unlock()
	if t.instance.closing {
		return nil, nil, ErrInstanceClosed
	}
	tracked := t.instance.sessions[session]
	if tracked == nil || tracked.connection == nil {
		return nil, nil, ErrInstanceClosed
	}
	return tracked.scope, tracked.connection, nil
}

func subscribableResourceURI(uri string) bool {
	if uri == resourceSessions {
		return true
	}
	return resourceURISegment(uri, "tmux://sessions/", "/windows") ||
		resourceURISegment(uri, "tmux://windows/", "/panes") ||
		resourceURISegment(uri, "tmux://panes/", "/content") ||
		resourceURISegment(uri, "tmux://sessions/", "") ||
		resourceURISegment(uri, "tmux://windows/", "") ||
		resourceURISegment(uri, "tmux://panes/", "")
}

func resourceURISegment(uri, prefix, suffix string) bool {
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return false
	}
	segment := strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix)
	return segment != "" && !strings.ContainsAny(segment, "/?#")
}

func (t *tools) unsubscribe(_ context.Context, request *mcp.UnsubscribeRequest) error {
	if request == nil || request.Params == nil || request.Session == nil {
		return ErrInstanceClosed
	}
	uri := request.Params.URI
	scope, _, err := t.watchSession(request.Session)
	if err != nil {
		return err
	}
	scope.unsubscribe(t.watchers, watchedURI(uri), uri)
	return nil
}

// watchedURI canonicalizes pane spellings while retaining the exact client URI
// for delivery.
func watchedURI(uri string) string {
	const prefix, suffix = "tmux://panes/", "/content"
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return uri
	}
	id := strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix)
	return paneContentURI(decodeSegment(id))
}

// add records canonical and exact spellings and returns connection readiness.
func (w *watchers) add(canonical, spelling string) (<-chan struct{}, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.closed {
		return nil, ErrInstanceClosed
	}
	if w.admitted == nil {
		w.admitted = map[string]struct{}{}
	}
	if _, seen := w.admitted[spelling]; !seen {
		if len(w.admitted) >= watchSubscriptionSpellingLimit {
			return nil, fmt.Errorf(
				"resource subscription limit reached: at most %d distinct URI spellings per server instance",
				watchSubscriptionSpellingLimit,
			)
		}
		w.admitted[spelling] = struct{}{}
	}
	w.subscribed[canonical]++
	if w.spelled[canonical] == nil {
		w.spelled[canonical] = map[string]int{}
	}
	w.spelled[canonical][spelling]++
	switch {
	case w.active == nil:
		w.startLocked(nil, false)
	case w.active.stopping:
		if w.nextReady == nil {
			w.nextReady = make(chan struct{})
		}
		return w.nextReady, nil
	case w.subscribed[canonical] == 1:
		w.active.stopping = true
		w.active.cancel()
		if w.nextReady == nil {
			w.nextReady = make(chan struct{})
		}
		return w.nextReady, nil
	}
	return w.active.ready, nil
}

func (w *watchers) attached(generation *watchGeneration) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.active != generation || generation.stopping {
		return false
	}
	if generation.attaching > 1 {
		generation.attaching--
		return false
	}
	generation.attaching = 0
	select {
	case <-generation.ready:
	default:
		close(generation.ready)
	}
	return true
}

func (w *watchers) remove(canonical, spelling string) {
	w.removeSubscription(canonical, spelling, false)
}

// removeExplicit reclaims a spelling because the SDK removes its outer map key
// after this handler succeeds. Disconnect cleanup uses remove instead: the SDK
// leaves that empty key allocated.
func (w *watchers) removeExplicit(canonical, spelling string) {
	w.removeSubscription(canonical, spelling, true)
}

func (w *watchers) removeSubscription(
	canonical, spelling string,
	reclaim bool,
) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if spellings := w.spelled[canonical]; spellings != nil {
		if spellings[spelling] > 1 {
			spellings[spelling]--
		} else {
			delete(spellings, spelling)
			if reclaim {
				delete(w.admitted, spelling)
			}
		}
		if len(spellings) == 0 {
			delete(w.spelled, canonical)
		}
	}
	if w.subscribed[canonical] > 1 {
		w.subscribed[canonical]--
		return
	}
	delete(w.subscribed, canonical)
	// Drop coalescing state so watched URIs do not accumulate after unsubscribe.
	if timer := w.owed[canonical]; timer != nil {
		timer.Stop()
		delete(w.owed, canonical)
	}
	delete(w.notified, canonical)
	if w.active != nil && !w.active.stopping {
		w.active.stopping = true
		w.active.cancel()
	}
	if len(w.subscribed) == 0 {
		if w.nextReady != nil {
			close(w.nextReady)
			w.nextReady = nil
		}
	}
}

// startLocked requires w.mutex.
func (w *watchers) startLocked(ready chan struct{}, restoring bool) {
	if ready == nil {
		ready = make(chan struct{})
	}
	ctx, cancel := context.WithCancel(w.runtime.ctx)
	generation := &watchGeneration{
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		ready:     ready,
		restoring: restoring,
	}
	w.active = generation
	go func() {
		w.watch(generation)
		w.retire(generation)
	}()
}

func (w *watchers) close() {
	w.mutex.Lock()
	if !w.closed {
		w.closed = true
		w.shutdownDeadlineLocked()
		if w.active != nil && !w.active.stopping {
			w.active.stopping = true
			w.active.cancel()
		}
		if w.nextReady != nil {
			close(w.nextReady)
			w.nextReady = nil
		}
		for uri, timer := range w.owed {
			timer.Stop()
			delete(w.owed, uri)
		}
		for delivery := range w.deliveryWorkers {
			delivery.stopping = true
			delivery.cancel()
		}
		clear(w.subscribed)
		clear(w.spelled)
		clear(w.admitted)
		clear(w.notified)
		clear(w.routes)
	}
	done := make([]<-chan struct{}, 0, len(w.deliveryWorkers)+1)
	if w.active != nil {
		done = append(done, w.active.done)
	}
	for delivery := range w.deliveryWorkers {
		done = append(done, delivery.done)
	}
	shutdownBy := w.shutdownDeadlineLocked()
	w.mutex.Unlock()
	w.waitForShutdown(done, shutdownBy)
}

func (w *watchers) waitForShutdown(
	done []<-chan struct{},
	deadline time.Time,
) {
	for _, joined := range done {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		timer := time.NewTimer(remaining)
		select {
		case <-joined:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			return
		}
	}
}

// shutdownDeadlineLocked returns the one deadline shared by the generation and
// per-session delivery workers. It requires w.mutex.
func (w *watchers) shutdownDeadlineLocked() time.Time {
	if w.shutdownBy.IsZero() {
		within := w.shutdownWait
		if within <= 0 {
			within = watchShutdownWait
		}
		w.shutdownBy = time.Now().Add(within)
	}
	return w.shutdownBy
}

// paneContentURI omits the percent sigil, which begins a URI escape.
func paneContentURI(paneID string) string {
	return "tmux://panes/" + strings.TrimPrefix(paneID, "%") + "/content"
}

func paneOfContentURI(uri string) (string, bool) {
	const prefix, suffix = "tmux://panes/", "/content"
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return "", false
	}
	return "%" + strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix), true
}
