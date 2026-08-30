package mcp

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type orderedLedgerConnection struct {
	reads        chan jsonrpc.Message
	readReturned chan struct{}
	writeVisible chan struct{}
	writeRelease chan struct{}
	writeErr     error
	visibleOnce  sync.Once
}

func (c *orderedLedgerConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case message := <-c.reads:
		c.readReturned <- struct{}{}
		return message, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *orderedLedgerConnection) Write(ctx context.Context, _ jsonrpc.Message) error {
	c.visibleOnce.Do(func() { close(c.writeVisible) })
	if c.writeRelease != nil {
		select {
		case <-c.writeRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.writeErr
}

func (*orderedLedgerConnection) Close() error      { return nil }
func (*orderedLedgerConnection) SessionID() string { return "" }

func TestResponseCommitPrecedesReusedIDAdmission(t *testing.T) {
	id, err := jsonrpc.MakeID("same")
	if err != nil {
		t.Fatal(err)
	}
	inner := &orderedLedgerConnection{
		reads:        make(chan jsonrpc.Message, 2),
		readReturned: make(chan struct{}, 2),
		writeVisible: make(chan struct{}),
		writeRelease: make(chan struct{}),
	}
	events := make(chan string, 3)
	connection := testReadyConnection(
		inner,
		func(jsonrpc.Message) error { events <- "read"; return nil },
		func(jsonrpc.Message) { events <- "write" },
		func(error) {},
	)
	inner.reads <- &jsonrpc.Request{ID: id, Method: "first"}
	if _, err := connection.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	if event := <-events; event != "read" {
		t.Fatalf("first event = %q, want read", event)
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- connection.Write(t.Context(), &jsonrpc.Response{ID: id})
	}()
	<-inner.writeVisible
	inner.reads <- &jsonrpc.Request{ID: id, Method: "second"}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := connection.Read(t.Context())
		readDone <- readErr
	}()
	<-inner.readReturned
	select {
	case event := <-events:
		t.Fatalf("reused ID admitted before response commit: %q", event)
	default:
	}
	close(inner.writeRelease)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if event := <-events; event != "write" {
		t.Fatalf("event after release = %q, want write", event)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if event := <-events; event != "read" {
		t.Fatalf("last event = %q, want reused read", event)
	}
}

type lateReadConnection struct {
	started chan struct{}
	release chan struct{}
	message jsonrpc.Message
	once    sync.Once
}

func (c *lateReadConnection) Read(context.Context) (jsonrpc.Message, error) {
	c.once.Do(func() { close(c.started) })
	<-c.release
	return c.message, nil
}

func (*lateReadConnection) Write(context.Context, jsonrpc.Message) error { return nil }
func (*lateReadConnection) Close() error                                 { return nil }
func (*lateReadConnection) SessionID() string                            { return "" }

func TestCloseCannotReinsertALateRead(t *testing.T) {
	id, err := jsonrpc.MakeID("late")
	if err != nil {
		t.Fatal(err)
	}
	inner := &lateReadConnection{
		started: make(chan struct{}), release: make(chan struct{}),
		message: &jsonrpc.Request{ID: id, Method: "late"},
	}
	var admitted atomic.Int64
	connection := testReadyConnection(
		inner,
		func(jsonrpc.Message) error { admitted.Add(1); return nil },
		func(jsonrpc.Message) {},
		func(error) {},
	)
	readDone := make(chan error, 1)
	go func() {
		_, readErr := connection.Read(t.Context())
		readDone <- readErr
	}()
	<-inner.started
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	close(inner.release)
	if err := <-readDone; !errors.Is(err, ErrInstanceClosed) {
		t.Fatalf("late Read() error = %v, want ErrInstanceClosed", err)
	}
	if admitted.Load() != 0 {
		t.Fatal("a read that completed behind Close was admitted")
	}
}

func TestRejectedWriteSettlesOnlyItsResponse(t *testing.T) {
	rejected := &jsonrpc.Error{Code: -32005, Message: "rejected"}
	inner := &orderedLedgerConnection{
		writeVisible: make(chan struct{}), writeErr: rejected,
	}
	var settled, terminated atomic.Int64
	connection := testReadyConnection(
		inner,
		func(jsonrpc.Message) error { return nil },
		func(jsonrpc.Message) { settled.Add(1) },
		func(error) { terminated.Add(1) },
	)
	if err := connection.Write(t.Context(), &jsonrpc.Response{}); !errors.Is(err, rejected) {
		t.Fatalf("Write() error = %v, want rejected", err)
	}
	if settled.Load() != 1 || terminated.Load() != 0 {
		t.Fatalf("settled/terminated = (%d, %d), want (1, 0)",
			settled.Load(), terminated.Load())
	}
}

func TestCanceledWriteSettlesOnlyItsResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cause := errors.New("request canceled by caller")
	caused, cancelCause := context.WithCancelCause(context.Background())
	cancelCause(cause)
	for name, test := range map[string]struct {
		ctx      context.Context
		writeErr error
	}{
		"context error": {ctx: ctx, writeErr: context.Canceled},
		"context cause": {ctx: caused, writeErr: cause},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &orderedLedgerConnection{
				writeVisible: make(chan struct{}), writeErr: test.writeErr,
			}
			var settled, terminated atomic.Int64
			connection := testReadyConnection(
				inner,
				func(jsonrpc.Message) error { return nil },
				func(jsonrpc.Message) { settled.Add(1) },
				func(error) { terminated.Add(1) },
			)
			if err := connection.Write(test.ctx, &jsonrpc.Response{}); !errors.Is(
				err,
				test.writeErr,
			) {
				t.Fatalf("Write() error = %v, want %v", err, test.writeErr)
			}
			if settled.Load() != 1 || terminated.Load() != 0 {
				t.Fatalf("settled/terminated = (%d, %d), want (1, 0)",
					settled.Load(), terminated.Load())
			}
		})
	}
}

