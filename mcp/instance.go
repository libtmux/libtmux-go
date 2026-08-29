package mcp

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrInstanceClosed identifies work refused after instance shutdown starts.
var ErrInstanceClosed = errors.New("libtmux MCP instance is closed")

// Instance owns the SDK server, all connected client sessions, and every
// resource allocated on their behalf. The SDK server is composed so Connect
// and Run cannot bypass lifecycle tracking.
type Instance struct {
	server *sdk.Server
	tools  *tools
	audit  io.Closer
	ctx    context.Context
	cancel context.CancelFunc

	mutex      sync.Mutex
	closing    bool
	sessions   map[*sdk.ServerSession]*ServerSession
	connecting sync.WaitGroup
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
}

func newInstance() *Instance {
	ctx, cancel := context.WithCancel(context.Background())
	return &Instance{
		sessions:  map[*sdk.ServerSession]*ServerSession{},
		closeDone: make(chan struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// ServerSession owns one SDK session and the resources scoped to its client.
type ServerSession struct {
	instance *Instance
	sdk      *sdk.ServerSession
	scope    *sessionScope

	finishOnce sync.Once
	done       chan struct{}
	waitErr    error
}

// Connect starts and tracks one client session. Incoming messages remain
// gated until the SDK session has an installed scope.
func (i *Instance) Connect(
	ctx context.Context,
	transport sdk.Transport,
	options *sdk.ServerSessionOptions,
) (*ServerSession, error) {
	if i == nil {
		return nil, ErrInstanceClosed
	}
	i.mutex.Lock()
	if i.closing {
		i.mutex.Unlock()
		return nil, ErrInstanceClosed
	}
	i.connecting.Add(1)
	i.mutex.Unlock()
	defer i.connecting.Done()

	ready := make(chan struct{})
	scope := newSessionScope(i.ctx)
	connectCtx, cancelConnect := context.WithCancel(ctx)
	stopConnect := context.AfterFunc(i.ctx, cancelConnect)
	defer cancelConnect()
	defer stopConnect()
	if i.ctx.Err() != nil {
		cancelConnect()
	}
	connected, err := i.server.Connect(connectCtx, sessionReadyTransport{
		inner:      handshakeOrderedTransport{inner: transport},
		ready:      ready,
		onTerminal: scope.stop,
	}, options)
	if err != nil {
		close(ready)
		scope.close(i.tools.watchers)
		return nil, err
	}
	session := &ServerSession{
		instance: i,
		sdk:      connected,
		scope:    scope,
		done:     make(chan struct{}),
	}

	i.mutex.Lock()
	if i.closing {
		i.mutex.Unlock()
		close(ready)
		scope.stop()
		_ = connected.Close()
		scope.close(i.tools.watchers)
		return nil, ErrInstanceClosed
	}
	i.sessions[connected] = session
	i.mutex.Unlock()
	close(ready)
	go session.await()
	return session, nil
}

// Run serves one tracked session until the client, context, or instance ends.
func (i *Instance) Run(ctx context.Context, transport sdk.Transport) error {
	session, err := i.Connect(ctx, transport, nil)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case <-session.done:
		return session.waitErr
	}
}

// Close starts shutdown and waits on a bounded internal context.
func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return i.CloseContext(ctx)
}

// CloseContext starts shutdown unconditionally. ctx bounds only this call's
// wait; a later call can resume waiting for the same shutdown.
func (i *Instance) CloseContext(ctx context.Context) error {
	if i == nil {
		return nil
	}
	i.startClose()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-i.closeDone:
		return i.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Instance) startClose() {
	i.closeOnce.Do(func() {
		i.mutex.Lock()
		i.closing = true
		i.mutex.Unlock()
		i.cancel()
		go i.shutdown()
	})
}

func (i *Instance) shutdown() {
	i.connecting.Wait()
	i.mutex.Lock()
	sessions := make([]*ServerSession, 0, len(i.sessions))
	for _, session := range i.sessions {
		sessions = append(sessions, session)
	}
	i.mutex.Unlock()

	// Cancel every session before asking the SDK to join its handlers.
	for _, session := range sessions {
		session.scope.stop()
	}
	failures := make([]error, len(sessions), len(sessions)+1)
	var closing sync.WaitGroup
	for index, session := range sessions {
		closing.Add(1)
		go func() {
			defer closing.Done()
			failures[index] = session.Close()
		}()
	}
	closing.Wait()
	i.tools.watchers.close()
	if i.audit != nil {
		failures = append(failures, i.audit.Close())
	}
	i.closeErr = errors.Join(failures...)
	close(i.closeDone)
}

func (s *ServerSession) await() {
	s.finish(s.sdk.Wait())
}

// Close closes the SDK connection and joins session cleanup.
func (s *ServerSession) Close() error {
	if s == nil {
		return nil
	}
	s.scope.stop()
	err := s.sdk.Close()
	<-s.done
	return err
}

// Wait waits for client termination and session cleanup.
func (s *ServerSession) Wait() error {
	if s == nil {
		return nil
	}
	<-s.done
	return s.waitErr
}

func (s *ServerSession) finish(err error) {
	s.finishOnce.Do(func() {
		s.waitErr = err
		s.instance.dropSession(s)
		close(s.done)
	})
}

func (i *Instance) dropSession(session *ServerSession) {
	i.mutex.Lock()
	delete(i.sessions, session.sdk)
	i.mutex.Unlock()
	session.scope.close(i.tools.watchers)
}

func (i *Instance) scope(session *sdk.ServerSession) (*sessionScope, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.closing {
		return nil, ErrInstanceClosed
	}
	tracked := i.sessions[session]
	if tracked == nil {
		return nil, ErrInstanceClosed
	}
	return tracked.scope, nil
}

func (i *Instance) scoped(next sdk.MethodHandler) sdk.MethodHandler {
	return func(
		ctx context.Context,
		method string,
		request sdk.Request,
	) (sdk.Result, error) {
		session, ok := request.GetSession().(*sdk.ServerSession)
		if !ok {
			return nil, ErrInstanceClosed
		}
		scope, err := i.scope(session)
		if err != nil {
			return nil, err
		}
		requestCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(scope.ctx, cancel)
		defer cancel()
		defer stop()
		if scope.ctx.Err() != nil {
			cancel()
			return nil, ErrInstanceClosed
		}
		return next(requestCtx, method, request)
	}
}
