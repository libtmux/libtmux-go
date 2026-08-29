package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStructuralNotificationInvalidatesEverySubscribedResource(t *testing.T) {
	resources := []string{
		resourceSessions,
		"tmux://sessions/work/windows",
		"tmux://windows/1/panes",
		"tmux://panes/1/content",
	}
	watchers := &watchers{subscribed: map[string]int{}}
	for _, uri := range resources {
		watchers.subscribed[uri] = 1
	}
	notification, err := tmux.ParseControlNotification([]byte("%window-add @1"))
	if err != nil {
		t.Fatal(err)
	}
	invalidated := map[string]struct{}{}
	for _, uri := range watchers.affected(notification) {
		invalidated[uri] = struct{}{}
	}
	for _, uri := range resources {
		if _, ok := invalidated[uri]; !ok {
			t.Errorf("structural update did not invalidate %q", uri)
		}
	}
}

func TestSessionSetChangeInvalidatesBeforeRebuild(t *testing.T) {
	const uri = "tmux://sessions/work"
	watchers := &watchers{
		subscribed: map[string]int{uri: 1},
		notified:   map[string]time.Time{},
		owed:       map[string]*time.Timer{},
	}
	notification, err := tmux.ParseControlNotification([]byte("%sessions-changed"))
	if err != nil {
		t.Fatal(err)
	}
	invalidated := false
	rebuild := watchers.handleNotification(notification, func() {
		invalidated = true
		watchers.tellEveryone()
	})
	if !rebuild {
		t.Fatal("session-set change did not request a watcher rebuild")
	}
	if !invalidated || watchers.at(uri).IsZero() {
		t.Fatal("session-set change rebuilt before invalidating current resources")
	}
}

func TestACoalescedNotificationIsDeferredNotDropped(t *testing.T) {
	const uri = "tmux://panes/%9/content"
	target := mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "coalescing-unused"})
	runtimeCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	watchers := newWatchers(newRuntime(runtimeCtx, target, func(error) { cancel() }))
	t.Cleanup(watchers.close)
	// Subscribed without a watcher: this is about what notify decides, and
	// starting one would open a connection to a server that is not there.
	watchers.subscribed[uri] = 1
	watchers.spelled[uri] = map[string]int{uri: 1}

	watchers.notify(uri)
	first := watchers.at(uri)
	if first.IsZero() {
		t.Fatal("the first notification was not sent")
	}

	// Inside the window, so it is held back.
	watchers.notify(uri)
	if !watchers.at(uri).Equal(first) {
		t.Fatal("a notification inside the interval was sent rather than held")
	}
	if !watchers.owes(uri) {
		t.Fatal("a notification inside the interval was dropped, not deferred")
	}

	deadline := time.Now().Add(5 * time.Second)
	for watchers.at(uri).Equal(first) {
		if time.Now().After(deadline) {
			t.Fatal("the deferred notification never went out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if watchers.owes(uri) {
		t.Error("the deferral was not cleared once it fired")
	}
}

func TestUnsubscribingDropsThePanesRecord(t *testing.T) {
	const uri = "tmux://panes/%7/content"
	target := mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "pruning-unused"})
	runtimeCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	watchers := newWatchers(newRuntime(runtimeCtx, target, func(error) { cancel() }))
	t.Cleanup(watchers.close)
	watchers.subscribed[uri] = 1
	watchers.spelled[uri] = map[string]int{uri: 1}

	watchers.notify(uri) // records when it went out
	watchers.notify(uri) // inside the window, so one is owed
	if watchers.at(uri).IsZero() || !watchers.owes(uri) {
		t.Fatal("the notification did not leave a record to drop")
	}

	watchers.remove(uri, uri)
	if !watchers.at(uri).IsZero() {
		t.Error("the coalescing window outlived the subscription")
	}
	if watchers.owes(uri) {
		t.Error("the deferral outlived the subscription")
	}
}

func TestResubscribeWaitsForTheSuccessorGeneration(t *testing.T) {
	const uri = "tmux://panes/7/content"
	target := mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "generation-unused"})
	runtimeCtx, cancelRuntime := context.WithCancel(t.Context())
	t.Cleanup(cancelRuntime)
	watchers := newWatchers(newRuntime(runtimeCtx, target, func(error) { cancelRuntime() }))
	t.Cleanup(watchers.close)
	oldCtx, cancelOld := context.WithCancel(runtimeCtx)
	old := &watchGeneration{
		ctx:      oldCtx,
		cancel:   cancelOld,
		done:     make(chan struct{}),
		ready:    make(chan struct{}),
		stopping: true,
	}
	watchers.active = old
	cancelOld()

	ready, err := watchers.add(uri, uri)
	if err != nil {
		t.Fatal(err)
	}
	if watchers.active != old || watchers.nextReady != ready {
		t.Fatal("a subscription started a successor before the old generation joined")
	}
	watchers.retire(old)
	watchers.mutex.Lock()
	successor := watchers.active
	if successor != nil {
		successorRebuild := &watchRebuild{cancel: func() {}}
		successor.rebuild = successorRebuild
		watchers.mutex.Unlock()
		watchers.clearRebuild(old, &watchRebuild{cancel: func() {}})
		watchers.mutex.Lock()
		if successor.rebuild != successorRebuild {
			watchers.mutex.Unlock()
			t.Fatal("stale rebuild cleanup changed the successor generation")
		}
	}
	watchers.mutex.Unlock()
	if successor == nil || successor == old || successor.ready != ready {
		t.Fatal("retiring generation did not transfer readiness to one successor")
	}
	if watchers.attached(old) {
		t.Fatal("a stale generation attached after its successor started")
	}
	select {
	case <-ready:
		t.Fatal("a stale generation closed its successor's readiness")
	default:
	}
	select {
	case <-old.done:
	default:
		t.Fatal("retired generation did not close its done signal")
	}
}

