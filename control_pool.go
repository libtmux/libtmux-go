package tmux

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
)

// ControlPoolRequest configures the control-mode connections a pool owns. Its
// zero value opens one, which is what a caller writes when the only goal is to
// stop starting a tmux process per command.
type ControlPoolRequest struct {
	// Connections is how many control-mode connections the pool owns. Zero
	// opens one, which is all a program that issues commands from a single
	// goroutine can use: ControlClient.Cmd serializes, so one connection
	// carries one tmux command at a time.
	//
	// A tmux command that blocks inside tmux holds its connection for as long
	// as it blocks: a wait on a tmux channel, and the prompting commands,
	// occupy one until they are answered. Enough of them at once
	// leaves nothing to carry the next command, which then waits for a
	// connection rather than for tmux and reports whatever the caller's
	// context reports. Run those on a handle with no engine instead.
	//
	// Raise it only for concurrent callers, and treat the number as a cost
	// rather than a tuning dial. Each connection is an attached tmux process
	// that appears in list-clients output, participates in
	// destroy-unattached and session-attached behavior, and receives its own
	// copy of every notification tmux broadcasts to the session.
	Connections int
}

// OpenControlPool returns a [Server] carrying a control-mode transport,
// together with the [ControlPool] that owns it.
//
// It is [Server.OpenControl] and [Server.WithEngine] in one call, and unlike
// them it can hold more than one connection: [ControlClient.Cmd] serializes, so
// a single connection carries one tmux command at a time and concurrent callers
// queue behind each other.
//
// Closing the pool does not invalidate anything derived from the returned
// handle. Those records go back to starting a tmux process per command and
// report [WarningControlPoolClosed] through [ServerOptions.WarningHandler], so
// a function may use a pool internally and return what it built.
//
// session is attached by every connection, because tmux has no unattached
// control client. Its lifetime governs the pool's: killing it closes the
// connections attached to it. Passing one the caller already owns is deliberate
// rather than convenient, since a pool that invented its own session would
// leave one behind that nobody asked for.
//
// The returned handle is the one to derive records from. A record taken from
// the original handle keeps starting a process; [Pane.WithServer] and its
// counterparts move one across without a lookup.
//
// The transport is otherwise four separate things a caller has to know: open a
// control client, adapt it to an [Engine], select that engine on a handle copy,
// and look up again every record obtained before the selection, because a
// record carries the handle that made it and an older record keeps starting
// tmux processes without reporting anything. Building the connection with the
// handle retires the last of those. The returned Server is the first handle the
// program holds, so no record can predate its engine.
//
// The returned handle is an ordinary immutable [Server]: it copies freely,
// [Server.WithStrictErrors] keeps the transport, and every session, window, and
// pane derived from it carries the transport too. The lifetime lives in the
// second return value rather than in a Close method on the handle, because a
// handle is embedded in every record it produces and copied into every one of
// them, so no copy could own the shutdown of the others.
//
// Construction starts tmux processes: it lists or creates the session, opens
// each connection, and probes the tmux version once so that a later
// version-gated operation finds the answer cached rather than starting a
// process for it. Afterwards every command the connection can carry runs over
// it. The exceptions are the ones [Server.WithEngine] documents, since routing
// is unchanged: interactive attachment, the tmux -V probe, and the reads whose
// contract is tmux's exact stdout bytes, which are [Pane.Capture],
// [Pane.CaptureBytes], and [Server.ShowBufferBytes].
// [Pane.CaptureToFile] is the capture that stays on the connection.
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
		client, err := s.OpenControl(ctx, session)
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
	connected := s.WithEngine(pool.Engine())
	// The session is handed back on the connected handle rather than as it
	// arrived. A caller writing "server, session, pool, err := ..." shadows the
	// session they passed in, so the one they go on to use is the one that
	// carries the connections.
	pool.session = session.WithServer(connected)
	return connected, pool.session, pool, nil
}

func closeControlClients(clients []*ControlClient) error {
	failures := make([]error, 0, len(clients))
	for _, client := range clients {
		failures = append(failures, client.Close())
	}
	return errors.Join(failures...)
}

// ControlPool owns the control-mode connections behind a connected [Server] and
// hands one to each command that needs it.
//
// It is the value that closes what [Server.OpenControlPool] opened. A pool is
// separate from the handle for the reason [Engine] gives for owning no
// shutdown: a [Server] is copied into every record it produces, so shutdown
// cannot belong to it. Close the pool when the program is done with the tmux
// server; commands issued afterwards report [ErrControlClosed] as transport
// failures, which lenient collection reads normalize exactly as they normalize
// a tmux server that is not running.
//
// More than one connection is worth owning only for concurrent callers, since
// a single connection carries one tmux command at a time. A pool hands each
// command a connection no other command is using and returns it afterwards, so
// concurrency is bounded by the number of connections rather than by the
// library.
//
// A pool carries commands and nothing else. It exposes no notification stream:
// which connection carried which command is not a caller-visible property, so a
// pooled connection's notifications are not a sequence a caller could reason
// about. Open a control client of your own with [Server.OpenControl] to watch
// tmux as it changes.
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

// Engine returns the [Engine] the connected handle already carries. It is the
// seam for a second handle that was built elsewhere, such as one from
// [NewServerFromEnv]: passing it to [Server.WithEngine] moves that handle onto
// these connections. Records obtained from the other handle before the call
// keep starting tmux processes, so look them up again through the result.
func (p *ControlPool) Engine() Engine { return poolEngine{pool: p} }

// Session returns the attached session on the connected handle, which is the
// same value [Server.OpenControlPool] returned. Reading it here rather than
// keeping the session that was passed in is what a caller wants: the one
// passed in still starts a tmux process per command.
func (p *ControlPool) Session() Session { return p.session }

// Connections reports how many of the pool's connections can still carry a
// command. It starts at [ConnectOptions.Connections] and falls as connections
// fail, so a supervisor can notice a pool degrading before it reaches zero and
// every command starts failing.
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
	p.stopOnce.Do(func() { close(p.stopped) })
}

// run carries one tmux command on a connection no other command is using.
// commandList is forwarded rather than defaulted: this engine wraps another,
// and a wrapper that drops it silently turns a command list into quoted
// arguments of the first command.
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
// The distinction matters because a retired connection is never replaced. A
// command that reached tmux before its connection died is indistinguishable
// from one that did not, so reconnecting and retrying would re-run a mutation;
// dropping the connection instead keeps the surviving ones serving while the
// failed command reports what happened.
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

// Supports reports that control connections carry server commands only.
// Supports stops claiming commands once the pool is closed, so routing sends
// them to a tmux process instead.
//
// A pool is an optimization, and an optimization that ends must not take
// correctness with it. Records derived from a pooled handle outlive the pool
// routinely: a function that builds something over a pool and returns what it
// built hands back records whose handle is the pooled one, and closing the pool
// on the way out would leave the caller holding records that fail rather than
// records that are merely slower again.
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

// declined reports the fallback, because a command that silently costs a
// process is the kind of thing a caller wants told rather than measured.
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
