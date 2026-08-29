package mcp

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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
		func(jsonrpc.Message) bool { events <- "read"; return true },
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
		func(jsonrpc.Message) bool { admitted.Add(1); return true },
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
		func(jsonrpc.Message) bool { return true },
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
				func(jsonrpc.Message) bool { return true },
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
		func(jsonrpc.Message) bool { return true },
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
	if !instance.requestRead(scope, &jsonrpc.Request{ID: first, Method: "first"}) {
		t.Fatal("initial request was not admitted")
	}
	instance.terminal(tmux.ErrDaemonReplaced)
	if instance.requestRead(scope, &jsonrpc.Request{ID: second, Method: "second"}) {
		t.Fatal("request admitted after terminal drain began")
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
	if !instance.requestRead(scope, &jsonrpc.Request{ID: id, Method: "stuck"}) {
		t.Fatal("request was not admitted")
	}
	instance.terminal(tmux.ErrDaemonReplaced)
	select {
	case <-instance.closeDone:
	case <-time.After(time.Second):
		t.Fatal("terminal response drain had no bounded fallback")
	}
}
