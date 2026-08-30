package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// ErrRuntimeTargetBound identifies a target already bound to a terminal
// Connection. An MCP runtime must own the connection it creates.
var ErrRuntimeTargetBound = errors.New("libtmux MCP target is already connection-bound")

// runtimeSetupTimeout bounds selector probes and control registration even
// when an MCP request has no deadline.
const runtimeSetupTimeout = 5 * time.Second

type runtimeState uint8

const (
	runtimeUnbound runtimeState = iota
	runtimeBinding
	runtimeBound
	runtimeTerminal
	runtimeClosed
)

func (s runtimeState) String() string {
	switch s {
	case runtimeUnbound:
		return "unbound"
	case runtimeBinding:
		return "binding"
	case runtimeBound:
		return "bound"
	case runtimeTerminal:
		return "terminal"
	case runtimeClosed:
		return "closed"
	default:
		return fmt.Sprintf("runtimeState(%d)", s)
	}
}

// tmuxRuntime is the sole mutable owner of MCP execution. Its selector is
// frozen at construction. Once a daemon is materialized, every command stays
// on terminal connections opened from the same original session: one retained
// command lane and one owned connection per active wait. Terminal transport
// failures never reconnect or fall back to another daemon.
type tmuxRuntime struct {
	base       tmux.Server
	ctx        context.Context
	onTerminal func(error)
	deps       mcpDependencies

	mutex               sync.Mutex
	state               runtimeState
	probe               chan struct{}
	binding             chan struct{}
	unboundActive       int
	unboundDrained      chan struct{}
	cause               error
	original            tmux.Session
	commandConnection   *tmux.Connection
	observations        sync.WaitGroup
	connectionCloseOnce sync.Once
	connectionsClosed   chan struct{}
	connectionCloseErr  error
}

type runtimeAcquisition struct {
	server   tmux.Server
	runtime  *tmuxRuntime
	unbound  bool
	released atomic.Bool
	once     sync.Once
}

func (a *runtimeAcquisition) release() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		a.released.Store(true)
		if a.unbound {
			a.runtime.releaseUnbound()
		}
	})
}

func (a *runtimeAcquisition) liveUnbound() bool {
	return a != nil && a.unbound && !a.released.Load()
}

func newRuntime(
	ctx context.Context,
	base tmux.Server,
	onTerminal func(error),
) *tmuxRuntime {
	return &tmuxRuntime{
		base:              base,
		ctx:               ctx,
		onTerminal:        onTerminal,
		deps:              defaultMCPDependencies(),
		state:             runtimeUnbound,
		connectionsClosed: make(chan struct{}),
	}
}

// command returns the command lane. An absent selector remains unbound and
// returns the frozen process handle so read-only absence checks keep working.
func (r *tmuxRuntime) command(ctx context.Context) (tmux.Server, error) {
	acquired, err := r.acquireCommand(ctx, false)
	if err != nil {
		return tmux.Server{}, err
	}
	return acquired.server, nil
}

