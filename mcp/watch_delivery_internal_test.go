package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type resourceWriteConnection struct {
	writes      chan string
	started     chan struct{}
	release     chan struct{}
	returned    chan struct{}
	calls       atomic.Int64
	startedOnce sync.Once
	releaseOnce sync.Once
	returnOnce  sync.Once
}

type resourceWireEvent struct {
	uri  string
	call bool
}

type resourceRecordingConnection struct {
	writes chan jsonrpc.Message
}

func (*resourceRecordingConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *resourceRecordingConnection) Write(
	_ context.Context,
	message jsonrpc.Message,
) error {
	c.writes <- message
	return nil
}

func (*resourceRecordingConnection) Close() error      { return nil }
func (*resourceRecordingConnection) SessionID() string { return "" }

type resourceGateTransport struct {
	inner    sdk.Transport
	observed chan resourceWireEvent
	started  chan struct{}
	release  chan struct{}
	calls    *atomic.Int64
	armed    *atomic.Bool
}

func (t resourceGateTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &resourceGateConnection{Connection: connection, gate: t}, nil
}

type resourceGateConnection struct {
	sdk.Connection
	gate resourceGateTransport
}

func (c *resourceGateConnection) Write(
	ctx context.Context,
	message jsonrpc.Message,
) error {
	request, ok := message.(*jsonrpc.Request)
	if ok && request.Method == "notifications/resources/updated" {
		var params sdk.ResourceUpdatedNotificationParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return err
		}
		c.gate.observed <- resourceWireEvent{uri: params.URI, call: request.IsCall()}
		if c.gate.release != nil && c.gate.armed.Load() && c.gate.calls.Add(1) == 1 {
			close(c.gate.started)
			<-c.gate.release
		}
	}
	return c.Connection.Write(ctx, message)
}

func (*resourceWriteConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *resourceWriteConnection) Write(_ context.Context, message jsonrpc.Message) error {
	request, ok := message.(*jsonrpc.Request)
	if !ok {
		return errors.New("resource update is not a request")
	}
	var params sdk.ResourceUpdatedNotificationParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return err
	}
	c.writes <- params.URI
	if c.release != nil && c.calls.Add(1) == 1 {
		c.startedOnce.Do(func() { close(c.started) })
		<-c.release
		if c.returned != nil {
			c.returnOnce.Do(func() { close(c.returned) })
		}
	}
	return nil
}

func TestBlockedResourceDeliveryRetiresItsWorkerWithinTransportBudget(t *testing.T) {
	const uri = "tmux://sessions/blocked"
	watchers := admissionTestWatchers()
	inner := &resourceWriteConnection{
		writes:   make(chan string, 1),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		returned: make(chan struct{}),
	}
	scope := newSessionScope(t.Context())
	connection := testReadyConnection(inner, nil, nil, func(err error) {
		scope.terminate(err)
	})
	connection.notificationWriteWait = 20 * time.Millisecond
	connection.transportCloseWait = 20 * time.Millisecond
	t.Cleanup(func() {
		inner.unblock()
		scope.close(watchers)
	})
	if _, err := scope.subscribe(watchers, connection, uri, uri); err != nil {
		t.Fatal(err)
	}
	watchers.mutex.Lock()
	delivery := watchers.deliveries[scope]
	watchers.mutex.Unlock()
	if delivery == nil {
		t.Fatal("subscription has no delivery worker")
	}

	watchers.notify(uri)
	awaitResourceWrite(t, inner.writes, uri)
	select {
	case <-delivery.done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("blocked transport pinned the resource delivery worker")
	}
	if !errors.Is(scope.terminalCause(), context.DeadlineExceeded) {
		t.Fatalf("session terminal error = %v, want delivery deadline",
			scope.terminalCause())
	}
	select {
	case <-inner.returned:
		t.Fatal("physical write returned before the test released it")
	default:
	}
}

