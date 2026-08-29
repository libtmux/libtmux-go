package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type blockingProgressTransport struct {
	connection *blockingProgressConnection
}

func (t blockingProgressTransport) Connect(context.Context) (sdk.Connection, error) {
	return t.connection, nil
}

type progressGateTransport struct {
	inner           sdk.Transport
	started         chan struct{}
	returned        chan struct{}
	writeRelease    chan struct{}
	closeStarted    chan struct{}
	closeReturned   chan struct{}
	closeRelease    chan struct{}
	once            sync.Once
	writeReleaseOne sync.Once
	closeReleaseOne sync.Once
	closes          atomic.Int32
}

func (t *progressGateTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &progressGateConnection{inner: connection, gate: t}, nil
}

type progressGateConnection struct {
	inner sdk.Connection
	gate  *progressGateTransport
}

func (c *progressGateConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	return c.inner.Read(ctx)
}

func (c *progressGateConnection) Write(
	ctx context.Context,
	message jsonrpc.Message,
) error {
	request, progress := message.(*jsonrpc.Request)
	if !progress || request.Method != "notifications/progress" {
		return c.inner.Write(ctx, message)
	}
	c.gate.once.Do(func() { close(c.gate.started) })
	defer close(c.gate.returned)
	<-c.gate.writeRelease
	return io.ErrClosedPipe
}

func (c *progressGateConnection) Close() error {
	c.gate.closes.Add(1)
	err := c.inner.Close()
	close(c.gate.closeStarted)
	<-c.gate.closeRelease
	close(c.gate.closeReturned)
	return err
}

func (c *progressGateConnection) SessionID() string { return c.inner.SessionID() }

func (t *progressGateTransport) releasePhysicalWork() {
	t.writeReleaseOne.Do(func() { close(t.writeRelease) })
	t.closeReleaseOne.Do(func() { close(t.closeRelease) })
}

type blockingProgressConnection struct {
	closed        chan struct{}
	release       chan struct{}
	writeStarted  chan struct{}
	writeReturned chan struct{}
	writes        chan sdk.ProgressNotificationParams
	closeOnce     sync.Once
	releaseOnce   sync.Once
	startOnce     sync.Once
	returnOnce    sync.Once
	ignoreCancel  bool
	calls         atomic.Int32
	active        atomic.Int32
	maxActive     atomic.Int32
}

func newBlockingProgressConnection() *blockingProgressConnection {
	return &blockingProgressConnection{
		closed:        make(chan struct{}),
		release:       make(chan struct{}),
		writeStarted:  make(chan struct{}),
		writeReturned: make(chan struct{}),
		writes:        make(chan sdk.ProgressNotificationParams, 8),
	}
}

func (c *blockingProgressConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, io.EOF
	}
}

func (c *blockingProgressConnection) Write(
	ctx context.Context,
	message jsonrpc.Message,
) error {
	call := c.calls.Add(1)
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for {
		maximum := c.maxActive.Load()
		if active <= maximum || c.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	request, ok := message.(*jsonrpc.Request)
	if !ok {
		return io.ErrUnexpectedEOF
	}
	var params sdk.ProgressNotificationParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return err
	}
	c.writes <- params
	c.startOnce.Do(func() { close(c.writeStarted) })
	defer c.returnOnce.Do(func() { close(c.writeReturned) })
	var err error
	if c.ignoreCancel && call == 1 {
		select {
		case <-c.release:
		case <-c.closed:
			err = io.ErrClosedPipe
		}
	} else {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-c.closed:
			err = io.ErrClosedPipe
		}
	}
	return err
}

func (c *blockingProgressConnection) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (*blockingProgressConnection) SessionID() string { return "progress-test" }

func (c *blockingProgressConnection) releaseWrite() {
	c.releaseOnce.Do(func() { close(c.release) })
}

