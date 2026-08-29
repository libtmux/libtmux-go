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

// OpenControlPool opens a control-mode pool attached to session and returns a
// [Server] and [Session] using it. The caller owns and must close the returned
// [ControlPool]; killing session closes its connections.
//
// Derive records from the returned handle or select its engine explicitly;
// existing records retain their old transport. After the pool closes,
// [CommandServer] operations fall back to subprocesses and report
// [WarningControlPoolClosed], or return [EngineFallbackError] when fallback is
// rejected.
//
// Interactive attachment, version loading, and exact-byte reads retain their
// documented transports; [Pane.CaptureToFile] remains pool-eligible. Partial
// construction closes opened connections and returns a nil pool.
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
	lanes, err := s.openControlLanePool(ctx, session, request.Connections)
	if err != nil {
		return Server{}, Session{}, nil, err
	}
	pool := &ControlPool{pool: lanes}
	connected := s.WithEngine(pool.Engine())
	if session.server.daemon != nil {
		connected = connected.withDaemon(*session.server.daemon)
	}
	// The session is handed back on the connected handle rather than as it
	// arrived. A caller writing "server, session, pool, err := ..." shadows the
	// session they passed in, so the one they go on to use is the one that
	// carries the connections.
	pool.session = session.withServer(connected)
	return connected, pool.session, pool, nil
}

func (s Server) openControlLanePool(
	ctx context.Context,
	session Session,
	connections int,
) (*controlLanePool, error) {
	count := max(connections, 1)

	if _, err := s.Version(ctx); err != nil {
		return nil, err
	}

	clients := make([]*ControlClient, 0, count)
	for range count {
		client, err := s.openControl(ctx, session, controlNotificationsDiscarded)
		if err != nil {
			return nil, errors.Join(err, closeControlClients(clients))
		}
		clients = append(clients, client)
	}
	return newControlLanePool(s, clients), nil
}

func newControlLanePool(s Server, clients []*ControlClient) *controlLanePool {
	free := make(chan *ControlClient, len(clients))
	for _, client := range clients {
		free <- client
	}
	coordination := s.connectionState().coordination()
	pool := &controlLanePool{
		clients:      clients,
		free:         free,
		stopped:      make(chan struct{}),
		drained:      make(chan struct{}),
		coordination: coordination,
		live:         len(clients),
	}
	coordination.pools.Add(1)
	return pool
}

func closeControlClients(clients []*ControlClient) error {
	failures := make([]error, 0, len(clients))
	for _, client := range clients {
		failures = append(failures, client.Close())
	}
	return errors.Join(failures...)
}

// ControlPool owns the control-mode connections behind a connected [Server].
// It leases one connection per command, bounding concurrency. It suppresses
// pane output and exposes no notification stream; use [Server.OpenControl] to
// watch tmux changes. On tmux 3.6 or later its clients move to another session
// when their initial session is destroyed and another exists. Close it when
// done. Every method is safe for concurrent use.
type ControlPool struct {
	session Session
	pool    *controlLanePool
}

// Engine returns the pool's [Engine] for use with [Server.WithEngine]. Records
// obtained before selecting it retain their original server handle.
func (p *ControlPool) Engine() Engine { return poolEngine{pool: p.pool} }

// Session returns the attached session on the connected handle.
func (p *ControlPool) Session() Session { return p.session }

// Connections reports how many connections have not been retired after a
// connection failure. It starts at the requested count, or one when
// [ControlPoolRequest.Connections] is zero. Closing the pool does not change it.
func (p *ControlPool) Connections() int { return p.pool.connections() }

// CloseContext stops every connection and waits within ctx. It is idempotent
// and retryable for the reason [ControlClient.CloseContext] is: a context that
// ends while waiting abandons the wait rather than the shutdown, so a later
// call resumes waiting for the same processes.
func (p *ControlPool) CloseContext(ctx context.Context) error {
	return p.pool.closeContext(ctx)
}

// Close stops every connection on a bounded context of its own. It is safe to
// call concurrently and more than once, so it suits defer.
func (p *ControlPool) Close() error { return p.pool.close() }

type controlLanePool struct {
	clients []*ControlClient

	free    chan *ControlClient
	stopped chan struct{}
	drained chan struct{}

	coordination *serverShared
	stopOnce     sync.Once

	mu      sync.Mutex
	live    int
	failure error
}

func (p *controlLanePool) connections() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.live
}

func (p *controlLanePool) closeContext(ctx context.Context) error {
	p.stop()
	failures := make([]error, 0, len(p.clients))
	for _, client := range p.clients {
		failures = append(failures, client.CloseContext(ctx))
	}
	return errors.Join(failures...)
}

func (p *controlLanePool) close() error {
	p.stop()
	return closeControlClients(p.clients)
}

func (p *controlLanePool) stop() {
	p.stopOnce.Do(func() {
		close(p.stopped)
		p.coordination.pools.Add(-1)
	})
}

// run carries one command on an exclusive connection. It must forward
// commandList so a list is not reinterpreted as arguments to its first command.
func (p *controlLanePool) run(
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

func (p *controlLanePool) acquire(ctx context.Context) (*ControlClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-p.stopped:
		return nil, ErrControlClosed
	default:
	}
	select {
	case <-p.drained:
		return nil, p.drainError()
	default:
	}
	select {
	case client := <-p.free:
		if err := ctx.Err(); err != nil {
			p.free <- client
			return nil, err
		}
		select {
		case <-p.stopped:
			p.free <- client
			return nil, ErrControlClosed
		default:
		}
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
func (p *controlLanePool) release(client *ControlClient, err error) {
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

func (p *controlLanePool) drainError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure != nil {
		return p.failure
	}
	return ErrControlClosed
}

type poolEngine struct {
	pool *controlLanePool
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