func TestWatcherCloseJoinsDeliveryWithoutWaitingForPhysicalWrite(t *testing.T) {
	const uri = "tmux://sessions/closing"
	watchers := admissionTestWatchers()
	watchers.shutdownWait = 200 * time.Millisecond
	inner := &resourceWriteConnection{
		writes:   make(chan string, 1),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		returned: make(chan struct{}),
	}
	scope := newSessionScope(t.Context())
	connection := testReadyConnection(inner, nil, nil, nil)
	connection.notificationWriteWait = time.Second
	t.Cleanup(func() {
		inner.unblock()
		scope.close(watchers)
	})
	if _, err := scope.subscribe(watchers, connection, uri, uri); err != nil {
		t.Fatal(err)
	}
	watchers.mutex.Lock()
	delivery := watchers.deliveries[scope]
	watchers.active = nil
	watchers.mutex.Unlock()

	watchers.notify(uri)
	awaitResourceWrite(t, inner.writes, uri)
	started := time.Now()
	watchers.close()
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("watcher close took %v behind a blocked physical write", elapsed)
	}
	select {
	case <-delivery.done:
	default:
		t.Fatal("watcher close returned before its delivery worker joined")
	}
	select {
	case <-inner.returned:
		t.Fatal("physical write returned before the test released it")
	default:
	}
}

func (*resourceWriteConnection) Close() error      { return nil }
func (*resourceWriteConnection) SessionID() string { return "" }

func (c *resourceWriteConnection) unblock() {
	if c.release != nil {
		c.releaseOnce.Do(func() { close(c.release) })
	}
}

