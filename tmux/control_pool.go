package tmux

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
)

// ControlPoolRequest configures the control-mode connections a pool owns.
type ControlPoolRequest struct {
	// Connections is how many control-mode connections the pool owns. Zero
	// opens one. A connection carries one command at a time.
	//
	// A command that blocks inside tmux, including waits and prompts, holds its
	// connection. If all connections block, later commands wait for a free one.
	//
	// Each connection is an attached tmux client that affects list-clients,
	// destroy-unattached, and session-attached. Pools disable pane output because
	// they expose no notification stream.
	Connections int
}

// OpenControlPool returns a [Server] carrying a control-mode transport,
// together with the [ControlPool] that owns it.
//
// Closing the pool does not invalidate anything derived from the returned
// handle. Under the default fallback policy, those records start a subprocess
// and report [WarningControlPoolClosed]. [EngineFallbackReject] instead returns
// [EngineFallbackError].
//
// Every connection attaches to session because tmux has no unattached control
// client. Killing the session closes its connections.
//
// The returned handle is the one to derive records from. A record taken from
// the original handle still starts a process per command. Use its WithEngine
// method to select the pool without a lookup.
//
// The pool, not the copyable [Server], owns the connections and must be closed.
//
// Construction probes and caches the tmux version, opens attached client
// processes, and registers each connection. Interactive attachment, version
// probing, and exact-byte reads still use their documented transports;
// [Pane.CaptureToFile] stays on the pool.
//
// Failure closes anything it already opened. A caller that receives an error
// receives no pool to close.
func (s Server) OpenControlPool(
	ctx context.Context,
	session Session,
	request ControlPoolRequest,
) (Server, Session, *ControlPool, error) {
	if request.Connections < 0 {
		return Server{}, Session{}, nil, invalidServerCommandRequest(
			"connect",
			"Connections",
			strconv.Itoa(request.Connections),
			"must not be negative",
		)
	}
	count := max(request.Connections, 1)

	if _, err := s.Version(ctx); err != nil {
		return Server{}, Session{}, nil, err
	}

	clients := make([]*ControlClient, 0, count)
	free := make(chan *ControlClient, count)
	for range count {
		client, err := s.openControl(ctx, session, controlNotificationsDiscarded)
		if err != nil {
			return Server{}, Session{}, nil, errors.Join(err, closeControlClients(clients))
		}
		clients = append(clients, client)
		free <- client
	}
	pool := &ControlPool{
		clients: clients,
		free:    free,
		stopped: make(chan struct{}),
		drained: make(chan struct{}),
		live:    count,
	}
	s.connectionState().coordination().pools.Add(1)
	connected := s.WithEngine(pool.Engine())
	// The session is handed back on the connected handle rather than as it
	// arrived. A caller writing "server, session, pool, err := ..." shadows the
	// session they passed in, so the one they go on to use is the one that
	// carries the connections.
	pool.session = session.withServer(connected)
	return connected, pool.session, pool, nil
}

func closeControlClients(clients []*ControlClient) error {
	failures := make([]error, 0, len(clients))
	for _, client := range clients {
		failures = append(failures, client.Close())
	}
	return errors.Join(failures...)
}

// ControlPool owns the control-mode connections behind a connected [Server].
// Close it when done. After closure, the default policy uses subprocesses and
// reports [WarningControlPoolClosed]; [EngineFallbackReject] refuses fallback.
//
// The pool gives each command an exclusive connection, bounding concurrency by
// the configured connection count.
//
// A pool exposes no notification stream and disables pane-output notifications.
// Use [Server.OpenControl] to watch tmux changes.
//
// Every method is safe for concurrent use.
type ControlPool struct {
	session Session
	clients []*ControlClient

	free    chan *ControlClient
	stopped chan struct{}
	drained chan struct{}

	stopOnce sync.Once

	mu      sync.Mutex
	live    int
	failure error
}

// Engine returns the pool's [Engine] for use with [Server.WithEngine]. Records
// obtained before selecting it retain their original server handle.
func (p *ControlPool) Engine() Engine { return poolEngine{pool: p} }

// Session returns the attached session on the connected handle.
func (p *ControlPool) Session() Session { return p.session }