// process returns the exact materialized daemon's process lane. Before a daemon
// exists, it returns the frozen selector.
func (r *tmuxRuntime) process(ctx context.Context) (tmux.Server, error) {
	if acquired := acquiredServerFromContext(ctx); acquired.liveUnbound() {
		return acquired.server, nil
	}
	if _, err := r.command(ctx); err != nil {
		return tmux.Server{}, err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.state == runtimeUnbound {
		return r.base, nil
	}
	if r.state != runtimeBound {
		return tmux.Server{}, r.stateErrorLocked()
	}
	return r.original.Server(), nil
}

// acquire retains an unbound process handle until release. That lease prevents
// first creation from materializing a daemon while an MCP surface can still
// issue work through the previously absent selector.
func (r *tmuxRuntime) acquire(
	ctx context.Context,
) (*runtimeAcquisition, error) {
	return r.acquireCommand(ctx, true)
}

func (r *tmuxRuntime) acquireCommand(
	ctx context.Context,
	leaseUnbound bool,
) (*runtimeAcquisition, error) {
	reprobed := false
	for {
		r.mutex.Lock()
		switch r.state {
		case runtimeBound:
			server := r.commandConnection.Server()
			r.mutex.Unlock()
			return &runtimeAcquisition{server: server}, nil
		case runtimeTerminal, runtimeClosed:
			err := r.stateErrorLocked()
			r.mutex.Unlock()
			return nil, err
		case runtimeBinding:
			ready := r.binding
			r.mutex.Unlock()
			if err := waitRuntime(ctx, ready); err != nil {
				return nil, err
			}
			continue
		case runtimeUnbound:
			if r.probe != nil {
				ready := r.probe
				r.mutex.Unlock()
				if err := waitRuntime(ctx, ready); err != nil {
					return nil, err
				}
				continue
			}
			probe := make(chan struct{})
			r.probe = probe
			r.mutex.Unlock()

			sessions, err := r.probeSessions(ctx)
			if err != nil || len(sessions) == 0 {
				if errors.Is(err, tmux.ErrNoServer) || err == nil {
					acquired, ok := r.finishAbsentProbe(probe, leaseUnbound)
					if ok {
						return acquired, nil
					}
					continue
				}
				r.finishProbe(probe)
				r.observe(err)
				return nil, err
			}
			drained, ok := r.beginBinding(probe)
			if !ok {
				continue
			}
			if drained != nil {
				if err := waitRuntime(ctx, drained); err != nil {
					r.failBinding(tmux.Session{}, nil, err, false)
					return nil, err
				}
			}
			server, reset, err := r.bind(ctx, sessions[0], nil)
			if err != nil {
				if reset && !reprobed && !isContextError(err) {
					reprobed = true
					continue
				}
				return nil, err
			}
			return &runtimeAcquisition{server: server}, nil
		}
	}
}

func (r *tmuxRuntime) finishAbsentProbe(
	probe chan struct{},
	lease bool,
) (*runtimeAcquisition, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.probe != probe {
		return nil, false
	}
	r.probe = nil
	defer close(probe)
	if r.state != runtimeUnbound {
		return nil, false
	}
	if !lease {
		return &runtimeAcquisition{server: r.base}, true
	}
	if r.unboundActive == 0 {
		r.unboundDrained = make(chan struct{})
	}
	r.unboundActive++
	return &runtimeAcquisition{
		server:  r.base,
		runtime: r,
		unbound: true,
	}, true
}

func (r *tmuxRuntime) releaseUnbound() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.unboundActive == 0 {
		return
	}
	r.unboundActive--
	if r.unboundActive == 0 {
		close(r.unboundDrained)
		r.unboundDrained = nil
	}
}

// current returns the handle selected by the most recent successful surface
// acquisition. MCP surface wrappers call command before invoking handlers.
func (r *tmuxRuntime) current() tmux.Server {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.commandConnection != nil {
		return r.commandConnection.Server()
	}
	if r.state == runtimeUnbound {
		return r.base
	}
	return tmux.Server{}
}

// createSession serializes first creation with discovery. The first session
// retains its creating control process as the command connection. Later
// sessions are created on the exact provenance process lane and resolved back
// through the command connection.
func (r *tmuxRuntime) createSession(
	ctx context.Context,
	request tmux.NewSessionRequest,
) (tmux.Session, error) {
	releaseAcquiredServer(ctx)
	reprobed := false
	for {
		r.mutex.Lock()
		switch r.state {
		case runtimeBound:
			r.mutex.Unlock()
			return r.createBoundSession(ctx, request)
		case runtimeTerminal, runtimeClosed:
			err := r.stateErrorLocked()
			r.mutex.Unlock()
			return tmux.Session{}, err
		case runtimeBinding:
			ready := r.binding
			r.mutex.Unlock()
			if err := waitRuntime(ctx, ready); err != nil {
				return tmux.Session{}, err
			}
			continue
		case runtimeUnbound:
			if r.probe != nil {
				ready := r.probe
				r.mutex.Unlock()
				if err := waitRuntime(ctx, ready); err != nil {
					return tmux.Session{}, err
				}
				continue
			}
			r.state = runtimeBinding
			r.binding = make(chan struct{})
			drained := r.unboundDrained
			r.mutex.Unlock()

			if drained != nil {
				if err := waitRuntime(ctx, drained); err != nil {
					r.failBinding(tmux.Session{}, nil, err, true)
					return tmux.Session{}, err
				}
			}
			sessions, err := r.probeSessions(ctx)
			switch {
			case err == nil && len(sessions) > 0:
				_, reset, bindErr := r.bind(ctx, sessions[0], nil)
				if reset && !reprobed && !isContextError(bindErr) {
					reprobed = true
					continue
				}
				if bindErr != nil {
					return tmux.Session{}, bindErr
				}
				continue
			case err != nil && !errors.Is(err, tmux.ErrNoServer):
				r.failBinding(tmux.Session{}, nil, err, true)
				return tmux.Session{}, err
			default:
				return r.bootstrap(ctx, request)
			}
		}
	}
}

func (r *tmuxRuntime) createBoundSession(
	ctx context.Context,
	request tmux.NewSessionRequest,
) (tmux.Session, error) {
	process, err := r.process(ctx)
	if err != nil {
		return tmux.Session{}, err
	}
	created, err := process.NewSession(ctx, request)
	if err != nil {
		r.observe(err)
		return created, err
	}
	command, err := r.command(ctx)
	if err != nil {
		return created, err
	}
	bound, err := command.Session(ctx, created.ID())
	r.observe(err)
	if err != nil {
		return created, err
	}
	return bound, nil
}

