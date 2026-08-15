package mcp

import (
	"context"
	"strings"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tmux "github.com/tmux-python/libtmux/golang"
)

// A client can be told a pane changed rather than asking whether it did.
//
// capture_since makes re-reading cheap; a subscription makes it unnecessary. A
// client that subscribes to tmux://panes/%1/content is notified when the pane
// writes, and reads it once, when there is something to read. Nothing is
// polled at either end.
//
// tmux already publishes this. A control-mode connection reports every byte a
// pane writes and every structural change to the server, so a subscription is
// a translation rather than a mechanism: tmux's notifications become MCP's,
// and the work is deciding which of tmux's affect which resource.
//
// One connection serves every subscription. It opens with the first and closes
// with the last, so a server nobody is watching holds no tmux client, and a
// server with twenty subscriptions holds one rather than twenty.
//
// Notifications are coalesced. A pane writing a line at a time would otherwise
// produce a notification per line, and a client reading the resource each time
// would be doing more work than polling. What a subscriber needs to know is
// that the pane is not what it last read, which one notification says as well
// as a hundred.

// watchNotifyInterval is the shortest time between two notifications about the
// same resource. Long enough that a pane printing a build log does not become
// a notification storm, short enough to feel immediate.
const watchNotifyInterval = 250 * time.Millisecond

// watchRetryInterval is how long to wait before trying the tmux connection
// again. A subscriber that arrived before the session it wants waits about
// this long once, rather than forever.
const watchRetryInterval = time.Second

// watchers turns tmux's control-mode notifications into resource updates.
type watchers struct {
	server *mcp.Server
	target tmux.Server

	mutex sync.Mutex
	// subscribed counts subscribers per URI, because MCP subscribes per client
	// session and several may watch the same pane.
	subscribed map[string]int
	// notified is when each URI last had an update sent, which is what
	// coalescing is measured against.
	notified map[string]time.Time
	// stop ends the connection when the last subscriber goes.
	stop func()
}

// newWatchers builds the watcher set for one MCP server.
func newWatchers(server *mcp.Server, target tmux.Server) *watchers {
	return &watchers{
		server:     server,
		target:     target,
		subscribed: map[string]int{},
		notified:   map[string]time.Time{},
	}
}

// subscribe records a client's interest and starts watching if nothing was.
//
// An unwatchable URI is accepted rather than refused. MCP has no way to say
// "subscribed, but nothing will ever arrive", and refusing would make a client
// that subscribed to everything it listed fail on the parts that are static.
func (t *tools) subscribe(_ context.Context, request *mcp.SubscribeRequest) error {
	t.watchers.add(request.Params.URI)
	return nil
}

// unsubscribe drops a client's interest and stops watching when none is left.
func (t *tools) unsubscribe(_ context.Context, request *mcp.UnsubscribeRequest) error {
	t.watchers.remove(request.Params.URI)
	return nil
}

// add records one subscriber.
func (w *watchers) add(uri string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.subscribed[uri]++
	if w.stop == nil {
		w.start()
	}
}

// remove drops one subscriber.
func (w *watchers) remove(uri string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.subscribed[uri] > 1 {
		w.subscribed[uri]--
		return
	}
	delete(w.subscribed, uri)
	if len(w.subscribed) == 0 && w.stop != nil {
		w.stop()
		w.stop = nil
	}
}

// start opens the control connection. The caller holds the mutex.
func (w *watchers) start() {
	ctx, cancel := context.WithCancel(context.Background())
	w.stop = cancel
	go w.watch(ctx)
}

// watch keeps a tmux connection for as long as anything is subscribed.
//
// It retries rather than giving up, because the two ways of failing are both
// ordinary and both temporary. A control connection has to attach to a
// session, and a client is commonly pointed at a socket before anyone has made
// one — giving up there meant a subscriber that arrived first was never told
// anything, however long it waited. A connection that drops, because tmux
// restarted or the session it attached to was killed, left every subscriber
// silently deaf with their subscriptions still registered.
//
// Only cancellation ends this, which happens when the last subscriber goes.
func (w *watchers) watch(ctx context.Context) {
	defer w.forget()
	for ctx.Err() == nil {
		w.follow(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(watchRetryInterval):
		}
	}
}

// forget clears the handle so a later subscription starts a new watcher.
func (w *watchers) forget() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if len(w.subscribed) == 0 {
		w.stop = nil
	}
}

// follow holds one tmux connection and dispatches what it reports, returning
// when that connection ends for any reason.
func (w *watchers) follow(ctx context.Context) {
	sessions, err := w.target.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return
	}
	// A connection of its own rather than the pooled one, because this holds
	// it open for as long as anything is subscribed and the pool is for
	// commands that return.
	control, err := w.target.WithEngine(w.target.SubprocessEngine()).
		OpenControl(ctx, sessions[0])
	if err != nil {
		return
	}
	defer func() { _ = control.Close() }()

	for {
		notification, err := control.NextNotification(ctx)
		if err != nil {
			return
		}
		for _, uri := range w.affected(notification) {
			w.notify(ctx, uri)
		}
	}
}

// affected reports which resource URIs one tmux notification changes.
//
// The mapping is tmux's containment read backwards: a pane wrote, so that
// pane's content changed; a window was added, so the session listing changed
// and so did that window's panes. Anything not listed here changes nothing a
// client can subscribe to.
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
		// A pane entering copy mode shows something else, which is a change to
		// what reading it returns.
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
		// The shape of the server changed, which is what the sessions resource
		// describes. Which window changed is in the notification, but the
		// windows resource is addressed by session name rather than window id,
		// so telling a subscriber the whole listing moved is both correct and
		// all this can say without a lookup per notification.
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
		// These describe this connection, tmux's own configuration, or the
		// paste buffers, none of which any subscribable resource reports. They
		// are named rather than left to a default so that a notification tmux
		// adds later shows up here as a decision to make rather than as
		// silence.
		return nil
	default:
		return nil
	}
}

// notify sends one resource update, no more often than the interval allows.
func (w *watchers) notify(ctx context.Context, uri string) {
	w.mutex.Lock()
	watched := w.subscribed[uri] > 0
	recent := time.Since(w.notified[uri]) < watchNotifyInterval
	if watched && !recent {
		w.notified[uri] = time.Now()
	}
	w.mutex.Unlock()
	if !watched || recent {
		return
	}
	// The SDK delivers to the sessions that subscribed to this URI and to
	// nobody else, so a failure here is one client's transport rather than
	// this watcher's business.
	_ = w.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: uri})
}

// paneContentURI is the URI a pane's contents are addressed by, built the same
// way the resource templates read it: without the sigil, which is an escape
// character in a URI.
func paneContentURI(paneID string) string {
	return "tmux://panes/" + strings.TrimPrefix(paneID, "%") + "/content"
}