func TestResourceUpdateWireNotificationHasNoID(t *testing.T) {
	const uri = "tmux://panes/7/content"
	message, err := resourceUpdateNotification(uri)
	if err != nil {
		t.Fatal(err)
	}
	request, ok := message.(*jsonrpc.Request)
	if !ok {
		t.Fatalf("resource update message = %T, want *jsonrpc.Request", message)
	}
	if request.IsCall() {
		t.Fatal("resource update carries a JSON-RPC id")
	}
	if request.Method != "notifications/resources/updated" {
		t.Fatalf("resource update method = %q", request.Method)
	}
	var params sdk.ResourceUpdatedNotificationParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.URI != uri {
		t.Fatalf("resource update URI = %q, want %q", params.URI, uri)
	}
	wire, err := jsonrpc.EncodeMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(wire, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["id"]; exists {
		t.Fatalf("resource update wire has an id: %s", wire)
	}
}

func TestUnsubscribeCancelsAPoppedResourceUpdate(t *testing.T) {
	const removed = "tmux://sessions/removed"
	const kept = "tmux://sessions/kept"
	watchers := admissionTestWatchers()
	inner := &resourceRecordingConnection{writes: make(chan jsonrpc.Message, 4)}
	scope := newSessionScope(t.Context())
	connection := testReadyConnection(inner, nil, nil, nil)
	t.Cleanup(func() { scope.close(watchers) })
	for _, uri := range []string{removed, kept} {
		if _, err := scope.subscribe(watchers, connection, uri, uri); err != nil {
			t.Fatal(err)
		}
	}

	// Hold the physical write slot until the removed route's update has left
	// the queue but has not been admitted to the transport.
	connection.writeSlot <- struct{}{}
	var releaseOnce sync.Once
	releaseSlot := func() { releaseOnce.Do(connection.releaseWriteSlot) }
	t.Cleanup(releaseSlot)
	watchers.notify(removed)
	watchers.mutex.Lock()
	delivery := watchers.deliveries[scope]
	watchers.mutex.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		watchers.mutex.Lock()
		popped := delivery != nil && len(delivery.queue) == 0
		watchers.mutex.Unlock()
		if popped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resource update remained queued")
		}
		time.Sleep(time.Millisecond)
	}

	scope.unsubscribe(watchers, removed, removed)
	responseDone := make(chan error, 1)
	go func() { responseDone <- connection.Write(t.Context(), &jsonrpc.Response{}) }()
	releaseSlot()
	select {
	case err := <-responseDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unsubscribe response did not commit")
	}
	select {
	case message := <-inner.writes:
		if _, ok := message.(*jsonrpc.Response); !ok {
			t.Fatalf("removed route committed %T before the unsubscribe response", message)
		}
	default:
		t.Fatal("unsubscribe response did not reach the transport")
	}
	select {
	case message := <-inner.writes:
		t.Fatalf("removed route committed %T after the unsubscribe response", message)
	case <-time.After(40 * time.Millisecond):
	}

	if _, err := scope.subscribe(watchers, connection, removed, removed); err != nil {
		t.Fatal(err)
	}
	watchers.notify(removed)
	select {
	case message := <-inner.writes:
		request, ok := message.(*jsonrpc.Request)
		if !ok {
			t.Fatalf("resubscribed route committed %T, want notification", message)
		}
		var params sdk.ResourceUpdatedNotificationParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Fatal(err)
		}
		if params.URI != removed {
			t.Fatalf("resubscribed route update = %q, want %q", params.URI, removed)
		}
	case <-time.After(time.Second):
		t.Fatal("resubscribed route did not receive a fresh update")
	}
	select {
	case message := <-inner.writes:
		t.Fatalf("old route revived after resubscribe: %T", message)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestResourceDeliveryIsOrderedAndIsolatedBySDKSession(t *testing.T) {
	const first = "tmux://sessions/integration"
	const second = "tmux://sessions/integration/windows"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	target := mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	instance := mustInternalMCPServer(t, target)
	observer := newFakeWatchStream()
	instance.tools.watchers.plan = func(context.Context, watchSelection) (watchPlan, error) {
		return handoffTestPlan("$1"), nil
	}
	instance.tools.watchers.open = func(
		_ context.Context,
		_ watchPlan,
		candidate *watchObserverSet,
	) error {
		candidate.add(watchObserver{stream: observer})
		return nil
	}

	blockedGate := resourceGateTransport{
		observed: make(chan resourceWireEvent, 4),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		calls:    &atomic.Int64{},
		armed:    &atomic.Bool{},
	}
	fastGate := resourceGateTransport{observed: make(chan resourceWireEvent, 4)}
	blockedUpdates := make(chan string, 4)
	fastUpdates := make(chan string, 4)
	connect := func(
		name string,
		gate *resourceGateTransport,
		updates chan<- string,
	) *sdk.ClientSession {
		t.Helper()
		clientTransport, serverTransport := sdk.NewInMemoryTransports()
		gate.inner = serverTransport
		serverSession, err := instance.Connect(
			ctx, AssumeResponseCommit(*gate), nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = serverSession.Close() })
		client := sdk.NewClient(&sdk.Implementation{Name: name}, &sdk.ClientOptions{
			ResourceUpdatedHandler: func(
				_ context.Context,
				request *sdk.ResourceUpdatedNotificationRequest,
			) {
				updates <- request.Params.URI
			},
		})
		clientSession, err := client.Connect(ctx, clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clientSession.Close() })
		return clientSession
	}
	blocked := connect("blocked-watch-client", &blockedGate, blockedUpdates)
	fast := connect("fast-watch-client", &fastGate, fastUpdates)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(blockedGate.release) }) }
	t.Cleanup(unblock)

	subscribe := func(session *sdk.ClientSession, uri string) {
		t.Helper()
		if err := session.Subscribe(ctx, &sdk.SubscribeParams{URI: uri}); err != nil {
			t.Fatal(err)
		}
	}
	for _, uri := range []string{first, second} {
		subscribe(blocked, uri)
		subscribe(fast, uri)
	}
	for len(blockedGate.observed) > 0 {
		<-blockedGate.observed
	}
	drainResourceUpdates(blockedUpdates)
	blockedGate.armed.Store(true)

	instance.tools.watchers.notify(first)
	awaitWireResource(t, blockedGate.observed, first)
	select {
	case <-blockedGate.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	instance.tools.watchers.notify(second)
	awaitWireResource(t, fastGate.observed, first)
	awaitWireResource(t, fastGate.observed, second)
	awaitResourceWrite(t, fastUpdates, first)
	awaitResourceWrite(t, fastUpdates, second)
	select {
	case update := <-blockedGate.observed:
		t.Fatalf("blocked session sent %q before its first update settled", update.uri)
	case <-time.After(40 * time.Millisecond):
	}

	unblock()
	awaitResourceWrite(t, blockedUpdates, first)
	awaitWireResource(t, blockedGate.observed, second)
	awaitResourceWrite(t, blockedUpdates, second)
	if err := blocked.Unsubscribe(ctx, &sdk.UnsubscribeParams{URI: second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(watchNotifyInterval + 20*time.Millisecond)
	instance.tools.watchers.notify(second)
	awaitWireResource(t, fastGate.observed, second)
	awaitResourceWrite(t, fastUpdates, second)
	select {
	case update := <-blockedGate.observed:
		t.Fatalf("unsubscribed session received %q", update.uri)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestResourceDeliveryOverflowRetiresOnlyThatSession(t *testing.T) {
	const first = "tmux://sessions/first"
	const second = "tmux://sessions/second"
	const third = "tmux://sessions/third"
	watchers := admissionTestWatchers()
	watchers.deliveryLimit = 1
	blockedInner := &resourceWriteConnection{
		writes:  make(chan string, 3),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	fastInner := &resourceWriteConnection{writes: make(chan string, 3)}
	blockedScope := newSessionScope(t.Context())
	fastScope := newSessionScope(t.Context())
	blocked := testReadyConnection(blockedInner, nil, nil, func(err error) {
		blockedScope.terminate(err)
	})
	fast := testReadyConnection(fastInner, nil, nil, func(err error) {
		fastScope.terminate(err)
	})
	t.Cleanup(func() {
		blockedInner.unblock()
		blockedScope.close(watchers)
		fastScope.close(watchers)
	})
	for _, uri := range []string{first, second, third} {
		if _, err := blockedScope.subscribe(watchers, blocked, uri, uri); err != nil {
			t.Fatal(err)
		}
		if _, err := fastScope.subscribe(watchers, fast, uri, uri); err != nil {
			t.Fatal(err)
		}
	}

	watchers.notify(first)
	awaitResourceWrite(t, blockedInner.writes, first)
	awaitResourceWrite(t, fastInner.writes, first)
	watchers.notify(second)
	awaitResourceWrite(t, fastInner.writes, second)
	watchers.notify(third)
	awaitResourceWrite(t, fastInner.writes, third)

	deadline := time.Now().Add(time.Second)
	for !errors.Is(blockedScope.terminalCause(), errWatchDeliveryOverflow) {
		if time.Now().After(deadline) {
			t.Fatalf("blocked session terminal error = %v, want delivery overflow",
				blockedScope.terminalCause())
		}
		time.Sleep(time.Millisecond)
	}
	if err := fastScope.terminalCause(); err != nil {
		t.Fatalf("fast session was retired by another session's overflow: %v", err)
	}
}

func awaitResourceWrite(t *testing.T, writes <-chan string, want string) {
	t.Helper()
	select {
	case got := <-writes:
		if got != want {
			t.Fatalf("resource update URI = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("resource update %q was not delivered", want)
	}
}

func awaitWireResource(
	t *testing.T,
	updates <-chan resourceWireEvent,
	want string,
) {
	t.Helper()
	select {
	case update := <-updates:
		if update.uri != want {
			t.Fatalf("wire resource update URI = %q, want %q", update.uri, want)
		}
		if update.call {
			t.Fatalf("wire resource update %q carries an ID", update.uri)
		}
	case <-time.After(time.Second):
		t.Fatalf("wire resource update %q was not delivered", want)
	}
}
