package mcp

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const notificationWriteWait = time.Second

var errTransportCloseTimeout = errors.New("mcp transport close timed out")

// sessionReadyTransport prevents the SDK reader from observing a request
// before Instance has installed the new session's scope.
type sessionReadyTransport struct {
	inner      mcp.Transport
	ready      <-chan struct{}
	onRequest  func(jsonrpc.Message) bool
	onSettled  func(jsonrpc.Message)
	onTerminal func(error)
	onConnect  func(*sessionReadyConnection)
}

func (t sessionReadyTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	lifetimeCtx, lifetimeCancel := context.WithCancel(context.Background())
	wrapped := &sessionReadyConnection{
		inner: connection, ready: t.ready, onRequest: t.onRequest,
		onSettled: t.onSettled, onTerminal: t.onTerminal, active: true,
		notificationWriteWait: notificationWriteWait,
		transportCloseWait:    notificationWriteWait,
		lifetimeCtx:           lifetimeCtx, lifetimeCancel: lifetimeCancel,
		writeSlot: make(chan struct{}, 1), closeDone: make(chan struct{}),
	}
	wrapped.changed = sync.NewCond(&wrapped.stateMutex)
	if t.onConnect != nil {
		t.onConnect(wrapped)
	}
	return wrapped, nil
}

type sessionReadyConnection struct {
	inner      mcp.Connection
	ready      <-chan struct{}
	onRequest  func(jsonrpc.Message) bool
	onSettled  func(jsonrpc.Message)
	onTerminal func(error)

	writeSlot             chan struct{}
	stateMutex            sync.Mutex
	changed               *sync.Cond
	active                bool
	committing            bool
	writeDone             chan struct{}
	writeCompleted        time.Time
	terminalErr           error
	notificationWriteWait time.Duration
	transportCloseWait    time.Duration
	lifetimeCtx           context.Context
	lifetimeCancel        context.CancelFunc
	closeOnce             sync.Once
	closeDone             chan struct{}
	closeBy               time.Time
	closeCompleted        time.Time
	closeTimedOut         bool
	closeErr              error
}

type notificationWriteResult struct {
	err       error
	completed time.Time
}

func (c *sessionReadyConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	readCtx := ctx
	if c.lifetimeCtx != nil {
		// Logical shutdown cancels SDK reads while physical Close is quarantined.
		var cancel context.CancelFunc
		readCtx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(c.lifetimeCtx, cancel)
		defer cancel()
		defer stop()
	}
	select {
	case <-c.ready:
	case <-readCtx.Done():
		return nil, errors.Join(readCtx.Err(), c.connectionTerminalError())
	}
	message, err := c.inner.Read(readCtx)
	if err != nil {
		c.terminate(nil)
		return message, errors.Join(err, c.connectionTerminalError())
	}
	c.stateMutex.Lock()
	for c.active && c.committing {
		c.changed.Wait()
	}
	if !c.active {
		c.stateMutex.Unlock()
		return nil, errors.Join(ErrInstanceClosed, c.connectionTerminalError())
	}
	accepted := c.onRequest == nil || c.onRequest(message)
	if !accepted {
		c.active = false
		c.changed.Broadcast()
	}
	c.stateMutex.Unlock()
	if !accepted {
		c.cancelLifetime()
		if c.onTerminal != nil {
			c.onTerminal(nil)
		}
		return nil, ErrInstanceClosed
	}
	return message, nil
}

func (c *sessionReadyConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if isBoundedNotification(message) {
		return c.writeNotification(ctx, message)
	}
	select {
	case c.writeSlot <- struct{}{}:
	case <-c.lifetimeCtx.Done():
		return errors.Join(ErrInstanceClosed, c.connectionTerminalError())
	}
	defer c.releaseWriteSlot()
	c.stateMutex.Lock()
	if !c.active {
		err := errors.Join(ErrInstanceClosed, c.terminalErr)
		c.stateMutex.Unlock()
		return err
	}
	c.committing = true
	writeDone := make(chan struct{})
	c.writeDone = writeDone
	c.writeCompleted = time.Time{}
	c.stateMutex.Unlock()

	err := c.inner.Write(ctx, message)
	completed := time.Now()
	fatal := writeBreaksConnection(ctx, err)
	c.stateMutex.Lock()
	active := c.active
	if active && !fatal && c.onSettled != nil {
		// Read admission waits behind settlement. After a successful write, the
		// client may reuse an ID before Write returns to this wrapper.
		c.onSettled(message)
	}
	if fatal {
		c.terminalErr = errors.Join(c.terminalErr, err)
		if active {
			c.active = false
		}
	}
	c.committing = false
	c.writeCompleted = completed
	close(writeDone)
	c.writeDone = nil
	c.changed.Broadcast()
	c.stateMutex.Unlock()
	if active && fatal && c.onTerminal != nil {
		c.onTerminal(err)
	}
	return err
}