func TestCanceledContextDoesNotHideConnectionWriteFailure(t *testing.T) {
	inner := &orderedLedgerConnection{
		writeVisible: make(chan struct{}), writeErr: io.ErrClosedPipe,
	}
	var settled, terminated atomic.Int64
	connection := testReadyConnection(
		inner,
		func(jsonrpc.Message) error { return nil },
		func(jsonrpc.Message) { settled.Add(1) },
		func(error) { terminated.Add(1) },
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := connection.Write(ctx, &jsonrpc.Response{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Write() error = %v, want io.ErrClosedPipe", err)
	}
	if settled.Load() != 0 || terminated.Load() != 1 {
		t.Fatalf("settled/terminated = (%d, %d), want (0, 1)",
			settled.Load(), terminated.Load())
	}
}

func TestTerminalFreezesItsResponseDrainSet(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "frozen-response-drain-unused",
	}))
	instance.drainWait = time.Hour
	scope := newSessionScope(instance.ctx)
	first, err := jsonrpc.MakeID("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := jsonrpc.MakeID("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.requestRead(scope, &jsonrpc.Request{ID: first, Method: "first"}); err != nil {
		t.Fatalf("initial request admission: %v", err)
	}
	instance.terminal(tmux.ErrDaemonReplaced)
	if err := instance.requestRead(scope, &jsonrpc.Request{ID: second, Method: "second"}); !errors.Is(
		err, ErrInstanceClosed,
	) {
		t.Fatalf("request after terminal drain error = %v, want ErrInstanceClosed", err)
	}
	instance.mutex.Lock()
	count := instance.responses.count()
	instance.mutex.Unlock()
	if count != 1 {
		t.Fatalf("drain count = %d, want frozen count 1", count)
	}
	instance.responseSettled(scope, &jsonrpc.Response{ID: first})
	select {
	case <-instance.closeDone:
	case <-time.After(time.Second):
		t.Fatal("frozen response drain did not finish")
	}
}

func TestTerminalResponseDrainHasABoundedFallback(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "bounded-response-drain-unused",
	}))
	instance.drainWait = 10 * time.Millisecond
	scope := newSessionScope(instance.ctx)
	id, err := jsonrpc.MakeID("never-written")
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.requestRead(scope, &jsonrpc.Request{ID: id, Method: "stuck"}); err != nil {
		t.Fatalf("request admission: %v", err)
	}
	instance.terminal(tmux.ErrDaemonReplaced)
	select {
	case <-instance.closeDone:
	case <-time.After(time.Second):
		t.Fatal("terminal response drain had no bounded fallback")
	}
}

func TestRequestAdmissionLimitsOneSession(t *testing.T) {
	instance := newInstance()
	defer instance.cancel()
	instance.maxSessionCalls = 2
	instance.maxInstanceCalls = 8
	scope := newSessionScope(instance.ctx)

	for _, id := range []string{"first", "second"} {
		if err := instance.requestRead(scope, admissionRequest(t, id)); err != nil {
			t.Fatalf("admit %s: %v", id, err)
		}
	}
	err := instance.requestRead(scope, admissionRequest(t, "third"))
	if !errors.Is(err, ErrRequestCapacity) {
		t.Fatalf("third admission error = %v, want ErrRequestCapacity", err)
	}
	if got := len(instance.responses[scope]); got != 2 {
		t.Fatalf("tracked session calls = %d, want 2", got)
	}
}

func TestRequestAdmissionLimitsTheInstance(t *testing.T) {
	instance := newInstance()
	defer instance.cancel()
	instance.maxSessionCalls = 4
	instance.maxInstanceCalls = 2
	first := newSessionScope(instance.ctx)
	second := newSessionScope(instance.ctx)
	offender := newSessionScope(instance.ctx)

	if err := instance.requestRead(first, admissionRequest(t, "first")); err != nil {
		t.Fatal(err)
	}
	if err := instance.requestRead(second, admissionRequest(t, "second")); err != nil {
		t.Fatal(err)
	}
	err := instance.requestRead(offender, admissionRequest(t, "third"))
	if !errors.Is(err, ErrRequestCapacity) {
		t.Fatalf("instance overflow error = %v, want ErrRequestCapacity", err)
	}
	if got := instance.responses.count(); got != 2 {
		t.Fatalf("tracked instance calls = %d, want 2", got)
	}
}