func TestProgressReporterStopCancelsAndJoinsAnActiveWrite(t *testing.T) {
	connection := newBlockingProgressConnection()
	server := sdk.NewServer(&sdk.Implementation{Name: "progress-test", Version: "0"}, nil)
	session, err := server.Connect(t.Context(), blockingProgressTransport{connection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	params := &sdk.CallToolParamsRaw{}
	params.SetProgressToken("progress-token")
	reporter := newProgressReporter(
		t.Context(),
		&sdk.CallToolRequest{Session: session, Params: params},
		10*time.Second,
		"blocked progress",
	)

	select {
	case <-connection.writeStarted:
	case <-time.After(3 * time.Second):
		_ = connection.Close()
		t.Fatal("progress notification did not start")
	}
	stopped := make(chan struct{})
	go func() {
		reporter.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		select {
		case <-connection.writeReturned:
		default:
			_ = connection.Close()
			t.Fatal("stop returned while the progress write was still blocked")
		}
	case <-time.After(time.Second):
		_ = connection.Close()
		t.Fatal("stop did not cancel and join the progress write")
	}
}

func TestProgressReporterStopBoundsANonCooperativeWrite(t *testing.T) {
	connection := newBlockingProgressConnection()
	connection.ignoreCancel = true
	defer connection.releaseWrite()
	server := sdk.NewServer(&sdk.Implementation{Name: "progress-test", Version: "0"}, nil)
	session, err := server.Connect(t.Context(), blockingProgressTransport{connection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	params := &sdk.CallToolParamsRaw{}
	params.SetProgressToken("progress-token")
	reporter := startProgressReporter(
		t.Context(),
		&sdk.CallToolRequest{Session: session, Params: params},
		10*time.Second,
		"blocked progress",
		10*time.Millisecond,
		nil,
		40*time.Millisecond,
	)

	select {
	case <-connection.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("progress notification did not start")
	}
	stopped := make(chan struct{})
	go func() {
		reporter.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		select {
		case <-connection.writeReturned:
			t.Fatal("blocked progress write returned before transport release")
		default:
		}
	case <-time.After(250 * time.Millisecond):
		connection.releaseWrite()
		<-stopped
		t.Fatal("reporter stop waited without a bound on the progress write")
	}
}

func TestProgressReporterCoalescesBlockedUpdatesToLatest(t *testing.T) {
	connection := newBlockingProgressConnection()
	connection.ignoreCancel = true
	defer connection.releaseWrite()
	server := sdk.NewServer(&sdk.Implementation{Name: "progress-test", Version: "0"}, nil)
	session, err := server.Connect(t.Context(), blockingProgressTransport{connection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	params := &sdk.CallToolParamsRaw{}
	params.SetProgressToken("progress-token")
	ticks := make(chan time.Time)
	base := time.Now()
	reporter := startProgressReporter(
		t.Context(),
		&sdk.CallToolRequest{Session: session, Params: params},
		10*time.Second,
		"blocked progress",
		time.Hour,
		ticks,
		40*time.Millisecond,
	)
	defer reporter.stop()

	offerProgressTick(t, ticks, base.Add(time.Second))
	first := receiveProgress(t, connection.writes)
	offerProgressTick(t, ticks, base.Add(2*time.Second))
	offerProgressTick(t, ticks, base.Add(3*time.Second))
	if got := connection.calls.Load(); got != 1 {
		t.Fatalf("concurrent progress writes = %d, want 1", got)
	}
	if got := connection.maxActive.Load(); got != 1 {
		t.Fatalf("maximum active progress writes = %d, want 1", got)
	}

	connection.releaseWrite()
	second := receiveProgress(t, connection.writes)
	if advanced := second.Progress - first.Progress; advanced < 1500*time.Millisecond.Seconds() {
		t.Fatalf("coalesced progress advanced %v seconds, want the latest update", advanced)
	}
}

func TestProgressWriteTimeoutBoundsToolAndInstanceShutdown(t *testing.T) {
	target := mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "progress-lifecycle-unused",
		Runner: tmux.CommandRunnerFunc(func(
			context.Context,
			tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			return tmux.CommandResult{Stdout: []string{"tmux 3.6"}}, nil
		}),
	})
	instance := mustInternalMCPServer(t, target)
	finish := make(chan struct{})
	sdk.AddTool(instance.server, &sdk.Tool{Name: "blocked_progress"}, func(
		ctx context.Context,
		request *sdk.CallToolRequest,
		_ struct{},
	) (*sdk.CallToolResult, struct{}, error) {
		reporter := startProgressReporter(
			ctx,
			request,
			10*time.Second,
			"blocked progress",
			10*time.Millisecond,
			nil,
			40*time.Millisecond,
		)
		defer reporter.stop()
		<-finish
		return &sdk.CallToolResult{}, struct{}{}, nil
	})

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	gate := &progressGateTransport{
		inner: serverTransport, started: make(chan struct{}),
		returned: make(chan struct{}), writeRelease: make(chan struct{}),
		closeStarted: make(chan struct{}), closeReturned: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
	defer gate.releasePhysicalWork()
	serverSession, err := instance.Connect(
		t.Context(), AssumeResponseCommit(gate), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	serverSession.connection.notificationWriteWait = 40 * time.Millisecond
	serverSession.connection.transportCloseWait = 40 * time.Millisecond
	clientSession, err := sdk.NewClient(
		&sdk.Implementation{Name: "progress-client", Version: "0"}, nil,
	).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	params := &sdk.CallToolParams{Name: "blocked_progress"}
	params.SetProgressToken("progress-token")
	called := make(chan error, 1)
	go func() {
		_, callErr := clientSession.CallTool(t.Context(), params)
		called <- callErr
	}()

	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("progress write did not start")
	}
	close(finish)
	select {
	case callErr := <-called:
		if callErr == nil {
			t.Fatal("CallTool() succeeded after its progress write timed out")
		}
	case <-time.After(250 * time.Millisecond):
		gate.releasePhysicalWork()
		t.Fatal("tool termination waited for the blocked progress write")
	}
	select {
	case <-gate.returned:
		t.Fatal("transport Close released the quarantined progress write")
	default:
	}
	select {
	case <-gate.closeStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("progress timeout did not start physical transport close")
	}
	select {
	case <-gate.closeReturned:
		t.Fatal("physical transport close returned before test release")
	default:
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := instance.CloseContext(closeCtx); !errors.Is(err, errTransportCloseTimeout) {
		t.Fatalf("Instance.CloseContext() error = %v, want transport close timeout", err)
	}
	if got := gate.closes.Load(); got != 1 {
		t.Fatalf("transport Close() calls = %d, want 1", got)
	}
	select {
	case <-gate.returned:
		t.Fatal("instance shutdown joined the quarantined progress write")
	default:
	}
	select {
	case <-gate.closeReturned:
		t.Fatal("instance shutdown joined the quarantined physical close")
	default:
	}
	gate.releasePhysicalWork()
	select {
	case <-gate.returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("quarantined progress write did not leave after release")
	}
	select {
	case <-gate.closeReturned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("quarantined transport close did not leave after release")
	}
}

func offerProgressTick(t *testing.T, ticks chan<- time.Time, tick time.Time) {
	t.Helper()
	select {
	case ticks <- tick:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("progress ticker blocked behind notification delivery")
	}
}

func receiveProgress(
	t *testing.T,
	writes <-chan sdk.ProgressNotificationParams,
) sdk.ProgressNotificationParams {
	t.Helper()
	select {
	case params := <-writes:
		return params
	case <-time.After(time.Second):
		t.Fatal("progress notification was not written")
		return sdk.ProgressNotificationParams{}
	}
}