func (r *tmuxRuntime) bootstrap(
	ctx context.Context,
	request tmux.NewSessionRequest,
) (tmux.Session, error) {
	operationCtx, cancel := r.operationContext(ctx)
	defer cancel()
	original, commandConnection, err := r.deps.newSessionConnection(
		operationCtx, r.base, request,
	)
	if err != nil {
		r.failBinding(original, commandConnection, err, true)
		return original, err
	}
	if err := r.finishBinding(original, commandConnection); err != nil {
		return original, err
	}
	return commandConnection.Session(), nil
}

func (r *tmuxRuntime) bind(
	ctx context.Context,
	original tmux.Session,
	commandConnection *tmux.Connection,
) (tmux.Server, bool, error) {
	operationCtx, cancel := r.operationContext(ctx)
	defer cancel()
	var err error
	if commandConnection == nil {
		commandConnection, err = original.OpenControl(operationCtx, tmux.ConnectionOptions{})
		if err != nil {
			reset := r.failBinding(original, nil, err, false)
			return tmux.Server{}, reset, err
		}
	}
	if err := r.finishBinding(original, commandConnection); err != nil {
		return tmux.Server{}, false, err
	}
	return commandConnection.Server(), false, nil
}

func (r *tmuxRuntime) probeSessions(ctx context.Context) ([]tmux.Session, error) {
	operationCtx, cancel := r.operationContext(ctx)
	defer cancel()
	return r.deps.probeSessions(operationCtx, r.base)
}

func (r *tmuxRuntime) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, runtimeSetupTimeout)
	operationCtx, cancel := context.WithCancel(timeoutCtx)
	stop := context.AfterFunc(r.ctx, cancel)
	return operationCtx, func() {
		stop()
		cancel()
		cancelTimeout()
	}
}

func (r *tmuxRuntime) beginBinding(probe chan struct{}) (<-chan struct{}, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.probe == probe {
		r.probe = nil
		close(probe)
	}
	if r.state != runtimeUnbound {
		return nil, false
	}
	r.state = runtimeBinding
	r.binding = make(chan struct{})
	return r.unboundDrained, true
}

func (r *tmuxRuntime) finishProbe(probe chan struct{}) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.probe == probe {
		r.probe = nil
		close(probe)
	}
}

func (r *tmuxRuntime) finishBinding(
	original tmux.Session,
	commandConnection *tmux.Connection,
) error {
	r.mutex.Lock()
	if r.state != runtimeBinding {
		err := r.stateErrorLocked()
		r.finishBindingSignalLocked()
		r.mutex.Unlock()
		_ = closeRuntimeConnections(commandConnection)
		return err
	}
	r.original = original
	r.commandConnection = commandConnection
	r.state = runtimeBound
	r.finishBindingSignalLocked()
	r.mutex.Unlock()
	return nil
}

func (r *tmuxRuntime) failBinding(
	original tmux.Session,
	commandConnection *tmux.Connection,
	err error,
	creation bool,
) bool {
	r.mutex.Lock()
	if r.state != runtimeBinding {
		r.finishBindingSignalLocked()
		r.mutex.Unlock()
		_ = closeRuntimeConnections(commandConnection)
		return false
	}
	retryable := commandConnection == nil &&
		!isIntrinsicTerminalRuntimeError(err) &&
		(!creation || original.ID() == "")
	if retryable {
		r.state = runtimeUnbound
		r.cause = nil
		r.finishBindingSignalLocked()
		r.mutex.Unlock()
		return true
	}
	r.original = original
	r.commandConnection = commandConnection
	r.cause = err
	r.state = runtimeTerminal
	r.finishBindingSignalLocked()
	r.mutex.Unlock()
	r.cancelOwner(err)
	r.startConnectionClose(r.ownedConnections(commandConnection))
	return false
}

func (r *tmuxRuntime) finishBindingSignalLocked() {
	if r.binding != nil {
		close(r.binding)
		r.binding = nil
	}
}

func (r *tmuxRuntime) stateErrorLocked() error {
	switch r.state {
	case runtimeClosed:
		if r.cause != nil {
			return r.cause
		}
		return ErrInstanceClosed
	case runtimeTerminal:
		if r.cause != nil {
			return r.cause
		}
		return tmux.ErrControlClosed
	case runtimeUnbound, runtimeBinding, runtimeBound:
		return fmt.Errorf("libtmux MCP runtime is %s", r.state)
	default:
		return fmt.Errorf("libtmux MCP runtime is %s", r.state)
	}
}

func waitRuntime(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
