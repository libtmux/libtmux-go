package mcp

import (
	"context"
	"strings"
	"sync"
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

//libtmux:real-tmux
func TestWatcherHandsPaneOutputToItsNewSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	target := tmuxtest.NewServer(ctx, t)
	unselected := tmuxtest.NewSession(
		ctx,
		t,
		target,
		tmux.NewSessionRequest{Name: "zz-second"},
	)
	sessions, err := target.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	windows, err := target.Windows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var firstWindow, secondWindow tmux.Window
	for _, window := range windows {
		if window.SessionID() == unselected.ID() {
			secondWindow = window
		} else {
			firstWindow = window
		}
	}
	if firstWindow.ID() == "" || secondWindow.ID() == "" {
		t.Fatal("a session has no destination window")
	}
	sleepPane, err := secondWindow.SplitPane(ctx, tmux.SplitPaneRequest{Command: "sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	panes, err := target.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resolvedPane tmux.Pane
	for _, pane := range panes {
		if pane.SessionID() == unselected.ID() && pane.ID() != sleepPane.ID() {
			resolvedPane = pane
			break
		}
	}
	if resolvedPane.ID() == "" {
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

	realOpen := instance.tools.watchers.open
	blocked := make(chan struct{})
	release := make(chan struct{})
	var openMutex sync.Mutex
	var blockedOnce, releaseOnce sync.Once
	opens := 0
	instance.tools.watchers.open = func(
		openCtx context.Context,
		plan watchPlan,
		candidate *watchObserverSet,
	) error {
		openMutex.Lock()
		opens++
		call := opens
		openMutex.Unlock()
		if call > 1 {
			blockedOnce.Do(func() { close(blocked) })
			select {
			case <-release:
			case <-openCtx.Done():
				return context.Cause(openCtx)
			}
		}
		return realOpen(openCtx, plan, candidate)
	}
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	keeper, err := target.OpenControl(ctx, unselected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
		defer cancelCleanup()
		_ = keeper.CloseContext(cleanupCtx)
	})
	version, err := target.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	minimumDestructionProof, err := tmux.ParseVersion("3.6")
	if err != nil {
		t.Fatal(err)
	}
	proveSessionDestruction := version.AtLeast(minimumDestructionProof)
	if proveSessionDestruction {
		if err := unselected.SetDestroyUnattached(ctx, tmux.DestroyUnattachedOn); err != nil {
			t.Fatal(err)
		}
	}
	resolved := paneContentURI(resolvedPane.ID().String())
	if err := clientSession.Subscribe(ctx, &sdk.SubscribeParams{URI: resolved}); err != nil {
		t.Fatal(err)
	}
	drainResourceUpdates(updated)
	moved, err := resolvedPane.Move(ctx, tmux.MovePaneRequest{TargetWindow: firstWindow})
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, blocked, "post-move candidate attachment")
	awaitResourceWrite(t, updated, resolved)
	gapCommand := "echo HANDOFF-GAP"
	if err := moved.SendKeys(ctx, tmux.SendKeysRequest{Command: &gapCommand}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(watchNotifyInterval + 50*time.Millisecond)
	drainResourceUpdates(updated)
	if err := keeper.CloseContext(ctx); err != nil {
		t.Fatal(err)
	}
	assertSessionGone := func() {
		t.Helper()
		for {
			remaining, listErr := target.SearchSessions(ctx, nil)
			present := false
			for _, session := range remaining {
				present = present || session.ID() == unselected.ID()
			}
			if listErr == nil && !present {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("source session remained after publication: %v", listErr)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	remaining, err := target.SearchSessions(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	sourcePresent := false
	for _, session := range remaining {
		sourcePresent = sourcePresent || session.ID() == unselected.ID()
	}
	if !sourcePresent {
		t.Fatal("source session was destroyed while candidate attachment was blocked")
	}
	releaseOnce.Do(func() { close(release) })
	if proveSessionDestruction {
		assertSessionGone()
	}
	awaitResourceWrite(t, updated, resolved)
	lines, err := moved.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "HANDOFF-GAP") {
		t.Fatalf("gap marker capture = (%q, %v)", lines, err)
	}
	time.Sleep(watchNotifyInterval + 50*time.Millisecond)
	drainResourceUpdates(updated)
	outputCommand := "echo HANDOFF-OUTPUT"
	if err := moved.SendKeys(ctx, tmux.SendKeysRequest{Command: &outputCommand}); err != nil {
		t.Fatal(err)
	}
	awaitResourceWrite(t, updated, resolved)
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
