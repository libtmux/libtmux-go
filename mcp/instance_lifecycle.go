package mcp

import (
	"context"
	"errors"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
		clear(i.responses)
		if i.drainTimer != nil {
			i.drainTimer.Stop()
			i.drainTimer = nil
		}
		i.mutex.Unlock()
		i.cancel()
		go i.shutdown()
	})
}

// terminal preserves the first classified tmux failure. It gives admitted
// responses the drain limit before forced session closure begins.
func (i *Instance) terminal(err error) {
	if err == nil {
		return
	}
	i.mutex.Lock()
	if i.terminalErr != nil {
		i.mutex.Unlock()
		return
	}
	i.terminalErr = err
	i.closing = true
	awaitResponse := i.responses.count() > 0
	scopes := make([]*sessionScope, 0, len(i.sessions))
	for _, session := range i.sessions {
		scopes = append(scopes, session.scope)
	}
	if awaitResponse {
		i.drainTimer = time.AfterFunc(i.drainWait, i.startClose)
	}
	i.mutex.Unlock()
	for _, scope := range scopes {
		scope.terminate(err)
	}
	if !awaitResponse {
		i.startClose()
	}
}

func (i *Instance) terminalCause() error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.terminalErr
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
	failures = append(failures, i.runtime.Close())
	failures = append(failures, i.terminalCause())
	if i.audit != nil {
		failures = append(failures, i.audit.Close())
	}
	i.closeErr = errors.Join(failures...)
	close(i.closeDone)
}

func (s *ServerSession) await() {
	s.finish(s.sdk.Wait())
}

// Close stops scoped work, closes the transport, and joins SDK session cleanup.
func (s *ServerSession) Close() error {
	if s == nil {
		return nil
	}
	s.scope.stop()
	var transportErr error
	if s.connection != nil {
		transportErr = s.connection.Close()
	}
	err := s.sdk.Close()
	<-s.done
	return errors.Join(transportErr, err)
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
		s.waitErr = errors.Join(err, s.scope.terminalCause())
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
