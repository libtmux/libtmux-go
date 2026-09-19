package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/libtmux/libtmux-go/tmux"
)

// observe makes errors at every MCP surface terminal without retrying work
// that may already have acted. The daemon this runtime was using is lost or
// untrusted, never fatal to the process: the call that hit err already
// reports it, and the next top-level acquisition heals this state back to
// unbound and starts fresh.
func (r *tmuxRuntime) observe(err error) {
	if err == nil || !r.isTerminalError(err) {
		return
	}
	r.mutex.Lock()
	if r.state == runtimeTerminal || r.state == runtimeClosed {
		r.mutex.Unlock()
		return
	}
	r.cause = err
	r.state = runtimeTerminal
	lost := r.commandConnection
	r.original = tmux.Session{}
	r.commandConnection = nil
	r.finishBindingSignalLocked()
	r.mutex.Unlock()
	r.closeLostConnection(lost)
}

// healTerminalLocked discards a runtime lost to a terminal error and returns
// it to unbound so the next acquisition or session creation retries as if
// starting fresh. Must hold r.mutex; returns the stale connection this
// runtime no longer trusts, for the caller to close outside the lock.
func (r *tmuxRuntime) healTerminalLocked() *tmux.Connection {
	lost := r.commandConnection
	r.original = tmux.Session{}
	r.commandConnection = nil
	r.cause = nil
	r.state = runtimeUnbound
	return lost
}

// closeLostConnection discards a connection this runtime no longer trusts,
// without touching the once-only close [tmuxRuntime.Close] performs: that one
// must wait for whichever connection is current when the process actually
// shuts down, not the first one this runtime ever lost.
func (r *tmuxRuntime) closeLostConnection(connection *tmux.Connection) {
	if connection == nil {
		return
	}
	go func() { _ = connection.Close() }()
}

// isTerminalError classifies target absence only after this runtime has bound
// to a concrete daemon. Before binding, absence is the state that permits the
// first create_session request.
func (r *tmuxRuntime) isTerminalError(err error) bool {
	if isIntrinsicTerminalRuntimeError(err) {
		return true
	}
	if !errors.Is(err, tmux.ErrNoServer) {
		return false
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.state == runtimeBound
}

func isIntrinsicTerminalRuntimeError(err error) bool {
	return errors.Is(err, tmux.ErrDaemonReplaced) ||
		errors.Is(err, tmux.ErrControlClosed) ||
		isIndeterminateRuntimeError(err)
}

func isIndeterminateRuntimeError(err error) bool {
	return errors.Is(err, tmux.ErrOutcomeUnknown) &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded)
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// Close closes owned transport and never kills a tmux session or server.
func (r *tmuxRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.mutex.Lock()
	if r.probe != nil {
		close(r.probe)
		r.probe = nil
	}
	r.state = runtimeClosed
	if r.cause == nil {
		r.cause = ErrInstanceClosed
	}
	commandConnection := r.commandConnection
	connections := r.ownedConnectionsLocked(commandConnection)
	r.finishBindingSignalLocked()
	r.mutex.Unlock()
	r.startConnectionClose(connections)
	<-r.connectionsClosed
	return r.connectionCloseErr
}

func (r *tmuxRuntime) startConnectionClose(connections []*tmux.Connection) {
	r.connectionCloseOnce.Do(func() {
		go func() {
			r.observations.Wait()
			r.connectionCloseErr = closeRuntimeConnections(connections...)
			close(r.connectionsClosed)
		}()
	})
}

// ownedConnectionsLocked snapshots the command connection before close starts.
func (r *tmuxRuntime) ownedConnectionsLocked(
	commandConnection *tmux.Connection,
) []*tmux.Connection {
	if commandConnection == nil {
		return nil
	}
	return []*tmux.Connection{commandConnection}
}

func closeRuntimeConnections(connections ...*tmux.Connection) error {
	errs := make([]error, len(connections))
	var closing sync.WaitGroup
	for index, connection := range connections {
		if connection == nil {
			continue
		}
		closing.Add(1)
		go func() {
			defer closing.Done()
			errs[index] = connection.Close()
		}()
	}
	closing.Wait()
	return errors.Join(errs...)
}
