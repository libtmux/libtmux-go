package mcp

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
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

// watchReadyWait bounds how long a subscription waits for the watch to be
// carrying notifications before it answers anyway.
const watchReadyWait = 10 * time.Second

// watchRetryInterval is how long to wait before trying the tmux connection
// again. A subscriber that arrived before the session it wants waits about
// this long once, rather than forever.
const watchRetryInterval = time.Second

// watchRebuildInterval is how long to wait before replacing a set of
// connections that was up and ended, which is short because every subscriber
// is deaf until it is back.
const watchRebuildInterval = 10 * time.Millisecond

// watchers turns tmux's control-mode notifications into resource updates.
type watchers struct {
	server *mcp.Server
	target tmux.Server

	mutex sync.Mutex
	// subscribed counts subscribers per canonical URI, because MCP subscribes
	// per client session and several may watch the same pane.
	subscribed map[string]int
	// spelled holds, per canonical URI, the spellings clients actually sent.
	// The SDK routes an update by the string a session subscribed with, so a
	// pane watched as %1 has to be told as %1.
	spelled map[string]map[string]int
	// owed holds the deferred notification timer for each URI.
	owed map[string]*time.Timer
	// ready is closed once a control connection is open, and replaced whenever
	// the set of them is rebuilt. A subscriber waits on the one taken before
	// its own rebuild, so it waits for a connection that will carry its pane
	// rather than one that predates the subscription.
	ready chan struct{}
	// notified is when each URI last had an update sent, which is what
	// coalescing is measured against.
	notified map[string]time.Time
	// stop ends the connection when the last subscriber goes.
	stop func()
	// rebuild reopens the set of connections. A subscription can name a pane
	// in a session nothing is attached to, and tmux has no reason to report
	// anything about it, so the arrival of the subscription is the only thing
	// that can say the set is now wrong.
	rebuild func()
	closed  bool
	wait    sync.WaitGroup
}

// newWatchers builds the watcher set for one MCP server.
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

// subscribe records a client's interest and starts watching if nothing was.
//
// An unwatchable URI is accepted rather than refused. MCP has no way to say
// "subscribed, but nothing will ever arrive", and refusing would make a client
// that subscribed to everything it listed fail on the parts that are static.
func (t *tools) subscribe(ctx context.Context, request *mcp.SubscribeRequest) error {
	uri := request.Params.URI
	ready := t.watchers.add(watchedURI(uri), uri)
	// A subscription that returns before anything is watching loses whatever
	// the pane writes next, and a pane that writes once never mentions it
	// again -- so a client that subscribes and immediately acts is told
	// nothing, however long it then waits. Waiting here is what makes the
	// reply mean what a caller reads it to mean.
	//
	// Bounded, because the connection may be impossible for now: a socket with
	// no session yet has nothing to attach to, and the watcher retries for as
	// long as anyone is subscribed. Returning after the bound keeps that case
	// working as it did rather than failing a subscription that will start
	// reporting shortly.
	select {
	case <-ready:
	case <-ctx.Done():
	case <-time.After(watchReadyWait):
	}
	return nil
}

// unsubscribe drops a client's interest and stops watching when none is left.
func (t *tools) unsubscribe(_ context.Context, request *mcp.UnsubscribeRequest) error {
	uri := request.Params.URI
	t.watchers.remove(watchedURI(uri), uri)
	return nil
}

// watchedURI rewrites a pane's content URI into the one spelling updates are
// addressed by.
//
// Reading accepts a pane as %1, as %251, or bare, so subscribing has to as
// well: a subscription is looked up by exact string, and the two spellings a
// client is most likely to send are the ones a tool result hands it. Without
// this a subscriber is registered under a key nothing ever notifies, is told
// the subscription succeeded, and cannot tell the silence from a quiet pane.
//
// Anything else is left alone, including a URI nothing watches.
func watchedURI(uri string) string {
	const prefix, suffix = "tmux://panes/", "/content"
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return uri
	}
	id := strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix)
	return paneContentURI(decodeSegment(id))
}

// add records one subscriber, under the canonical URI and the spelling it sent,
// and returns the readiness to wait on before the caller acts on the pane.
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
		// The first subscriber for this URI, so the session holding it may not
		// be one of the sessions currently attached to. The connections are
		// rebuilt, and the readiness with them, because the one that is open
		// now is not the one this subscriber needs.
		w.ready = make(chan struct{})
		w.rebuild()
	}
	return w.ready
}

// attached reports that a control connection is carrying notifications, which
// releases anyone waiting to act on a pane they have just subscribed to.
func (w *watchers) attached() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	select {
	case <-w.ready:
	default:
		close(w.ready)
	}
}

