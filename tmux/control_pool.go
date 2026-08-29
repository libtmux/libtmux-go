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
		client, err := s.openControl(ctx, session, controlNotificationsDiscarded)
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
	failures := make([]error, 0, len(clients))
	for _, client := range clients {
		failures = append(failures, client.Close())
	}
	return errors.Join(failures...)
}

type controlLanePool struct {
	clients []*ControlClient

	free    chan *ControlClient
	stopped chan struct{}
	drained chan struct{}

	stopOnce sync.Once

	mu      sync.Mutex
	live    int
	failure error
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

func (p *controlLanePool) call(
	ctx context.Context,
	arguments []string,
	commandList bool,
) ([]ControlCommandResult, error) {
	client, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	results, err := client.cmd(ctx, commandList, arguments...)
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