func TestRuntimeCancellationDoesNotRespawnAWatchGeneration(t *testing.T) {
	runtimeCtx, cancelRuntime := context.WithCancel(t.Context())
	watchers := newWatchers(&tmuxRuntime{ctx: runtimeCtx})
	t.Cleanup(watchers.close)
	oldCtx, cancelOld := context.WithCancel(runtimeCtx)
	old := &watchGeneration{
		ctx: oldCtx, cancel: cancelOld, done: make(chan struct{}), ready: make(chan struct{}),
	}
	const uri = "tmux://sessions/work"
	waiting := make(chan struct{})
	watchers.subscribed[uri] = 1
	watchers.active = old
	watchers.nextReady = waiting
	cancelRuntime()

	watchers.retire(old)
	watchers.mutex.Lock()
	successor := watchers.active
	watchers.mutex.Unlock()
	if successor != nil {
		t.Fatal("runtime cancellation respawned a watch generation")
	}
	select {
	case <-waiting:
	default:
		t.Fatal("runtime cancellation left successor readiness open")
	}
}

func TestSuccessorRestoresAPreviouslyReadyGeneration(t *testing.T) {
	const uri = "tmux://sessions/work"
	target := mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: "restore-unused"})
	runtimeCtx, cancelRuntime := context.WithCancel(t.Context())
	t.Cleanup(cancelRuntime)
	watchers := newWatchers(newRuntime(runtimeCtx, target, func(error) { cancelRuntime() }))
	t.Cleanup(watchers.close)
	oldCtx, cancelOld := context.WithCancel(runtimeCtx)
	ready := make(chan struct{})
	close(ready)
	old := &watchGeneration{
		ctx: oldCtx, cancel: cancelOld, done: make(chan struct{}), ready: ready, stopping: true,
	}
	watchers.subscribed[uri] = 1
	watchers.spelled[uri] = map[string]int{uri: 1}
	watchers.active = old
	cancelOld()

	watchers.retire(old)
	watchers.mutex.Lock()
	successor := watchers.active
	watchers.mutex.Unlock()
	if successor == nil || !successor.restoring {
		t.Fatal("successor forgot that it must invalidate changes from the replacement gap")
	}
}

func TestANewResourceRetiresTheSelectedGeneration(t *testing.T) {
	const existing = "tmux://panes/7/content"
	const added = "tmux://panes/8/content"
	ctx, cancel := context.WithCancel(t.Context())
	generation := &watchGeneration{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		ready:  make(chan struct{}),
	}
	watchers := &watchers{
		subscribed: map[string]int{existing: 1},
		spelled: map[string]map[string]int{
			existing: {existing: 1},
		},
		admitted: map[string]struct{}{existing: {}},
		active:   generation,
	}

	ready, err := watchers.add(added, added)
	if err != nil {
		t.Fatal(err)
	}
	if !generation.stopping {
		t.Fatal("the generation selected before the new resource remained active")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("the generation selected before the new resource was not canceled")
	}
	if ready == generation.ready || ready != watchers.nextReady {
		t.Fatal("the new resource inherited readiness from the stale selection")
	}
}

func TestGenerationReadinessWaitsForEverySelectedSession(t *testing.T) {
	generation := &watchGeneration{
		done:      make(chan struct{}),
		ready:     make(chan struct{}),
		attaching: 2,
	}
	watchers := &watchers{active: generation}

	if watchers.attached(generation) {
		t.Fatal("the first selected session made the generation ready")
	}
	select {
	case <-generation.ready:
		t.Fatal("readiness closed before every selected session attached")
	default:
	}
	if !watchers.attached(generation) {
		t.Fatal("the last selected session did not make the generation ready")
	}
	select {
	case <-generation.ready:
	default:
		t.Fatal("readiness remained open after every selected session attached")
	}
}

