package tmux

import (
	"context"
	"errors"
)

// ErrConnectionRequiresProcess identifies an operation refused because a
// terminal [Connection] cannot start a tmux process outside its owned lanes.
var ErrConnectionRequiresProcess = errors.New(
	"tmux: operation requires a process outside the connection",
)

// ConnectionOptions configures the control-mode lanes owned by a
// [Connection].
type ConnectionOptions struct {
	// Lanes is the number of concurrent command lanes. Zero opens one. Each
	// lane is an attached tmux client and carries one command at a time.
	Lanes int
}

// Connection owns terminal control-mode transport to the exact daemon that
// materialized its session. Values obtained from Server or Session retain the
// connection. Closing is terminal: they neither reconnect nor fall back to a
// subprocess.
type Connection struct {
	server  Server
	session Session
	pool    *ControlPool
}

// OpenControl opens terminal control-mode transport to the receiver's daemon.
// The returned connection owns its lanes and must be closed. A receiver already
// bound to a connection cannot be rebound.
func (s Session) OpenControl(
	ctx context.Context,
	options ConnectionOptions,
) (*Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.server.connection != nil {
		return nil, s.server.connection.routeError(ctx, CommandProcess)
	}
	_, _, pool, err := s.server.OpenControlPool(ctx, s, ControlPoolRequest{
		Connections: options.Lanes,
	})
	if err != nil {
		return nil, err
	}
	connection := &Connection{pool: pool}
	bound := s.server
	bound.connection = connection
	connection.server = bound
	connection.session = s.withServer(bound)
	pool.session = connection.session
	return connection, nil
}

// Server returns the ordinary server value bound to this connection.
func (c *Connection) Server() Server { return c.server }

// Session returns the attached session value bound to this connection.
func (c *Connection) Session() Session { return c.session }

// Lanes reports the number of owned control-mode command lanes.
func (c *Connection) Lanes() int {
	if c == nil || c.pool == nil {
		return 0
	}
	return len(c.pool.clients)
}

// CloseContext starts terminal shutdown and waits within ctx. The context
// bounds only the wait; a later call may resume waiting for the same shutdown.
func (c *Connection) CloseContext(ctx context.Context) error {
	if c == nil || c.pool == nil {
		return ErrControlClosed
	}
	return c.pool.CloseContext(ctx)
}

// Close terminally closes every lane on bounded internal contexts. It is safe
// to call concurrently and more than once.
func (c *Connection) Close() error {
	if c == nil || c.pool == nil {
		return ErrControlClosed
	}
	return c.pool.Close()
}

func (c *Connection) routeError(ctx context.Context, kind CommandKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.terminalError(kind)
}

func (c *Connection) terminalError(kind CommandKind) error {
	if c == nil || c.pool == nil {
		return ErrControlClosed
	}
	select {
	case <-c.pool.stopped:
		return ErrControlClosed
	default:
	}
	if kind != CommandServer {
		return ErrConnectionRequiresProcess
	}
	return nil
}

func (c *Connection) run(
	ctx context.Context,
	kind CommandKind,
	arguments []string,
	commandList bool,
) (CommandResult, error) {
	if err := c.routeError(ctx, kind); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return c.pool.run(ctx, arguments, commandList)
}
