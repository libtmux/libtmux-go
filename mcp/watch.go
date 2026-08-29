package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
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

type watchers struct {
	server *mcp.Server
	target tmux.Server

	mutex sync.Mutex
	// subscribed counts subscribers per canonical URI.
	subscribed map[string]int
	// spelled retains exact client spellings because SDK routing uses them.
	spelled map[string]map[string]int
	owed    map[string]*time.Timer
	// ready closes after the first watcher connection opens; a new pane
	// subscription may replace it while rebuilding.
	ready    chan struct{}
	notified map[string]time.Time
	stop     func()
	// rebuild reopens connections when a newly watched pane needs another session.
	rebuild func()
	closed  bool
	wait    sync.WaitGroup
}

func newWatchers(server *mcp.Server, target tmux.Server) *watchers {
	return &watchers{
		server:     server,
		target:     target,
		subscribed: map[string]int{},
		spelled:    map[string]map[string]int{},
		notified:   map[string]time.Time{},
		owed:       map[string]*time.Timer{},
		ready:      make(chan struct{}),
	}
}

// subscribe accepts static URIs because MCP has no separate result for a
// subscription that will never emit.
func (t *tools) subscribe(ctx context.Context, request *mcp.SubscribeRequest) error {
	uri := request.Params.URI
	required := CapabilityMetadataRead
	if _, content := paneOfContentURI(watchedURI(uri)); content {
		required = CapabilityContentRead
	}
	if !t.capabilities.permits(required) {
		return fmt.Errorf("subscribing to %s requires the %s capability", uri, required)
	}
	ready := t.watchers.add(watchedURI(uri), uri)
	// Wait briefly for a watcher connection. An unavailable watcher is accepted
	// so it can attach after a session appears.
	select {
	case <-ready:
	case <-ctx.Done():
	case <-time.After(watchReadyWait):
	}
	return nil
}

func (t *tools) unsubscribe(_ context.Context, request *mcp.UnsubscribeRequest) error {
	uri := request.Params.URI
	t.watchers.remove(watchedURI(uri), uri)
	return nil
}

// watchedURI canonicalizes pane spellings while the original is retained for
// SDK routing.
func watchedURI(uri string) string {
	const prefix, suffix = "tmux://panes/", "/content"
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return uri
	}
	id := strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix)
	return paneContentURI(decodeSegment(id))
}

// add records canonical and exact spellings and returns connection readiness.
func (w *watchers) add(canonical, spelling string) <-chan struct{} {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.closed {
		ready := make(chan struct{})
		close(ready)
		return ready
	}
	w.subscribed[canonical]++
	if w.spelled[canonical] == nil {
		w.spelled[canonical] = map[string]int{}
	}
	w.spelled[canonical][spelling]++
	switch {
	case w.stop == nil:
		w.start()
	case w.subscribed[canonical] == 1 && w.rebuild != nil:
		// A new pane may require attaching to another session.
		w.ready = make(chan struct{})
		w.rebuild()
	}
	return w.ready
}

func (w *watchers) attached() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	select {
	case <-w.ready:
	default:
		close(w.ready)
	}
}

