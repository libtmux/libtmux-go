package tmux

import (
	"context"
	"errors"
	"slices"
	"sync"
)

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
		client, err := s.openControl(ctx, session, controlCommands)
		if err != nil {
			return nil, errors.Join(err, closeControlClients(clients))
		}
		clients = append(clients, client)
	}
	return newControlLanePool(clients), nil
}

func newControlLanePool(clients []*ControlClient) *controlLanePool {
	free := make(chan *ControlClient, len(clients))
	for _, client := range clients {
		free <- client
	}
	pool := &controlLanePool{
		clients: clients,
		free:    free,
		stopped: make(chan struct{}),
		drained: make(chan struct{}),
		live:    len(clients),
	}
	return pool
}

func closeControlClients(clients []*ControlClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), controlClientCloseTimeout)
	defer cancel()
	return closeControlClientsContext(ctx, clients)
}

func closeControlClientsContext(ctx context.Context, clients []*ControlClient) error {
	for _, client := range clients {
		client.startClose()
	}
	failures := make([]error, 0, len(clients))
	for _, client := range clients {
		failures = append(failures, client.waitClose(ctx))
	}
	return errors.Join(failures...)
}

type controlLanePool struct {
	clients []*ControlClient

	free    chan *ControlClient
	stopped chan struct{}
	drained chan struct{}

	stopOnce sync.Once

	// open starts a client outside the lanes for a command that can block.
	open func(context.Context) (*ControlClient, error)

	mu      sync.Mutex
	live    int
	failure error
	// exclusive holds the clients running blocking commands, so closing the
	// pool ends them.
	exclusive map[*ControlClient]struct{}
}

func (p *controlLanePool) closeContext(ctx context.Context) error {
	p.stop()
	return closeControlClientsContext(ctx, p.closing())
}

func (p *controlLanePool) close() error {
	p.stop()
	return closeControlClients(p.closing())
}

// closing returns every client the pool owns. Call it after stop, which keeps
// adopt from adding another.
func (p *controlLanePool) closing() []*ControlClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	clients := slices.Clone(p.clients)
	for client := range p.exclusive {
		clients = append(clients, client)
	}
	return clients
}

func (p *controlLanePool) stop() {
	p.stopOnce.Do(func() {
		close(p.stopped)
	})
}

// run carries one command on an exclusive connection. It must forward
// commandList so a list is not reinterpreted as arguments to its first command.
func (p *controlLanePool) run(
	ctx context.Context,
	arguments []string,
	commandList bool,
) (CommandResult, error) {
	results, err := p.call(ctx, arguments, commandList)
	if err != nil {
		return CommandResult{Command: slices.Clone(arguments), ExitCode: -1}, err
	}
	return controlCommandResults(arguments, results), nil
}

// runExclusive carries one command that can block on a client opened for it
// alone. tmux has no way to cancel a command already sent to a control client,
// so the client is closed when the command ends, however it ends; a shared
// lane would stay occupied until a peer acted.
func (p *controlLanePool) runExclusive(
	ctx context.Context,
	arguments []string,
	commandList bool,
) (CommandResult, error) {
	failed := CommandResult{Command: slices.Clone(arguments), ExitCode: -1}
	if err := ctx.Err(); err != nil {
		return failed, err
	}
	select {
	case <-p.stopped:
		return failed, ErrControlClosed
	default:
	}
	client, err := p.open(ctx)
	if err != nil {
		return failed, err
	}
	if !p.adopt(client) {
		_ = client.Close()
		return failed, ErrControlClosed
	}
	results, err := client.cmdUnobserved(ctx, commandList, arguments...)
	p.mu.Lock()
	delete(p.exclusive, client)
	p.mu.Unlock()
	_ = client.Close()
	if err != nil {
		select {
		case <-p.stopped:
			err = errors.Join(ErrControlClosed, err)
		default:
		}
		return failed, err
	}
	return controlCommandResults(arguments, results), nil
}

// adopt records client as belonging to the pool, unless the pool has stopped.
func (p *controlLanePool) adopt(client *ControlClient) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.stopped:
		return false
	default:
	}
	if p.exclusive == nil {
		p.exclusive = make(map[*ControlClient]struct{})
	}
	p.exclusive[client] = struct{}{}
	return true
}

func (p *controlLanePool) call(
	ctx context.Context,
	arguments []string,
	commandList bool,
) ([]ControlCommandResult, error) {
	client, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	// Unobserved: both callers report their own, Server.runCommand for the
	// commands it sends down this lane and Connection.Call for its own.
	results, err := client.cmdUnobserved(ctx, commandList, arguments...)
	if errors.Is(err, ErrOutcomeUnknown) && connectionSurvives(err) {
		// The caller stopped waiting on a command tmux is still running.
		// The lane answers nothing else until tmux finishes it, so it
		// rejoins the pool only once a reply fence has crossed it, and a
		// caller meanwhile takes a lane that is free.
		go func() { p.release(client, client.crossReplyFence(context.Background())) }()
		return results, err
	}
	p.release(client, err)
	return results, err
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
// about the connection. Draining waits for tmux to finish the command, which
// is why a command that can block never runs on a lane; see runExclusive.
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