// Connections reports how many connections have not been retired after a
// connection failure. It starts at the requested count, or one when
// [ControlPoolRequest.Connections] is zero. Closing the pool does not change it.
func (p *ControlPool) Connections() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.live
}

// CloseContext stops every connection and waits within ctx. It is idempotent
// and retryable for the reason [ControlClient.CloseContext] is: a context that
// ends while waiting abandons the wait rather than the shutdown, so a later
// call resumes waiting for the same processes.
func (p *ControlPool) CloseContext(ctx context.Context) error {
	p.stop()
	failures := make([]error, 0, len(p.clients))
	for _, client := range p.clients {
		failures = append(failures, client.CloseContext(ctx))
	}
	return errors.Join(failures...)
}

// Close stops every connection on a bounded context of its own. It is safe to
// call concurrently and more than once, so it suits defer.
func (p *ControlPool) Close() error {
	p.stop()
	return closeControlClients(p.clients)
}

func (p *ControlPool) stop() {
	p.stopOnce.Do(func() {
		close(p.stopped)
		p.session.server.connectionState().coordination().pools.Add(-1)
	})
}

// run carries one command on an exclusive connection. It must forward
// commandList so a list is not reinterpreted as arguments to its first command.
func (p *ControlPool) run(
	ctx context.Context,
	arguments []string,
	commandList bool,
) (CommandResult, error) {
	client, err := p.acquire(ctx)
	if err != nil {
		return CommandResult{Command: slices.Clone(arguments), ExitCode: -1}, err
	}
	results, err := client.cmd(ctx, commandList, arguments...)
	p.release(client, err)
	if err != nil {
		return CommandResult{Command: slices.Clone(arguments), ExitCode: -1}, err
	}
	return controlCommandResults(arguments, results), nil
}

func (p *ControlPool) acquire(ctx context.Context) (*ControlClient, error) {
	select {
	case client := <-p.free:
		return client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.stopped:
		return nil, ErrControlClosed
	case <-p.drained:
		return nil, p.drainError()
	}
}

// release returns a connection to the pool, or retires it when the failure
// proves the connection itself is gone.
//
// Retired connections are not replaced or retried because delivery is ambiguous
// and retrying could repeat a mutation.
func (p *ControlPool) release(client *ControlClient, err error) {
	if err == nil || connectionSurvives(err) {
		p.free <- client
		return
	}
	p.mu.Lock()
	if p.failure == nil {
		p.failure = err
	}
	p.live--
	drained := p.live == 0
	p.mu.Unlock()
	_ = client.Close()
	if drained {
		close(p.drained)
	}
}

// connectionSurvives reports whether a failed command left its connection
// usable. A rejected request never reached the wire, and a canceled command is
// drained by the client before it writes another, so neither says anything
// about the connection.
func connectionSurvives(err error) bool {
	if errors.Is(err, ErrInvalidRequest) {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (p *ControlPool) drainError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure != nil {
		return p.failure
	}
	return ErrControlClosed
}

type poolEngine struct {
	pool *ControlPool
}

// Supports accepts server commands while the pool is open. After closure it
// declines them, leaving the handle's fallback policy to choose the result.
func (e poolEngine) Supports(kind CommandKind) bool {
	if kind != CommandServer {
		return false
	}
	select {
	case <-e.pool.stopped:
		return false
	default:
		return true
	}
}

// InstanceBound reports that the pool's connections cannot reach a replacement
// server, and stops claiming that once the pool is closed and its commands go
// back to starting tmux processes.
func (e poolEngine) InstanceBound() bool { return e.Supports(CommandServer) }

func (e poolEngine) declined(kind CommandKind) (Warning, bool) {
	if kind != CommandServer {
		return Warning{}, false
	}
	select {
	case <-e.pool.stopped:
		return newControlPoolClosedWarning(kind), true
	default:
		return Warning{}, false
	}
}

// Run executes one tmux command on one of the pool's connections.
func (e poolEngine) Run(
	ctx context.Context,
	_ CommandKind,
	request CommandRequest,
) (CommandResult, error) {
	return e.pool.run(ctx, request.Arguments, request.CommandList)
}

// String implements fmt.Stringer.
func (e poolEngine) String() string {
	return "control-pool(" + strconv.Itoa(len(e.pool.clients)) + ")"
}
