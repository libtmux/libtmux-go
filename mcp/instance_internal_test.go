package mcp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type readSpyConnection struct {
	started chan struct{}
	once    sync.Once
}

func (c *readSpyConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*readSpyConnection) Write(context.Context, jsonrpc.Message) error { return nil }
func (*readSpyConnection) Close() error                                 { return nil }
func (*readSpyConnection) SessionID() string                            { return "" }

func TestSessionReadWaitsForScopeAdmission(t *testing.T) {
	ready := make(chan struct{})
	inner := &readSpyConnection{started: make(chan struct{})}
	connection := &sessionReadyConnection{inner: inner, ready: ready}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = connection.Read(ctx)
	}()

	select {
	case <-inner.started:
		t.Fatal("inner transport read before session admission")
	case <-time.After(25 * time.Millisecond):
	}
	close(ready)
	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("admitted read did not reach the inner transport")
	}
	cancel()
	<-done
}

type blockingTransport struct {
	started chan struct{}
}

var errVersionGateTransportReached = errors.New("version gate transport reached")

type versionGateTransport struct {
	calls atomic.Int32
}

func (t *versionGateTransport) Connect(context.Context) (sdk.Connection, error) {
	t.calls.Add(1)
	return nil, errVersionGateTransportReached
}

func TestInstanceConnectChecksTheMCPVersionBeforeTransport(t *testing.T) {
	for _, test := range []struct {
		version   string
		fixture   string
		wantCalls int32
		wantError error
	}{
		{version: "3.5", fixture: fixtureVersion35, wantError: tmux.ErrVersionTooLow},
		{version: "3.6", fixture: fixtureVersion36, wantCalls: 1, wantError: errVersionGateTransportReached},
	} {
		t.Run(test.version, func(t *testing.T) {
			target := mustInternalTmuxServer(t, executableFixtureOptions(t, test.fixture, tmux.ServerOptions{
				SocketName: "version-gate-unused",
			}))
			instance := mustInternalMCPServer(t, target)
			transport := &versionGateTransport{}
			_, err := instance.Connect(
				t.Context(), AssumeResponseCommit(transport), nil,
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Connect() error = %v, want %v", err, test.wantError)
			}
			if calls := transport.calls.Load(); calls != test.wantCalls {
				t.Fatalf("transport Connect calls = %d, want %d", calls, test.wantCalls)
			}
			if test.version == "3.5" {
				var tooLow *tmux.VersionTooLowError
				if !errors.As(err, &tooLow) || tooLow.Current.String() != "3.5" ||
					tooLow.Minimum.String() != minimumTmuxVersion {
					t.Fatalf("Connect() error = %#v, want current 3.5 and minimum %s",
						err, minimumTmuxVersion)
				}
			}
		})
	}
}

func TestInstanceConnectRejectsInstalledTmuxBelowMCPFloor(t *testing.T) {
	target := mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "installed-version-gate-unused",
	})
	current, err := target.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion(minimumTmuxVersion)
	if err != nil {
		t.Fatal(err)
	}
	if current.AtLeast(minimum) {
		t.Skipf("installed tmux %s meets the MCP %s floor", current, minimum)
	}

	instance := mustInternalMCPServer(t, target)
	transport := &versionGateTransport{}
	_, err = instance.Connect(
		t.Context(), AssumeResponseCommit(transport), nil,
	)
	if !errors.Is(err, tmux.ErrVersionTooLow) {
		t.Fatalf("Connect() error = %v, want ErrVersionTooLow", err)
	}
	var tooLow *tmux.VersionTooLowError
	if !errors.As(err, &tooLow) || tooLow.Current != current || tooLow.Minimum != minimum {
		t.Fatalf("Connect() error = %#v, want current %s and minimum %s",
			err, current, minimum)
	}
	if calls := transport.calls.Load(); calls != 0 {
		t.Fatalf("transport Connect calls = %d, want 0", calls)
	}
}