func TestRemovingCanonicalResourceRetiresTheSelectedGeneration(t *testing.T) {
	const removed = "tmux://panes/7/content"
	const kept = "tmux://panes/8/content"
	ctx, cancel := context.WithCancel(t.Context())
	generation := &watchGeneration{
		ctx: ctx, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{}),
	}
	watchers := &watchers{
		subscribed: map[string]int{removed: 1, kept: 1},
		spelled: map[string]map[string]int{
			removed: {removed: 1},
			kept:    {kept: 1},
		},
		active:   generation,
		notified: map[string]time.Time{},
		owed:     map[string]*time.Timer{},
	}

	watchers.remove(removed, removed)
	if !generation.stopping {
		t.Fatal("generation retained an obsolete resource selection")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("generation with an obsolete resource selection was not canceled")
	}
}

//libtmux:real-tmux
func TestWatcherSelectsSessionsNeededByItsResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	target := tmuxtest.NewServer(ctx, t)
	tmuxtest.NewSession(ctx, t, target, tmux.NewSessionRequest{Name: "zz-second"})
	sessions, err := target.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	unselected := sessions[1]
	windows, err := target.Windows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var secondWindow tmux.Window
	for _, window := range windows {
		if window.SessionID() == unselected.ID() {
			secondWindow = window
			break
		}
	}
	if secondWindow.ID() == "" {
		t.Fatal("second session has no window")
	}
	if _, err := secondWindow.SplitPane(ctx, tmux.SplitPaneRequest{Command: "sleep 30"}); err != nil {
		t.Fatal(err)
	}
	panes, err := target.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPane := ""
	for _, pane := range panes {
		if pane.SessionID() == unselected.ID() {
			resolvedPane = pane.ID().String()
			break
		}
	}
	if resolvedPane == "" {
		t.Fatal("second session has no pane")
	}

	instance := mustInternalMCPServer(t, target)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	updated := make(chan string, 4)
	client := sdk.NewClient(&sdk.Implementation{Name: "watch-every-session"}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(
			_ context.Context,
			request *sdk.ResourceUpdatedNotificationRequest,
		) {
			updated <- request.Params.URI
		},
	})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	metadata := "tmux://windows/" + strings.TrimPrefix(secondWindow.ID().String(), "@")
	if err := clientSession.Subscribe(ctx, &sdk.SubscribeParams{URI: metadata}); err != nil {
		t.Fatal(err)
	}
	drainResourceUpdates(updated)
	assertWatcherAttends(ctx, t, instance.tools.watchers, target, unselected.ID())
	if err := secondWindow.SelectLayout(ctx, tmux.SelectLayoutRequest{
		Layout: "even-horizontal",
	}); err != nil {
		t.Fatal(err)
	}
	awaitResourceWrite(t, updated, metadata)
	if err := clientSession.Unsubscribe(ctx, &sdk.UnsubscribeParams{URI: metadata}); err != nil {
		t.Fatal(err)
	}

	unresolved := "tmux://panes/999999999/content"
	if err := clientSession.Subscribe(ctx, &sdk.SubscribeParams{URI: unresolved}); err != nil {
		t.Fatal(err)
	}
	// Replacing the metadata selection restores a previously live generation.
	// Consume that invalidation so only the layout change can satisfy the next
	// assertion.
	awaitResourceWrite(t, updated, unresolved)
	drainResourceUpdates(updated)
	assertWatcherAttends(ctx, t, instance.tools.watchers, target, unselected.ID())
	if err := secondWindow.SelectLayout(ctx, tmux.SelectLayoutRequest{
		Layout: "even-vertical",
	}); err != nil {
		t.Fatal(err)
	}
	awaitResourceWrite(t, updated, unresolved)
	if err := clientSession.Unsubscribe(ctx, &sdk.UnsubscribeParams{URI: unresolved}); err != nil {
		t.Fatal(err)
	}

	resolved := paneContentURI(resolvedPane)
	if err := clientSession.Subscribe(ctx, &sdk.SubscribeParams{URI: resolved}); err != nil {
		t.Fatal(err)
	}
	selected, err := instance.tools.watchers.attending(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].ID() != unselected.ID() {
		t.Fatalf("resolved pane selected sessions %v, want only %s", selected, unselected.ID())
	}
}

func assertWatcherAttends(
	ctx context.Context,
	t *testing.T,
	watchers *watchers,
	server tmux.Server,
	want tmux.SessionID,
) {
	t.Helper()
	selected, err := watchers.attending(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range selected {
		if session.ID() == want {
			return
		}
	}
	t.Fatalf("watcher selected sessions %v, missing %s", selected, want)
}

func drainResourceUpdates(updates <-chan string) {
	quiet := time.NewTimer(100 * time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case <-updates:
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(100 * time.Millisecond)
		case <-quiet.C:
			return
		}
	}
}

// at is when the last notification about one URI went out.
func (w *watchers) at(uri string) time.Time {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.notified[uri]
}

// owes reports a notification held back and not yet sent.
func (w *watchers) owes(uri string) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.owed[uri] != nil
}
