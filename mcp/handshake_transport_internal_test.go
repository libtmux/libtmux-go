package mcp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type handshakeProbeTransport struct {
	connection *handshakeProbeConnection
}

func (t handshakeProbeTransport) Connect(context.Context) (sdk.Connection, error) {
	return t.connection, nil
}

type handshakeProbeConnection struct {
	reads         chan jsonrpc.Message
	writeStarted  chan struct{}
	writeReturned chan struct{}
	writeRelease  <-chan struct{}
	startOnce     sync.Once
	returnOnce    sync.Once
	writes        atomic.Int64
}

func (c *handshakeProbeConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case message := <-c.reads:
		return message, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *handshakeProbeConnection) Write(
	ctx context.Context,
	_ jsonrpc.Message,
) error {
	c.writes.Add(1)
	c.startOnce.Do(func() { close(c.writeStarted) })
	if c.writeRelease != nil {
		select {
		case <-c.writeRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.returnOnce.Do(func() { close(c.writeReturned) })
	return nil
}

func (*handshakeProbeConnection) Close() error      { return nil }
func (*handshakeProbeConnection) SessionID() string { return "" }

func connectHandshakeProbe(
	t *testing.T,
	probe *handshakeProbeConnection,
) (sdk.Connection, *sessionReadyConnection) {
	t.Helper()
	ready := make(chan struct{})
	close(ready)
	var tracked *sessionReadyConnection
	managedTransport := sessionReadyTransport{
		inner: handshakeProbeTransport{connection: probe},
		ready: ready,
		onConnect: func(connection *sessionReadyConnection) {
			tracked = connection
		},
	}
	transport := HandshakeOrdered(managedTransport)
	connection, err := transport.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tracked == nil {
		t.Fatal("session transport did not expose its tracked connection")
	}
	return connection, tracked
}

func TestHeldHandshakeNotificationUsesBoundedSessionWrite(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWrite := func() { releaseOnce.Do(func() { close(release) }) }
	probe := &handshakeProbeConnection{
		reads: make(chan jsonrpc.Message, 1), writeStarted: make(chan struct{}),
		writeReturned: make(chan struct{}), writeRelease: release,
	}
	defer releaseWrite()
	connection, tracked := connectHandshakeProbe(t, probe)
	tracked.notificationWriteWait = 20 * time.Millisecond
	tracked.transportCloseWait = 20 * time.Millisecond
	if err := connection.Write(t.Context(), &jsonrpc.Request{
		Method: notificationToolListChanged,
	}); err != nil {
		t.Fatal(err)
	}
	if calls := probe.writes.Load(); calls != 0 {
		t.Fatalf("held notification reached the raw transport %d times", calls)
	}
	probe.reads <- &jsonrpc.Request{Method: "notifications/initialized"}

	read := make(chan error, 1)
	go func() {
		_, err := connection.Read(t.Context())
		read <- err
	}()
	select {
	case <-probe.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("held notification did not start after initialization")
	}
	select {
	case err := <-read:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("initialized Read() error = %v, want deadline exceeded", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("held notification bypassed the bounded session write")
	}
	if calls := probe.writes.Load(); calls != 1 {
		t.Fatalf("raw notification writes = %d, want 1", calls)
	}
	closed := make(chan error, 1)
	go func() { closed <- tracked.Close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, errTransportCloseTimeout) {
			t.Fatalf("tracked Close() error = %v, want transport close timeout", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("tracked Close did not bound the quarantined handshake write")
	}
	releaseWrite()
	select {
	case <-probe.writeReturned:
	case <-time.After(time.Second):
		t.Fatal("quarantined handshake write did not leave after release")
	}
}

func TestHeldHandshakeNotificationsAreCoalesced(t *testing.T) {
	probe := &handshakeProbeConnection{
		reads: make(chan jsonrpc.Message, 1), writeStarted: make(chan struct{}),
		writeReturned: make(chan struct{}),
	}
	connection, _ := connectHandshakeProbe(t, probe)
	for range 32 {
		if err := connection.Write(t.Context(), &jsonrpc.Request{
			Method: notificationToolListChanged,
		}); err != nil {
			t.Fatal(err)
		}
	}
	probe.reads <- &jsonrpc.Request{Method: "notifications/initialized"}
	if _, err := connection.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls := probe.writes.Load(); calls != 1 {
		t.Fatalf("raw notification writes = %d, want one coalesced update", calls)
	}
}