func (c *sessionReadyConnection) writeNotification(
	ctx context.Context,
	message jsonrpc.Message,
) error {
	wait := c.notificationWriteWait
	if wait <= 0 {
		wait = notificationWriteWait
	}
	deadline := time.Now().Add(wait)
	timer := time.NewTimer(time.Until(deadline))
	timerTransferred := false
	defer func() {
		if !timerTransferred {
			timer.Stop()
		}
	}()
	select {
	case c.writeSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.lifetimeCtx.Done():
		return errors.Join(ErrInstanceClosed, c.connectionTerminalError())
	case <-timer.C:
		if err := c.notificationCancellation(ctx); err != nil {
			return err
		}
		c.terminate(context.DeadlineExceeded)
		c.startTransportClose()
		return context.DeadlineExceeded
	}
	if err := c.notificationCancellation(ctx); err != nil {
		c.releaseWriteSlot()
		return err
	}
	if notificationDeadlineExceeded(time.Now(), deadline) {
		if err := c.notificationCancellation(ctx); err != nil {
			c.releaseWriteSlot()
			return err
		}
		c.terminate(context.DeadlineExceeded)
		c.releaseWriteSlot()
		c.startTransportClose()
		return context.DeadlineExceeded
	}
	c.stateMutex.Lock()
	if !c.active {
		err := errors.Join(ErrInstanceClosed, c.terminalErr)
		c.stateMutex.Unlock()
		c.releaseWriteSlot()
		return err
	}
	writeDone := make(chan struct{})
	c.writeDone = writeDone
	c.writeCompleted = time.Time{}
	c.stateMutex.Unlock()

	done := make(chan notificationWriteResult, 1)
	release := make(chan struct{})
	go func() {
		started := time.Now()
		var err error
		if notificationDeadlineExceeded(started, deadline) {
			err = context.DeadlineExceeded
		} else {
			err = c.inner.Write(ctx, message)
		}
		completed := time.Now()
		c.stateMutex.Lock()
		c.writeCompleted = completed
		c.stateMutex.Unlock()
		done <- notificationWriteResult{err: err, completed: completed}
		<-release
		c.stateMutex.Lock()
		close(writeDone)
		c.writeDone = nil
		c.stateMutex.Unlock()
		c.releaseWriteSlot()
	}()
	var result notificationWriteResult
	select {
	case result = <-done:
	case <-ctx.Done():
		timerTransferred = true
		go c.monitorNotificationQuarantine(ctx, timer, done, release, deadline)
		return ctx.Err()
	case <-c.lifetimeCtx.Done():
		timerTransferred = true
		go c.monitorNotificationQuarantine(ctx, timer, done, release, deadline)
		return errors.Join(ErrInstanceClosed, c.connectionTerminalError())
	case <-timer.C:
		// If completion and the timer became ready together, the physical
		// completion time—not select arbitration—owns the boundary.
		select {
		case result = <-done:
		default:
			if err := c.notificationCancellation(ctx); err != nil {
				close(release)
				return err
			}
			c.terminate(context.DeadlineExceeded)
			c.startTransportClose()
			close(release)
			return context.DeadlineExceeded
		}
	}
	completionErr, expired := c.notificationCompletionError(
		ctx, result.err, result.completed, deadline,
	)
	if expired {
		c.retireNotificationDeadline()
		close(release)
		return completionErr
	}
	if writeBreaksConnection(ctx, completionErr) {
		c.terminate(completionErr)
		c.startTransportClose()
	}
	close(release)
	return completionErr
}

func (c *sessionReadyConnection) monitorNotificationQuarantine(
	ctx context.Context,
	timer *time.Timer,
	done <-chan notificationWriteResult,
	release chan<- struct{},
	deadline time.Time,
) {
	defer close(release)
	var result notificationWriteResult
	select {
	case result = <-done:
		timer.Stop()
	case <-timer.C:
		select {
		case result = <-done:
		default:
			c.retireNotificationDeadline()
			return
		}
	}
	if notificationDeadlineExceeded(result.completed, deadline) {
		c.retireNotificationDeadline()
		return
	}
	if writeBreaksConnection(ctx, result.err) {
		c.terminate(result.err)
		c.startTransportClose()
	}
}

func (c *sessionReadyConnection) retireNotificationDeadline() {
	c.terminate(context.DeadlineExceeded)
	c.startTransportClose()
}

func (c *sessionReadyConnection) notificationCancellation(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.lifetimeCtx.Done():
		return errors.Join(ErrInstanceClosed, c.connectionTerminalError())
	default:
		return nil
	}
}

func (c *sessionReadyConnection) notificationCompletionError(
	ctx context.Context,
	writeErr error,
	completed time.Time,
	deadline time.Time,
) (error, bool) {
	cancellationErr := c.notificationCancellation(ctx)
	if notificationDeadlineExceeded(completed, deadline) {
		if cancellationErr != nil {
			return cancellationErr, true
		}
		return context.DeadlineExceeded, true
	}
	if cancellationErr != nil {
		return errors.Join(cancellationErr, writeErr), false
	}
	return writeErr, false
}

func (c *sessionReadyConnection) releaseWriteSlot() { <-c.writeSlot }

func isBoundedNotification(message jsonrpc.Message) bool {
	request, ok := message.(*jsonrpc.Request)
	return ok && !request.ID.IsValid()
}

