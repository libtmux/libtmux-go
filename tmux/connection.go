package tmux

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrConnectionRequiresProcess identifies an operation refused because a
// terminal [Connection] cannot start a tmux process outside its owned lanes.
var ErrConnectionRequiresProcess = errors.New(
	"tmux: operation requires a process outside the connection",
)

var (
	controlMinimumVersion32a = Version{
		raw: MinimumConnectionVersion, major: 3, minor: 2,
	}
	controlNoDetachVersion36 = Version{
		raw: "3.6", major: 3, minor: 6,
	}
)

// controlDialect keeps version differences at the process boundary instead of
// making them different public transports. tmux 3.6 added a per-client
// no-detach-on-destroy flag; earlier control clients retain the session's
// detach-on-destroy policy.
type controlDialect struct {
	version Version
}

func (dialect controlDialect) clientFlags(mode controlNotificationMode) []string {
	flags := make([]string, 0, 2)
	if mode == controlNotificationsDiscarded {
		flags = append(flags, "no-output")
	}
	if dialect.version.AtLeast(controlNoDetachVersion36) {
		flags = append(flags, "no-detach-on-destroy")
	}
	return flags
}

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
// subprocess. Its lanes are attached tmux clients; closing the last client can
// trigger tmux's destroy-unattached or exit-unattached policy. On tmux 3.6 or
// later, destroying its initial session moves the clients to another session
// when one exists; the retained Session value keeps its original identity.
type Connection struct {
	server  Server
	session Session
	pool    *controlLanePool
}

// ConnectionBound reports whether this handle retains terminal transport
// owned by a [Connection]. A bound handle never reconnects or falls back to a
// tmux subprocess after that connection closes.
func (s Server) ConnectionBound() bool { return s.connection != nil }

// OpenControl opens terminal control-mode transport to the receiver's daemon.
// The returned connection owns its lanes and must be closed. A receiver already
// bound to a connection cannot be rebound. Before tmux 3.6, destroying the
// attached session follows that session's detach-on-destroy policy and may end
// the connection.
func (s Session) OpenControl(
	ctx context.Context,
	options ConnectionOptions,
) (*Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.server.connection != nil {
		return nil, s.server.connection.routeError(ctx, commandProcess)
	}
	if options.Lanes < 0 {
		return nil, invalidServerCommandRequest(
			"connect",
			"Lanes",
			strconv.Itoa(options.Lanes),
			"must not be negative",
		)
	}
	if err := s.server.RequireVersion(ctx, controlMinimumVersion32a); err != nil {
		return nil, err
	}
	pool, err := s.server.openControlLanePool(ctx, s, options.Lanes)
	if err != nil {
		return nil, err
	}
	return bindControlConnection(s, pool), nil
}

// NewSessionConnection creates a session with a control-mode client and keeps
// that same process as the first terminal command lane. Unlike [Server.NewSession],
// it creates an attached session. If tmux reports a valid session ID before
// later setup fails, the returned partial session retains that ID and server.
// Cleanup closes the attached creator, so tmux policy may make the partial
// session no longer live. Before tmux 3.6, destroying the created session
// follows its detach-on-destroy policy and may end the connection.
// KillExisting is rejected because removing an existing session cannot be
// rolled back if later connection setup fails.
func (s Server) NewSessionConnection(
	ctx context.Context,
	request NewSessionRequest,
	options ConnectionOptions,
) (Session, *Connection, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, nil, err
	}
	if options.Lanes < 0 {
		return Session{}, nil, invalidServerCommandRequest(
			"connect",
			"Lanes",
			strconv.Itoa(options.Lanes),
			"must not be negative",
		)
	}
	request = captureNewSessionRequest(request)
	if request.KillExisting {
		return Session{}, nil, invalidLifecycleRequest(
			"KillExisting is not supported by NewSessionConnection",
		)
	}
	if err := validateServerCommandArguments(
		"new-session",
		serverCommandArgument{field: "Name", value: request.Name},
		serverCommandArgument{field: "StartDirectory", value: request.StartDirectory},
		serverCommandArgument{field: "WindowName", value: request.WindowName},
		serverCommandArgument{field: "Command", value: request.Command},
	); err != nil {
		return Session{}, nil, err
	}
	if s.connection != nil {
		return Session{}, nil, s.connection.routeError(ctx, commandProcess)
	}
	effective, err := newSessionCommandServer(s)
	if err != nil {
		return Session{}, nil, err
	}
	if err := effective.RequireVersion(ctx, controlMinimumVersion32a); err != nil {
		return Session{}, nil, err
	}
	version, err := effective.Version(ctx)
	if err != nil {
		return Session{}, nil, err
	}
	arguments, fields, err := newSessionConnectionArguments(
		request,
		controlDialect{version: version},
	)
	if err != nil {
		return Session{}, nil, err
	}
	if request.Name != "" {
		existing, lookupErr := effective.sessionNamed(ctx, request.Name)
		if lookupErr != nil {
			return Session{}, nil, lookupErr
		}
		if existing != "" {
			return Session{}, nil, fmt.Errorf("%w: %q", ErrSessionExists, request.Name)
		}
	}
	startup, guard, err := effective.guardCommand(arguments, false)
	if err != nil {
		return Session{}, nil, err
	}

	var created Session
	first, err := effective.startControl(
		ctx,
		Session{},
		controlNotificationsDiscarded,
		append([]string{"-C"}, startup...),
		func(client *ControlClient) error {
			accepted, acceptErr := effective.acceptNewSessionFrame(
				ctx,
				client,
				guard,
				arguments,
				fields,
			)
			created = accepted
			if acceptErr != nil {
				return acceptErr
			}
			client.server = accepted.server
			client.session = created
			return nil
		},
	)
	if err != nil {
		return created, nil, err
	}
	materialized, err := created.Refresh(ctx)
	if err != nil {
		return created, nil, errors.Join(err, first.Close())
	}

	count := max(options.Lanes, 1)
	clients := make([]*ControlClient, 1, count)
	clients[0] = first
	for range count - 1 {
		client, openErr := materialized.server.openControl(
			ctx,
			materialized,
			controlNotificationsDiscarded,
		)
		if openErr != nil {
			return materialized, nil, errors.Join(openErr, closeControlClients(clients))
		}
		clients = append(clients, client)
	}
	pool := newControlLanePool(clients)
	return materialized, bindControlConnection(materialized, pool), nil
}

