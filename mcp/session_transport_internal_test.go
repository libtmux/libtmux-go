package mcp

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testReadyConnection(
	inner mcp.Connection,
	onRequest func(jsonrpc.Message) error,
	onSettled func(jsonrpc.Message),
	onTerminal func(error),
) *sessionReadyConnection {
	ready := make(chan struct{})
	close(ready)
	lifetimeCtx, lifetimeCancel := context.WithCancel(context.Background())
	connection := &sessionReadyConnection{
		inner: inner, ready: ready, onRequest: onRequest,
		onSettled: onSettled, onTerminal: onTerminal, active: true,
		writeSlot:   make(chan struct{}, 1),
		lifetimeCtx: lifetimeCtx, lifetimeCancel: lifetimeCancel,
	}
	connection.changed = sync.NewCond(&connection.stateMutex)
	return connection
}

type firstWriteGateConnection struct {
	started     chan struct{}
	release     chan struct{}
	returned    chan struct{}
	firstErr    error
	calls       atomic.Int64
	once        sync.Once
	releaseOnce sync.Once
	returnOnce  sync.Once
}

func (*firstWriteGateConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *firstWriteGateConnection) Write(
	context.Context,
	jsonrpc.Message,
) error {
	if c.calls.Add(1) != 1 {
		return nil
	}
	c.once.Do(func() { close(c.started) })
	<-c.release
	if c.returned != nil {
		c.returnOnce.Do(func() { close(c.returned) })
	}
	return c.firstErr
}

func (*firstWriteGateConnection) Close() error      { return nil }
func (*firstWriteGateConnection) SessionID() string { return "" }

func (c *firstWriteGateConnection) releaseWrite() {
	c.releaseOnce.Do(func() { close(c.release) })
}

type classificationGateError struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*classificationGateError) Error() string { return "classification gate failed" }

func (e *classificationGateError) As(any) bool {
	e.once.Do(func() { close(e.started) })
	<-e.release
	return false
}

var errCompletionProbe = errors.New("completion probe failed")

type completionProbeConnection struct {
	completed chan time.Time
}

func (*completionProbeConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *completionProbeConnection) Write(context.Context, jsonrpc.Message) error {
	c.completed <- time.Now()
	return errCompletionProbe
}

func (*completionProbeConnection) Close() error      { return nil }
func (*completionProbeConnection) SessionID() string { return "" }

type fatalWriteLifecycleTransport struct {
	connection *fatalWriteLifecycleConnection
}

func (t fatalWriteLifecycleTransport) Connect(context.Context) (mcp.Connection, error) {
	return t.connection, nil
}

type fatalWriteLifecycleConnection struct {
	reads         chan jsonrpc.Message
	writeFailed   chan struct{}
	readReturned  chan error
	physicalClose chan struct{}
	writeOnce     sync.Once
	closeOnce     sync.Once
}

func (c *fatalWriteLifecycleConnection) Read(
	ctx context.Context,
) (jsonrpc.Message, error) {
	select {
	case message := <-c.reads:
		return message, nil
	case <-ctx.Done():
		c.readReturned <- ctx.Err()
		return nil, ctx.Err()
	}
}

func (c *fatalWriteLifecycleConnection) Write(
	context.Context,
	jsonrpc.Message,
) error {
	c.writeOnce.Do(func() { close(c.writeFailed) })
	return io.ErrClosedPipe
}

func (c *fatalWriteLifecycleConnection) Close() error {
	c.closeOnce.Do(func() { close(c.physicalClose) })
	return nil
}

func (*fatalWriteLifecycleConnection) SessionID() string { return "" }