func TestResponseCommitReleasesRequestCapacity(t *testing.T) {
	instance := newInstance()
	defer instance.cancel()
	instance.maxSessionCalls = 1
	instance.maxInstanceCalls = 1
	scope := newSessionScope(instance.ctx)
	first := admissionRequest(t, "reused")

	if err := instance.requestRead(scope, first); err != nil {
		t.Fatal(err)
	}
	// The SDK removes an ID just before its response enters the wrapped Write.
	// Reuse in that gap must not create an accepted but untracked handler.
	if err := instance.requestRead(scope, first); !errors.Is(err, errDuplicateRequestID) {
		t.Fatalf("duplicate admission error = %v, want errDuplicateRequestID", err)
	}
	if got := instance.responses.count(); got != 1 {
		t.Fatalf("tracked duplicate calls = %d, want 1", got)
	}
	instance.responseSettled(scope, &jsonrpc.Response{ID: first.ID})
	if err := instance.requestRead(scope, admissionRequest(t, "next")); err != nil {
		t.Fatalf("admission after response commit: %v", err)
	}
}

func TestSDKRequestAdmissionClosesOnlyTheOffendingSession(t *testing.T) {
	for _, test := range []struct {
		name             string
		maxInstanceCalls int
		splitAcrossPeers bool
	}{
		{name: "session limit", maxInstanceCalls: 8},
		{name: "instance limit", maxInstanceCalls: 2, splitAcrossPeers: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
				SocketName: "request-admission-" + strings.ReplaceAll(test.name, " ", "-"),
			}))
			instance.maxSessionCalls = 2
			instance.maxInstanceCalls = test.maxInstanceCalls
			started := make(chan struct{}, 3)
			release := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			sdk.AddTool(instance.server, &sdk.Tool{Name: "admission-block"}, func(
				ctx context.Context,
				_ *sdk.CallToolRequest,
				_ struct{},
			) (*sdk.CallToolResult, struct{}, error) {
				started <- struct{}{}
				select {
				case <-release:
					return nil, struct{}{}, nil
				case <-ctx.Done():
					return nil, struct{}{}, ctx.Err()
				}
			})

			connect := func(name string) (*sdk.ClientSession, *ServerSession) {
				clientTransport, serverTransport := sdk.NewInMemoryTransports()
				serverSession, err := instance.Connect(
					t.Context(), AssumeResponseCommit(serverTransport), nil,
				)
				if err != nil {
					t.Fatal(err)
				}
				clientSession, err := sdk.NewClient(
					&sdk.Implementation{Name: name}, nil,
				).Connect(t.Context(), clientTransport, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = clientSession.Close() })
				t.Cleanup(func() { _ = serverSession.Close() })
				return clientSession, serverSession
			}
			offender, offenderServer := connect("admission-offender")
			survivor, _ := connect("admission-survivor")

			callDone := make(chan error, 3)
			call := func(client *sdk.ClientSession) {
				_, err := client.CallTool(t.Context(), &sdk.CallToolParams{
					Name: "admission-block", Arguments: map[string]any{},
				})
				callDone <- err
			}
			go call(offender)
			if test.splitAcrossPeers {
				go call(survivor)
			} else {
				go call(offender)
			}
			for range 2 {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("admitted handler did not start")
				}
			}
			waited := make(chan error, 1)
			go func() { waited <- offenderServer.Wait() }()
			go call(offender)
			select {
			case <-started:
				t.Fatal("a handler started beyond the admission limit")
			case err := <-waited:
				if !errors.Is(err, ErrRequestCapacity) {
					t.Fatalf("offending session error = %v, want ErrRequestCapacity", err)
				}
			case <-time.After(time.Second):
				t.Fatal("overflow did not close the offending session")
			}
			// Ping needs one slot. At the instance limit it proves termination
			// reclaimed the offender's unsettled call before preserving this peer.
			if err := survivor.Ping(t.Context(), nil); err != nil {
				t.Fatalf("surviving session Ping() error = %v", err)
			}
			releaseOnce.Do(func() { close(release) })
			for range 3 {
				select {
				case <-callDone:
				case <-time.After(time.Second):
					t.Fatal("call did not retire")
				}
			}
		})
	}
}

func admissionRequest(t *testing.T, value string) *jsonrpc.Request {
	t.Helper()
	id, err := jsonrpc.MakeID(value)
	if err != nil {
		t.Fatal(err)
	}
	return &jsonrpc.Request{ID: id, Method: "admission-probe"}
}