func (s Server) acceptNewSessionFrame(
	ctx context.Context,
	client *ControlClient,
	guard *daemonCommandGuard,
	arguments []string,
	fields []formatField,
) (Session, error) {
	frame, err := client.nextStartupFrame(ctx, guard)
	if err != nil {
		return Session{}, err
	}
	if frame.failed {
		result := controlCommandResults(
			arguments,
			[]ControlCommandResult{frame.result(arguments)},
		)
		return Session{}, newRedactedCommandError("new-session", result)
	}
	rows, err := decodeFormatRecords(frame.rawStdout, Version{}, fields)
	if err != nil {
		return Session{}, err
	}
	if len(rows) != 1 {
		return Session{}, fmt.Errorf(
			"%w: session command printed %d identity lines",
			ErrInvalidCommandOutput,
			len(rows),
		)
	}
	identifier, err := requiredSnapshotValue("session", 0, rows[0], "session_id")
	if err != nil {
		return Session{}, err
	}
	if err = validateStableTarget("session", identifier); err != nil {
		return Session{}, err
	}
	created := Session{server: s, sessionID: SessionID(identifier)}
	identity, err := decodeSnapshotIdentity("session", 0, rows[0])
	if err != nil {
		return created, err
	}
	identity, err = s.normalizeSnapshotIdentityVersion(ctx, identity)
	if err != nil {
		return created, err
	}
	if s.daemon != nil && !sameSnapshotIdentity(*s.daemon, identity) {
		return created, ErrDaemonReplaced
	}
	created.server = s.withDaemon(identity)
	return created, nil
}

func newSessionConnectionArguments(
	request NewSessionRequest,
	dialect controlDialect,
) ([]string, []formatField, error) {
	fields := append(
		[]formatField{{name: "session_id", scope: formatScopeSession, kind: formatKindSessionID}},
		snapshotIdentityFields()...,
	)
	arguments, err := renderNewSessionArguments(
		request,
		formatTemplate(fields),
		false,
		strings.Join(dialect.clientFlags(controlNotificationsDiscarded), ","),
	)
	return arguments, fields, err
}

func bindControlConnection(session Session, pool *controlLanePool) *Connection {
	connection := &Connection{pool: pool}
	bound := session.server
	bound.connection = connection
	connection.server = bound
	connection.session = session.withServer(bound)
	return connection
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

// Call executes one safely encoded tmux command on an owned lane and returns
// every reply frame in order. An alias may produce zero or multiple frames; a
// %error frame remains result data through [ControlCommandResult.Failed]. Args
// always describe one command, so a bare semicolon is quoted as an operand
// rather than interpreted as a command-list separator.
func (c *Connection) Call(
	ctx context.Context,
	args ...string,
) ([]ControlCommandResult, error) {
	if err := c.routeError(ctx, commandServer); err != nil {
		return nil, err
	}
	return c.pool.call(ctx, args, false)
}

// CloseContext starts terminal shutdown and waits within ctx. The context
// bounds only the wait; a later call may resume waiting for the same shutdown.
func (c *Connection) CloseContext(ctx context.Context) error {
	if c == nil || c.pool == nil {
		return ErrControlClosed
	}
	return c.pool.closeContext(ctx)
}

// Close terminally closes every lane on bounded internal contexts. It is safe
// to call concurrently and more than once.
func (c *Connection) Close() error {
	if c == nil || c.pool == nil {
		return ErrControlClosed
	}
	return c.pool.close()
}

func (c *Connection) routeError(ctx context.Context, kind commandKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.terminalError(kind)
}

func (c *Connection) terminalError(kind commandKind) error {
	if c == nil || c.pool == nil {
		return ErrControlClosed
	}
	select {
	case <-c.pool.stopped:
		return ErrControlClosed
	default:
	}
	if kind != commandServer {
		return ErrConnectionRequiresProcess
	}
	return nil
}

func (c *Connection) run(
	ctx context.Context,
	kind commandKind,
	arguments []string,
	commandList bool,
) (CommandResult, error) {
	if err := c.routeError(ctx, kind); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return c.pool.run(ctx, arguments, commandList)
}
