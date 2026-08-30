package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/libtmux/libtmux-go/tmux"
)

// observe makes errors at every MCP surface terminal without retrying work that
// may already have acted.
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
	commandConnection := r.commandConnection
	connections := r.ownedConnectionsLocked(commandConnection)
	r.finishBindingSignalLocked()
	r.mutex.Unlock()
	r.cancelOwner(err)
	r.startConnectionClose(connections)
}

func (r *tmuxRuntime) cancelOwner(err error) {
	if r.onTerminal != nil {
		r.onTerminal(err)
	}
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

func (r *tmuxRuntime) ownedConnections(
	commandConnection *tmux.Connection,
) []*tmux.Connection {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.ownedConnectionsLocked(commandConnection)
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