func (w *watchers) remove(canonical, spelling string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if spellings := w.spelled[canonical]; spellings != nil {
		if spellings[spelling] > 1 {
			spellings[spelling]--
		} else {
			delete(spellings, spelling)
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
	if len(w.subscribed) == 0 && w.stop != nil {
		w.stop()
		w.stop = nil
	}
}

// start requires w.mutex.
func (w *watchers) start() {
	ctx, cancel := context.WithCancel(context.Background())
	w.stop = cancel
	w.wait.Add(1)
	go func() {
		defer w.wait.Done()
		w.watch(ctx)
	}()
}

func (w *watchers) close() {
	w.mutex.Lock()
	if !w.closed {
		w.closed = true
		if w.stop != nil {
			w.stop()
		}
		for uri, timer := range w.owed {
			timer.Stop()
			delete(w.owed, uri)
		}
		clear(w.subscribed)
		clear(w.spelled)
		clear(w.notified)
	}
	w.mutex.Unlock()
	w.wait.Wait()
}

// watch retries initial and dropped connections until the last subscription
// cancels it.
func (w *watchers) watch(ctx context.Context) {
	defer w.forget()
	for built := false; ctx.Err() == nil; {
		attended := w.follow(ctx, built)
		built = built || attended
		// Restore a previously live set sooner than an initial empty server.
		wait := watchRetryInterval
		if attended {
			wait = watchRebuildInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (w *watchers) forget() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if len(w.subscribed) == 0 {
		w.stop = nil
	}
}

func (w *watchers) follow(ctx context.Context, rebuilt bool) bool {
	// Watches need private long-lived connections; a stale pool must not make
	// every retry fail against a replacement tmux server.
	plain := w.target.WithEngine(w.target.SubprocessEngine())
	sessions, err := w.attending(ctx, plain)
	if err != nil || len(sessions) == 0 {
		return false
	}

	// tmux reports pane output only to clients attached to the owning session.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w.mutex.Lock()
	w.rebuild = cancel
	w.mutex.Unlock()
	defer func() {
		w.mutex.Lock()
		w.rebuild = nil
		w.mutex.Unlock()
	}()

	var attending sync.WaitGroup
	for _, session := range sessions {
		attending.Add(1)
		go func() {
			defer attending.Done()
			// Any connection ending rebuilds the session set.
			defer cancel()
			w.followSession(ctx, plain, session, rebuilt)
		}()
	}
	attending.Wait()
	return true
}

// attending selects owning sessions. With no resolved owner, it keeps one
// session attached for structural changes.
func (w *watchers) attending(ctx context.Context, server tmux.Server) ([]tmux.Session, error) {
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return nil, err
	}
	wanted := w.watchedPanes()
	if len(wanted) == 0 {
		return sessions[:1], nil
	}
	// Do not guess a watched pane's owning session.
	panes, err := server.Panes(ctx)
	if err != nil {
		return nil, err
	}
	owning := map[tmux.SessionID]struct{}{}
	for _, pane := range panes {
		if _, ok := wanted[pane.ID().String()]; ok {
			owning[pane.SessionID()] = struct{}{}
		}
	}
	if len(owning) == 0 {
		return sessions[:1], nil
	}
	chosen := make([]tmux.Session, 0, len(owning))
	for _, session := range sessions {
		if _, ok := owning[session.ID()]; ok {
			chosen = append(chosen, session)
		}
	}
	return chosen, nil
}

func (w *watchers) watchedPanes() map[string]struct{} {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	panes := map[string]struct{}{}
	for uri := range w.subscribed {
		if id, ok := paneOfContentURI(uri); ok {
			panes[id] = struct{}{}
		}
	}
	return panes
}

func (w *watchers) followSession(
	ctx context.Context,
	server tmux.Server,
	session tmux.Session,
	rebuilt bool,
) {
	control, err := server.OpenControl(ctx, session)
	if err != nil {
		return
	}
	defer func() { _ = control.Close() }()
	w.attached()
	if rebuilt {
		w.tellEveryone(ctx)
	}

	for {
		notification, err := control.NextNotification(ctx)
		if err != nil {
			return
		}
		// Rebuild so newly created sessions can gain a watcher.
		if notification.Kind() == tmux.ControlNotificationSessionsChanged {
			return
		}
		for _, uri := range w.affected(notification) {
			w.notify(ctx, uri)
		}
	}
}

func (w *watchers) affected(notification tmux.ControlNotification) []string {
	switch notification.Kind() {
	case tmux.ControlNotificationOutput, tmux.ControlNotificationExtendedOutput:
		pane, _, ok := notification.Output()
		if !ok {
			return nil
		}
		return []string{paneContentURI(pane.String())}
	case tmux.ControlNotificationPaneModeChanged:
		arguments := notification.Arguments()
		if len(arguments) == 0 {
			return nil
		}
		// Copy mode changes pane content reads without pane output.
		return []string{paneContentURI(arguments[0])}
	case tmux.ControlNotificationLayoutChange,
		tmux.ControlNotificationWindowAdd,
		tmux.ControlNotificationWindowClose,
		tmux.ControlNotificationWindowRenamed,
		tmux.ControlNotificationWindowPaneChanged,
		tmux.ControlNotificationUnlinkedWindowAdd,
		tmux.ControlNotificationUnlinkedWindowClose,
		tmux.ControlNotificationUnlinkedWindowRenamed,
		tmux.ControlNotificationSessionsChanged,
		tmux.ControlNotificationSessionRenamed,
		tmux.ControlNotificationSessionChanged,
		tmux.ControlNotificationSessionWindowChanged:
		// Session-scoped resources require a lookup, so invalidate the root.
		return []string{resourceSessions}
	case tmux.ControlNotificationClientDetached,
		tmux.ControlNotificationClientSessionChanged,
		tmux.ControlNotificationConfigError,
		tmux.ControlNotificationContinue,
		tmux.ControlNotificationExit,
		tmux.ControlNotificationMessage,
		tmux.ControlNotificationPasteBufferChanged,
		tmux.ControlNotificationPasteBufferDeleted,
		tmux.ControlNotificationPause,
		tmux.ControlNotificationSubscriptionChanged:
		// These notifications do not change a subscribable resource.
		return nil
	default:
		return nil
	}
}

// notify defers the final update inside a coalescing interval rather than
// dropping it.
func (w *watchers) notify(ctx context.Context, uri string) {
	w.mutex.Lock()
	if w.closed {
		w.mutex.Unlock()
		return
	}
	watched := w.subscribed[uri] > 0
	since := time.Since(w.notified[uri])
	recent := since < watchNotifyInterval
	var spellings []string
	if watched && !recent {
		w.notified[uri] = time.Now()
		for spelling := range w.spelled[uri] {
			spellings = append(spellings, spelling)
		}
	}
	// One timer per canonical URI coalesces a burst.
	if watched && recent && w.owed[uri] == nil {
		var timer *time.Timer
		timer = time.AfterFunc(watchNotifyInterval-since, func() {
			w.mutex.Lock()
			if w.owed[uri] != timer {
				w.mutex.Unlock()
				return
			}
			delete(w.owed, uri)
			closed := w.closed
			w.mutex.Unlock()
			if !closed {
				w.notify(context.WithoutCancel(ctx), uri)
			}
		})
		w.owed[uri] = timer
	}
	w.mutex.Unlock()
	if !watched || recent {
		return
	}
	// Notify each exact spelling because SDK routing uses it. Transport failure
	// from one client does not stop the watcher.
	for _, spelling := range spellings {
		_ = w.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{
			URI: spelling,
		})
	}
}

// tellEveryone invalidates subscriptions after a rebuild and resets live-stream
// coalescing so writes during the gap are not hidden.
func (w *watchers) tellEveryone(ctx context.Context) {
	w.mutex.Lock()
	w.notified = map[string]time.Time{}
	watched := make([]string, 0, len(w.subscribed))
	for uri := range w.subscribed {
		watched = append(watched, uri)
	}
	w.mutex.Unlock()
	for _, uri := range watched {
		w.notify(ctx, uri)
	}
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