func TestNotificationTimeoutRetiresQueuedResponses(t *testing.T) {
	for name, notification := range map[string]jsonrpc.Message{
		"progress": &jsonrpc.Request{Method: "notifications/progress"},
		"resource update": &jsonrpc.Request{
			Method: "notifications/resources/updated",
		},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &firstWriteGateConnection{
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			defer inner.releaseWrite()
			connection := testReadyConnection(inner, nil, nil, nil)
			connection.notificationWriteWait = 20 * time.Millisecond
			connection.transportCloseWait = 20 * time.Millisecond

			firstDone := make(chan error, 1)
			go func() {
				firstDone <- connection.Write(t.Context(), &jsonrpc.Response{})
			}()
			select {
			case <-inner.started:
			case <-time.After(time.Second):
				t.Fatal("ordinary response write did not start")
			}

			queuedDone := make(chan error, 1)
			go func() {
				queuedDone <- connection.Write(t.Context(), &jsonrpc.Response{})
			}()
			notificationDone := make(chan error, 1)
			go func() { notificationDone <- connection.Write(t.Context(), notification) }()

			select {
			case err := <-notificationDone:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("notification Write() error = %v, want deadline exceeded", err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("notification budget did not include write serialization")
			}
			select {
			case err := <-queuedDone:
				if !errors.Is(err, ErrInstanceClosed) {
					t.Fatalf("queued response Write() error = %v, want ErrInstanceClosed", err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("queued response did not retire with the logical connection")
			}
			if calls := inner.calls.Load(); calls != 1 {
				t.Fatalf("inner Write() calls = %d, want only the owned response write", calls)
			}
			inner.releaseWrite()
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBoundedWritesAreExactlyNotifications(t *testing.T) {
	callID, err := jsonrpc.MakeID("call")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		message jsonrpc.Message
		want    bool
	}{
		"progress notification": {
			message: &jsonrpc.Request{Method: "notifications/progress"}, want: true,
		},
		"resource notification": {
			message: &jsonrpc.Request{Method: "notifications/resources/updated"}, want: true,
		},
		"other notification": {
			message: &jsonrpc.Request{Method: "notifications/other"}, want: true,
		},
		"outbound call": {
			message: &jsonrpc.Request{ID: callID, Method: "sampling/createMessage"},
		},
		"response": {message: &jsonrpc.Response{ID: callID}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := isBoundedNotification(test.message); got != test.want {
				t.Fatalf("isBoundedNotification() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestNotificationDeadlineIncludesCompletionBoundary(t *testing.T) {
	deadline := time.Unix(100, 0)
	for name, test := range map[string]struct {
		completed time.Time
		want      bool
	}{
		"before": {completed: deadline.Add(-time.Nanosecond)},
		"at":     {completed: deadline, want: true},
		"after":  {completed: deadline.Add(time.Nanosecond), want: true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := notificationDeadlineExceeded(test.completed, deadline); got != test.want {
				t.Fatalf("notificationDeadlineExceeded() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestNotificationCancellationWinsReturnAtLateCompletion(t *testing.T) {
	connection := testReadyConnection(&firstWriteGateConnection{
		started: make(chan struct{}), release: make(chan struct{}),
	}, nil, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deadline := time.Now().Add(-time.Second)
	expired, err := connection.notificationCompletionError(
		ctx, nil, deadline.Add(time.Nanosecond), deadline,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("completion error = %v, want context canceled", err)
	}
	if !expired {
		t.Fatal("late physical completion escaped the transport deadline")
	}
	expired, err = connection.notificationCompletionError(
		ctx, io.ErrClosedPipe, deadline.Add(-time.Nanosecond), deadline,
	)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("in-budget completion error = %v, want cancellation and closed pipe", err)
	}
	if expired {
		t.Fatal("in-budget physical failure was classified as a deadline")
	}
	if !writeBreaksConnection(ctx, err) {
		t.Fatal("caller cancellation hid an independent physical failure")
	}
}

func TestClosePublishesInactiveBeforeCancelingWrites(t *testing.T) {
	for name, message := range map[string]jsonrpc.Message{
		"response":     &jsonrpc.Response{},
		"notification": &jsonrpc.Request{Method: "notifications/progress"},
	} {
		t.Run(name, func(t *testing.T) {
			inner := &firstWriteGateConnection{
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			inner.releaseWrite()
			connection := testReadyConnection(inner, nil, nil, nil)
			cancelStarted := make(chan struct{})
			cancelRelease := make(chan struct{})
			var cancelOnce, releaseOnce sync.Once
			originalCancel := connection.lifetimeCancel
			connection.lifetimeCancel = func() {
				cancelOnce.Do(func() {
					close(cancelStarted)
					<-cancelRelease
				})
				originalCancel()
			}
			releaseCancel := func() { releaseOnce.Do(func() { close(cancelRelease) }) }
			defer releaseCancel()

			closed := make(chan error, 1)
			go func() { closed <- connection.Close() }()
			select {
			case <-cancelStarted:
			case <-time.After(time.Second):
				t.Fatal("Close did not reach lifetime cancellation")
			}

			writeErr := connection.Write(t.Context(), message)
			releaseCancel()
			closeErr := <-closed
			if !errors.Is(writeErr, ErrInstanceClosed) {
				t.Fatalf("Write() error = %v, want ErrInstanceClosed", writeErr)
			}
			if closeErr != nil {
				t.Fatalf("Close() error = %v", closeErr)
			}
			if calls := inner.calls.Load(); calls != 0 {
				t.Fatalf("inner Write() calls = %d after Close started, want 0", calls)
			}
		})
	}
}

func TestCloseWaitsForAnAdmittedNotification(t *testing.T) {
	inner := &firstWriteGateConnection{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer inner.releaseWrite()
	connection := testReadyConnection(inner, nil, nil, nil)
	connection.notificationWriteWait = time.Second
	connection.transportCloseWait = time.Second

	written := make(chan error, 1)
	go func() {
		written <- connection.Write(t.Context(), &jsonrpc.Request{
			Method: "notifications/resources/updated",
		})
	}()
	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("notification write did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- connection.Close() }()
	select {
	case err := <-written:
		if !errors.Is(err, ErrInstanceClosed) {
			t.Fatalf("notification Write() error = %v, want ErrInstanceClosed", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("notification caller did not return after Close cancellation")
	}
	select {
	case err := <-closed:
		t.Fatalf("Close() returned before the physical notification write: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	inner.releaseWrite()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not join the admitted notification")
	}
}

func TestCanceledNotificationReturnsBeforePhysicalWrite(t *testing.T) {
	inner := &firstWriteGateConnection{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		returned: make(chan struct{}),
	}
	defer inner.releaseWrite()
	connection := testReadyConnection(inner, nil, nil, nil)
	connection.notificationWriteWait = time.Second

	writeCtx, cancelWrite := context.WithCancel(t.Context())
	written := make(chan error, 1)
	go func() {
		written <- connection.Write(writeCtx, &jsonrpc.Request{
			Method: "notifications/resources/updated",
		})
	}()
	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("notification write did not start")
	}
	cancelWrite()
	select {
	case err := <-written:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("notification Write() error = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("notification Write waited for the blocked physical write")
	}
	select {
	case <-inner.returned:
		t.Fatal("physical write returned before the test released it")
	default:
	}
	connection.stateMutex.Lock()
	writeDone := connection.writeDone
	active := connection.active
	terminalErr := connection.terminalErr
	connection.stateMutex.Unlock()
	if writeDone == nil {
		t.Fatal("canceled notification released its physical write fence")
	}
	if !active || terminalErr != nil {
		t.Fatalf("caller cancellation retired the connection: active=%t error=%v",
			active, terminalErr)
	}
	if occupied := len(connection.writeSlot); occupied != 1 {
		t.Fatalf("write slot occupancy = %d, want quarantined physical writer", occupied)
	}

	inner.releaseWrite()
	select {
	case <-inner.returned:
	case <-time.After(time.Second):
		t.Fatal("physical write did not return after release")
	}
	select {
	case <-writeDone:
	case <-time.After(time.Second):
		t.Fatal("physical write fence did not close after return")
	}
	select {
	case connection.writeSlot <- struct{}{}:
		connection.releaseWriteSlot()
	case <-time.After(time.Second):
		t.Fatal("physical writer did not release the shared write slot")
	}
}

func TestCanceledNotificationRetiresBeforeQueuedResponse(t *testing.T) {
	for name, test := range map[string]struct {
		wait         time.Duration
		physicalFail bool
	}{
		"independent failure": {wait: time.Second, physicalFail: true},
		"absolute deadline":   {wait: 20 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			var classification *classificationGateError
			var releaseClassification func()
			var firstErr error
			if test.physicalFail {
				classification = &classificationGateError{
					started: make(chan struct{}), release: make(chan struct{}),
				}
				var releaseOnce sync.Once
				releaseClassification = func() {
					releaseOnce.Do(func() { close(classification.release) })
				}
				firstErr = errors.Join(context.Canceled, classification)
				defer releaseClassification()
			}
			inner := &firstWriteGateConnection{
				started: make(chan struct{}), release: make(chan struct{}),
				returned: make(chan struct{}), firstErr: firstErr,
			}
			defer inner.releaseWrite()
			terminal := make(chan error, 1)
			connection := testReadyConnection(inner, nil, nil, func(err error) {
				terminal <- err
			})
			connection.notificationWriteWait = test.wait

			writeCtx, cancelWrite := context.WithCancel(t.Context())
			written := make(chan error, 1)
			go func() {
				written <- connection.Write(writeCtx, &jsonrpc.Request{
					Method: "notifications/resources/updated",
				})
			}()
			select {
			case <-inner.started:
			case <-time.After(time.Second):
				t.Fatal("notification write did not start")
			}
			cancelWrite()
			if err := <-written; !errors.Is(err, context.Canceled) {
				t.Fatalf("notification Write() error = %v, want context canceled", err)
			}
			queued := make(chan error, 1)
			go func() { queued <- connection.Write(t.Context(), &jsonrpc.Response{}) }()

			if classification != nil {
				inner.releaseWrite()
				select {
				case <-classification.started:
				case <-time.After(time.Second):
					t.Fatal("monitor did not classify the independent write failure")
				}
				assertQueuedResponseHeld(t, inner, queued)
				releaseClassification()
			}
			if !test.physicalFail {
				assertQueuedResponseHeld(t, inner, queued)
			}
			select {
			case err := <-terminal:
				if test.physicalFail && !errors.Is(err, firstErr) {
					t.Fatalf("terminal error = %v, want physical write failure", err)
				}
				if !test.physicalFail && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("terminal error = %v, want deadline exceeded", err)
				}
			case <-time.After(time.Second):
				t.Fatal("quarantined notification did not retire the connection")
			}
			if !test.physicalFail {
				inner.releaseWrite()
			}
			select {
			case err := <-queued:
				if !errors.Is(err, ErrInstanceClosed) {
					t.Fatalf("queued response Write() error = %v, want ErrInstanceClosed", err)
				}
			case <-time.After(time.Second):
				t.Fatal("queued response did not retire with the connection")
			}
			if calls := inner.calls.Load(); calls != 1 {
				t.Fatalf("inner Write() calls = %d, want only the notification", calls)
			}
		})
	}
}

func assertQueuedResponseHeld(
	t *testing.T,
	inner *firstWriteGateConnection,
	queued <-chan error,
) {
	t.Helper()
	select {
	case err := <-queued:
		t.Fatalf("queued response escaped before notification retirement: %v", err)
	default:
	}
	if calls := inner.calls.Load(); calls != 1 {
		t.Fatalf("inner Write() calls = %d before retirement, want 1", calls)
	}
}

func TestJoinedCancellationDoesNotHideWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := errors.Join(context.Canceled, io.ErrClosedPipe)
	if !writeBreaksConnection(ctx, err) {
		t.Fatal("joined independent failure was treated as caller cancellation")
	}
}

func TestWriteCompletionUsesAbsoluteCloseDeadline(t *testing.T) {
	deadline := time.Now().Add(-time.Second)
	for name, test := range map[string]struct {
		completed time.Time
		want      error
	}{
		"before": {completed: deadline.Add(-time.Nanosecond)},
		"at":     {completed: deadline, want: errTransportCloseTimeout},
		"after":  {completed: deadline.Add(time.Nanosecond), want: errTransportCloseTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			connection := testReadyConnection(&firstWriteGateConnection{
				started: make(chan struct{}), release: make(chan struct{}),
			}, nil, nil, nil)
			connection.stateMutex.Lock()
			connection.writeCompleted = test.completed
			connection.stateMutex.Unlock()

			err := connection.waitForWrite(deadline)
			if !errors.Is(err, test.want) {
				t.Fatalf("waitForWrite() error = %v, want %v", err, test.want)
			}
			connection.stateMutex.Lock()
			timedOut := connection.closeTimedOut
			connection.stateMutex.Unlock()
			if timedOut != (test.want != nil) {
				t.Fatalf("sticky close timeout = %t, want %t",
					timedOut, test.want != nil)
			}
		})
	}
}

func TestNotificationFenceRecordsPhysicalCompletion(t *testing.T) {
	inner := &completionProbeConnection{completed: make(chan time.Time, 1)}
	type terminalResult struct {
		err     error
		started time.Time
	}
	terminal := make(chan terminalResult, 1)
	releaseTerminal := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseTerminal) }) }
	defer release()
	connection := testReadyConnection(inner, nil, nil, func(err error) {
		terminal <- terminalResult{err: err, started: time.Now()}
		<-releaseTerminal
	})
	connection.notificationWriteWait = time.Second

	written := make(chan error, 1)
	go func() {
		written <- connection.Write(t.Context(), &jsonrpc.Request{
			Method: "notifications/resources/updated",
		})
	}()
	innerReturning := <-inner.completed
	terminalCall := <-terminal
	if !errors.Is(terminalCall.err, errCompletionProbe) {
		t.Fatalf("terminal error = %v, want completion probe error", terminalCall.err)
	}
	connection.stateMutex.Lock()
	writeDone := connection.writeDone
	connection.stateMutex.Unlock()
	if writeDone == nil {
		t.Fatal("notification has no active write fence")
	}
	time.Sleep(2 * time.Millisecond)
	release()
	if err := <-written; !errors.Is(err, errCompletionProbe) {
		t.Fatalf("notification Write() error = %v, want completion probe error", err)
	}
	select {
	case <-writeDone:
	case <-time.After(time.Second):
		t.Fatal("notification write fence did not close")
	}
	connection.stateMutex.Lock()
	recordedCompletion := connection.writeCompleted
	connection.stateMutex.Unlock()
	if recordedCompletion.Before(innerReturning) ||
		recordedCompletion.After(terminalCall.started) {
		t.Fatalf("recorded completion %v falls outside physical return window [%v, %v]",
			recordedCompletion, innerReturning, terminalCall.started)
	}
}

func TestFatalOrdinaryWriteClosesSDKReaderAndTransport(t *testing.T) {
	inner := &fatalWriteLifecycleConnection{
		reads: make(chan jsonrpc.Message, 1), writeFailed: make(chan struct{}),
		readReturned: make(chan error, 1), physicalClose: make(chan struct{}),
	}
	ready := make(chan struct{})
	close(ready)
	terminal := make(chan error, 1)
	server := mcp.NewServer(&mcp.Implementation{
		Name: "fatal-write-lifecycle", Version: "0",
	}, nil)
	session, err := server.Connect(t.Context(), sessionReadyTransport{
		inner: fatalWriteLifecycleTransport{connection: inner},
		ready: ready,
		onTerminal: func(err error) {
			terminal <- err
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	id, err := jsonrpc.MakeID("fatal-write")
	if err != nil {
		t.Fatal(err)
	}
	inner.reads <- &jsonrpc.Request{ID: id, Method: "ping"}

	select {
	case <-inner.writeFailed:
	case <-time.After(time.Second):
		t.Fatal("SDK did not attempt the ping response")
	}
	select {
	case err := <-terminal:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("terminal error = %v, want closed pipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fatal response write did not mark the wrapper terminal")
	}
	select {
	case <-inner.physicalClose:
	case <-time.After(time.Second):
		t.Fatal("SDK did not physically close after the fatal response write")
	}
	select {
	case err := <-inner.readReturned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("reader error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fatal response write left the SDK reader blocked")
	}
	waited := make(chan error, 1)
	go func() { waited <- session.Wait() }()
	select {
	case err := <-waited:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("ServerSession.Wait() error = %v, want closed pipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SDK session did not finish after fatal response write")
	}
}

type delayedCloseConnection struct {
	release  chan struct{}
	returned chan struct{}
	calls    atomic.Int64
}

func (*delayedCloseConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*delayedCloseConnection) Write(context.Context, jsonrpc.Message) error { return nil }

func (c *delayedCloseConnection) Close() error {
	c.calls.Add(1)
	<-c.release
	close(c.returned)
	return nil
}

func (*delayedCloseConnection) SessionID() string { return "" }

func TestConnectionClosePreservesDeadlineTimeout(t *testing.T) {
	inner := &delayedCloseConnection{
		release:  make(chan struct{}),
		returned: make(chan struct{}),
	}
	connection := testReadyConnection(inner, nil, nil, nil)
	connection.transportCloseWait = 20 * time.Millisecond

	if err := connection.Close(); !errors.Is(err, errTransportCloseTimeout) {
		t.Fatalf("first Close() error = %v, want transport close timeout", err)
	}
	close(inner.release)
	select {
	case <-inner.returned:
	case <-time.After(time.Second):
		t.Fatal("physical transport Close did not return")
	}

	const callers = 4
	errorsReturned := make(chan error, callers)
	for range callers {
		go func() { errorsReturned <- connection.Close() }()
	}
	for range callers {
		if err := <-errorsReturned; !errors.Is(err, errTransportCloseTimeout) {
			t.Fatalf("repeated Close() error = %v, want transport close timeout", err)
		}
	}
	if calls := inner.calls.Load(); calls != 1 {
		t.Fatalf("physical transport Close calls = %d, want 1", calls)
	}
}