// remove drops one subscriber.
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
	// These outlive their subscription otherwise, one entry per pane ever
	// watched, for the life of the process. Nothing else drops them: the
	// coalescing window is only cleared wholesale when a rebuild restarts the
	// stream, and a deferral clears just its own key when it fires.
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

// start opens the control connection. The caller holds the mutex.
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
	for built := false; ctx.Err() == nil; {
		attended := w.follow(ctx, built)
		built = built || attended
		// A set that was up and then ended is a rebuild, and every subscriber
		// is deaf until it is back; a set that never came up is a server with
		// nothing to attach to, which is worth waiting on.
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
func (w *watchers) follow(ctx context.Context, rebuilt bool) bool {
	// Connections of its own rather than the pooled one, because these are
	// held open for as long as anything is subscribed and the pool is for
	// commands that return.
	//
	// The sessions are looked up over that same plain connection. The pool
	// belongs to a tmux server that may have died since this handle was made,
	// and a lookup through a dead one fails every retry forever, which left a
	// subscriber taken on and never told anything.
	plain := w.target.WithEngine(w.target.SubprocessEngine())
	sessions, err := w.attending(ctx, plain)
	if err != nil || len(sessions) == 0 {
		return false
	}

	// One connection per session, because tmux reports a pane's output only to
	// a client attached to that pane's session. Watching one session's panes
	// and silently missing every other session's is the same failure as not
	// watching at all, and it is the ordinary case: most people have more than
	// one session.
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
			// One connection ending rebuilds the whole set. That is how a
			// session created later gets a connection, and how one that went
			// away stops being waited on.
			defer cancel()
			w.followSession(ctx, plain, session, rebuilt)
		}()
	}
	attending.Wait()
	return true
}

// attending chooses the sessions to hold a connection to: those owning a
// watched pane, and otherwise the first one.
//
// A connection per session would cost a tmux client for every session on the
// machine to watch one pane. The fallback keeps the structural resources --
// the session and window listings -- reported when only those are subscribed,
// since tmux sends those to any attached client.
func (w *watchers) attending(ctx context.Context, server tmux.Server) ([]tmux.Session, error) {
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return nil, err
	}
	wanted := w.watchedPanes()
	if len(wanted) == 0 {
		return sessions[:1], nil
	}
	// Which session owns a watched pane cannot be guessed, and attaching to
	// the wrong one delivers nothing, so this waits for the next attempt
	// rather than settling for a connection that would stay silent.
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

// watchedPanes is the set of pane ids subscribers are waiting on.
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

// followSession holds one session's connection and dispatches what it reports,
// returning when that connection ends for any reason.
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
		// The set of sessions has changed, so the set of connections has to be
		// rebuilt: a pane watched in a session made a moment ago has nothing
		// attached to it yet.
		if notification.Kind() == tmux.ControlNotificationSessionsChanged {
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
//
// A notification the interval holds back is deferred rather than dropped. The
// last write of a burst commonly lands inside the shadow of the one before it,
// and a client re-reading on the earlier notification reads the pane before
// that write arrived: dropping it leaves the client a few hundred milliseconds
// stale with nothing coming to correct it, which for a pane that then goes
// quiet is permanent.
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
	// One timer per URI, because a burst suppresses many and they all describe
	// the same change: the deferred notification says the pane moved, not how
	// often.
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
	// One update per spelling in use, because the SDK routes by the string a
	// session subscribed with and delivers to nobody else. Coalescing is
	// measured against the canonical URI, so the spellings of one pane share
	// an interval rather than each getting their own.
	//
	// A failure here is one client's transport rather than this watcher's
	// business.
	for _, spelling := range spellings {
		_ = w.server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{
			URI: spelling,
		})
	}
}

// tellEveryone reports every subscription once, for the gap where nothing was
// listening.
//
// A connection ends whenever the set of sessions changes, and the set is
// rebuilt from scratch; between the two, tmux reports a pane's output to
// nobody and there is no record of it to catch up from. A write in that window
// was never mentioned again, so a subscriber watching a pane sat silent while
// the pane filled. A re-read the client did not need costs one call.
//
// The coalescing window is cleared first: it exists to thin a live stream, and
// this is the stream restarting, so a notification a moment before the gap
// must not swallow the one that says what happened during it.
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

// paneContentURI is the URI a pane's contents are addressed by, built the same
// way the resource templates read it: without the sigil, which is an escape
// character in a URI.
func paneContentURI(paneID string) string {
	return "tmux://panes/" + strings.TrimPrefix(paneID, "%") + "/content"
}

// paneOfContentURI reads a pane id back out of its canonical content URI.
func paneOfContentURI(uri string) (string, bool) {
	const prefix, suffix = "tmux://panes/", "/content"
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return "", false
	}
	return "%" + strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix), true
}