func notificationDeadlineExceeded(completed, deadline time.Time) bool {
	return !completed.Before(deadline)
}

func (c *sessionReadyConnection) Close() error {
	c.deactivate(nil, true)
	done, deadline := c.startTransportClose()
	closeErr := c.waitForTransportClose(done, deadline)
	writeErr := c.waitForWrite(deadline)
	c.cancelLifetime()
	return errors.Join(closeErr, writeErr)
}

func (c *sessionReadyConnection) waitForTransportClose(
	done <-chan struct{},
	deadline time.Time,
) error {
	select {
	case <-done:
		return c.transportCloseResult()
	default:
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return c.recordCloseTimeout()
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		return c.transportCloseResult()
	case <-timer.C:
		return c.recordCloseTimeout()
	}
}

func (c *sessionReadyConnection) waitForWrite(deadline time.Time) error {
	c.stateMutex.Lock()
	done := c.writeDone
	c.stateMutex.Unlock()
	if done == nil {
		return c.writeCloseResult(deadline)
	}
	select {
	case <-done:
		return c.writeCloseResult(deadline)
	default:
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		select {
		case <-done:
			return c.writeCloseResult(deadline)
		default:
		}
		return c.recordCloseTimeout()
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		return c.writeCloseResult(deadline)
	case <-timer.C:
		select {
		case <-done:
			return c.writeCloseResult(deadline)
		default:
		}
		return c.recordCloseTimeout()
	}
}

func (c *sessionReadyConnection) writeCloseResult(deadline time.Time) error {
	c.stateMutex.Lock()
	if !c.writeCompleted.IsZero() &&
		notificationDeadlineExceeded(c.writeCompleted, deadline) {
		c.closeTimedOut = true
	}
	timedOut := c.closeTimedOut
	c.stateMutex.Unlock()
	if timedOut {
		return errTransportCloseTimeout
	}
	return nil
}

func (c *sessionReadyConnection) recordCloseTimeout() error {
	c.stateMutex.Lock()
	c.closeTimedOut = true
	c.stateMutex.Unlock()
	return errTransportCloseTimeout
}

func (c *sessionReadyConnection) transportCloseResult() error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	if c.closeCompleted.After(c.closeBy) {
		c.closeTimedOut = true
	}
	if c.closeTimedOut {
		return errors.Join(c.closeErr, errTransportCloseTimeout)
	}
	return c.closeErr
}

func (c *sessionReadyConnection) startTransportClose() (<-chan struct{}, time.Time) {
	c.stateMutex.Lock()
	if c.closeDone == nil {
		c.closeDone = make(chan struct{})
	}
	wait := c.transportCloseWait
	if wait <= 0 {
		wait = notificationWriteWait
	}
	if c.closeBy.IsZero() {
		c.closeBy = time.Now().Add(wait)
	}
	done := c.closeDone
	deadline := c.closeBy
	c.stateMutex.Unlock()
	c.closeOnce.Do(func() {
		go func() {
			err := c.inner.Close()
			completed := time.Now()
			c.stateMutex.Lock()
			c.closeErr = err
			c.closeCompleted = completed
			if completed.After(c.closeBy) {
				c.closeTimedOut = true
			}
			c.stateMutex.Unlock()
			close(done)
		}()
	})
	return done, deadline
}

func (c *sessionReadyConnection) SessionID() string { return c.inner.SessionID() }

func (c *sessionReadyConnection) terminate(err error) {
	c.deactivate(err, true)
}

func (c *sessionReadyConnection) deactivate(err error, cancel bool) {
	c.stateMutex.Lock()
	if err != nil {
		c.terminalErr = errors.Join(c.terminalErr, err)
	}
	wasActive := c.active
	if wasActive {
		c.active = false
		c.changed.Broadcast()
	}
	c.stateMutex.Unlock()
	if cancel {
		c.cancelLifetime()
	}
	if wasActive && c.onTerminal != nil {
		c.onTerminal(err)
	}
}

func (c *sessionReadyConnection) connectionTerminalError() error {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()
	return c.terminalErr
}

func (c *sessionReadyConnection) cancelLifetime() {
	if c.lifetimeCancel != nil {
		c.lifetimeCancel()
	}
}

// The SDK reserves -32005 for a request-local transport rejection. A write is
// nonfatal only when every error leaf is a rejection or matches cancellation.
func writeBreaksConnection(ctx context.Context, err error) bool {
	return err != nil && !writeErrorIsNonfatal(ctx, err)
}

func writeErrorIsNonfatal(ctx context.Context, err error) bool {
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapped.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if child != nil && !writeErrorIsNonfatal(ctx, child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		if child := wrapped.Unwrap(); child != nil {
			return writeErrorIsNonfatal(ctx, child)
		}
	}
	if contextErr := ctx.Err(); contextErr != nil &&
		(errors.Is(err, contextErr) || errors.Is(err, context.Cause(ctx))) {
		return true
	}
	var wire *jsonrpc.Error
	return errors.As(err, &wire) && wire.Code == -32005
}