func TestInstanceRejectsAnUnknownResponseCommitBeforeTransport(t *testing.T) {
	target := mustInternalTmuxServer(t, executableFixtureOptions(t, fixtureVersion36, tmux.ServerOptions{
		SocketName: "response-commit-unused",
	}))
	instance := mustInternalMCPServer(t, target)
	transport := &versionGateTransport{}
	_, err := instance.Connect(t.Context(), transport, nil)
	if !errors.Is(err, ErrResponseCommitUnknown) {
		t.Fatalf("Connect() error = %v, want ErrResponseCommitUnknown", err)
	}
	if calls := transport.calls.Load(); calls != 0 {
		t.Fatalf("transport Connect calls = %d, want 0", calls)
	}
}

func (t *blockingTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	close(t.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestInstanceCloseCancelsAConnectingTransport(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "connecting-close-unused",
	}))
	transport := &blockingTransport{started: make(chan struct{})}
	connected := make(chan error, 1)
	go func() {
		_, err := instance.Connect(
			context.Background(), AssumeResponseCommit(transport), nil,
		)
		connected <- err
	}()
	<-transport.started

	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-connected:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Connect() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Connect() remained blocked after instance shutdown")
	}
}

func TestSessionCloseCancelsAHandlerBeforeJoiningIt(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "handler-close-unused",
	}))
	started := make(chan struct{})
	stopped := make(chan struct{})
	sdk.AddTool(instance.server, &sdk.Tool{Name: "blocking"}, func(
		ctx context.Context,
		_ *sdk.CallToolRequest,
		_ struct{},
	) (*sdk.CallToolResult, struct{}, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, struct{}{}, ctx.Err()
	})

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(
		t.Context(), AssumeResponseCommit(serverTransport), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	clientSession, err := sdk.NewClient(
		&sdk.Implementation{Name: "handler-close"}, nil,
	).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	called := make(chan struct{})
	go func() {
		defer close(called)
		_, _ = clientSession.CallTool(context.Background(), &sdk.CallToolParams{
			Name: "blocking", Arguments: map[string]any{},
		})
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- serverSession.Close() }()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("session Close did not cancel the active handler")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("session Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session Close did not join the canceled handler")
	}
	<-called
}

type orderingSpyTransport struct {
	inner     sdk.Transport
	violation bool
	mutex     sync.Mutex
}

func (t *orderingSpyTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &orderingSpyConnection{inner: connection, owner: t}, nil
}

type orderingSpyConnection struct {
	inner       sdk.Connection
	owner       *orderingSpyTransport
	initialized bool
	mutex       sync.Mutex
}

func (c *orderingSpyConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.inner.Read(ctx)
	if request, ok := message.(*jsonrpc.Request); ok &&
		request.Method == "notifications/initialized" {
		c.mutex.Lock()
		c.initialized = true
		c.mutex.Unlock()
	}
	return message, err
}

func (c *orderingSpyConnection) Write(
	ctx context.Context,
	message jsonrpc.Message,
) error {
	if request, ok := message.(*jsonrpc.Request); ok &&
		request.Method == notificationToolListChanged {
		c.mutex.Lock()
		initialized := c.initialized
		c.mutex.Unlock()
		if !initialized {
			c.owner.mutex.Lock()
			c.owner.violation = true
			c.owner.mutex.Unlock()
		}
	}
	return c.inner.Write(ctx, message)
}

func (c *orderingSpyConnection) Close() error      { return c.inner.Close() }
func (c *orderingSpyConnection) SessionID() string { return c.inner.SessionID() }

func TestInstanceConnectAlwaysOrdersTheHandshake(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "handshake-unused",
	}))
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	spy := &orderingSpyTransport{inner: serverTransport}
	serverSession, err := instance.Connect(t.Context(), AssumeResponseCommit(spy), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	sdk.AddTool(instance.server, &sdk.Tool{Name: "late-ordering-probe"}, func(
		context.Context,
		*sdk.CallToolRequest,
		struct{},
	) (*sdk.CallToolResult, struct{}, error) {
		return nil, struct{}{}, nil
	})

	clientSession, err := sdk.NewClient(
		&sdk.Implementation{Name: "ordering-probe"}, nil,
	).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	spy.mutex.Lock()
	violation := spy.violation
	spy.mutex.Unlock()
	if violation {
		t.Fatal("tools/list_changed reached transport before initialized")
	}
}
