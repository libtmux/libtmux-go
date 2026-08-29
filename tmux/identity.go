package tmux

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

type snapshotServerIdentity struct {
	version    Version
	pid        string
	startTime  string
	socketPath string
}

// ErrDaemonReplaced identifies an operation refused because the tmux server
// instance that produced a materialized value no longer occupies its socket.
var ErrDaemonReplaced = errors.New("tmux: materialized value's server was replaced")

var daemonGuardSequence atomic.Uint64

type daemonCommandGuard struct {
	failure string
}

// Equal reports whether both handles currently select the same socket path.
// It evaluates relative paths and environment-dependent named and default
// sockets against each handle's frozen binding.
func (s Server) Equal(other Server) bool {
	left, err := s.SocketSelection()
	if err != nil {
		return false
	}
	right, err := other.SocketSelection()
	return err == nil && left.Path == right.Path
}

// String returns a concise representation of the server selector.
func (s Server) String() string {
	if s.state == nil {
		return "Server(invalid)"
	}
	flag, value := effectiveSocketSelectorValues(
		s.state.config.socketPath,
		s.state.config.socketName,
	)
	switch flag {
	case "-S":
		return fmt.Sprintf("Server(socket_path=%s)", value)
	case "-L":
		return fmt.Sprintf("Server(socket_name=%s)", value)
	}
	return "Server(default)"
}

// Equal reports whether two session records carry the same stable ID and tmux
// server provenance.
func (s Session) Equal(other Session) bool {
	return s.sessionID == other.sessionID && sameMaterializedDaemon(s.server, other.server)
}

// String returns the session's stable ID and queried name.
func (s Session) String() string {
	if name, ok := s.Name(); ok {
		return fmt.Sprintf("Session(%s %s)", s.sessionID, name)
	}
	return fmt.Sprintf("Session(%s)", s.sessionID)
}

// Equal reports whether two window records carry the same stable window ID.
// It intentionally collapses linked-session views with different sessions or
// indexes; use SessionID, WindowID, and WindowIndex for exact view identity.
func (w Window) Equal(other Window) bool {
	return w.windowID == other.windowID && sameMaterializedDaemon(w.server, other.server)
}

// String returns the winlink identity and its materialized parent, when present.
func (w Window) String() string {
	name, _ := w.Name()
	identity := fmt.Sprintf("Window(%s %d:%s", w.windowID, w.windowIndex, name)
	if session, ok := w.Session(); ok {
		return identity + ", " + session.String() + ")"
	}
	return identity + ")"
}

// Equal reports whether two pane records carry the same stable pane ID. It
// intentionally collapses linked-session views; a pane's exact view identity
// includes SessionID, WindowID, WindowIndex, and PaneID.
func (p Pane) Equal(other Pane) bool {
	return p.paneID == other.paneID && sameMaterializedDaemon(p.server, other.server)
}

// String returns the pane identity and its materialized parent, when present.
func (p Pane) String() string {
	identity := fmt.Sprintf("Pane(%s", p.paneID)
	if window, ok := p.Window(); ok {
		return identity + " " + window.String() + ")"
	}
	return identity + ")"
}

// Equal reports whether two clients carry the same stable client name and tmux
// server provenance.
func (c Client) Equal(other Client) bool {
	return c.clientName == other.clientName && sameMaterializedDaemon(c.server, other.server)
}

// String returns the client's stable name.
func (c Client) String() string { return fmt.Sprintf("Client(%s)", c.clientName) }

func sameMaterializedDaemon(left, right Server) bool {
	if left.daemon == nil || right.daemon == nil {
		return left.daemon == nil && right.daemon == nil
	}
	return sameSnapshotIdentity(*left.daemon, *right.daemon)
}

func (s Server) withDaemon(identity snapshotServerIdentity) Server {
	s.daemon = &identity
	return s
}

func (s Server) withoutDaemon() Server {
	s.daemon = nil
	return s
}

func (s Server) guardCommand(
	arguments []string,
	commandList bool,
) ([]string, *daemonCommandGuard, error) {
	if s.daemon == nil {
		return arguments, nil, nil
	}
	thenCommand, err := encodeControlCommand(arguments, commandList)
	if err != nil {
		return nil, nil, err
	}
	guard := &daemonCommandGuard{
		failure: "__libtmux_daemon_replaced_" +
			strconv.FormatUint(daemonGuardSequence.Add(1), 10) + "__",
	}
	condition := fmt.Sprintf(
		"#{&&:#{==:#{pid},%s},#{==:#{start_time},%s},#{==:#{socket_path},%s}}",
		s.daemon.pid,
		s.daemon.startTime,
		escapeFormatLiteral(s.daemon.socketPath),
	)
	return []string{
		"if-shell", "-F", condition, thenCommand, guard.failure,
	}, guard, nil
}

func escapeFormatLiteral(value string) string {
	var escaped strings.Builder
	escaped.Grow(len(value))
	for _, character := range value {
		// A leading # makes tmux's format parser treat its operator and
		// expansion punctuation as literal text.
		if strings.ContainsRune(",#{}:", character) {
			escaped.WriteByte('#')
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

func (g *daemonCommandGuard) rejected(exitCode int, stderr []string) bool {
	return g != nil && exitCode != 0 &&
		len(stderr) == 1 && stderr[0] == "unknown command: "+g.failure
}

func snapshotIdentityChangeError(identity snapshotServerIdentity) error {
	return newSnapshotDecodeError(
		"server",
		1,
		"server_identity",
		formatSnapshotIdentity(identity),
		"tmux server changed while materializing snapshot",
	)
}

func snapshotIdentityFields() []formatField {
	return []formatField{
		{name: "version"},
		{name: "pid"},
		{name: "start_time"},
		{name: "socket_path"},
	}
}

// probeClosingIdentity verifies one daemon produced the listing. A bound
// transport already proves this and reuses the opening identity.
func (s Server) probeClosingIdentity(
	ctx context.Context,
	opening snapshotServerIdentity,
) (snapshotServerIdentity, error) {
	if s.boundToInstance() {
		return opening, nil
	}
	return s.probeSnapshotIdentity(ctx)
}

func (s Server) probeSnapshotIdentity(ctx context.Context) (snapshotServerIdentity, error) {
	if s.daemon != nil {
		return *s.daemon, nil
	}
	fields := snapshotIdentityFields()
	result, rawOutput, err := s.literalCmdWithRaw(
		ctx,
		"display-message",
		"-p",
		formatTemplate(fields),
	)
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	if result.ExitCode != 0 {
		return snapshotServerIdentity{}, newCommandError("display-message", result)
	}
	rows, err := decodeFormatRecords(rawOutput, Version{}, fields)
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	if len(rows) != 1 {
		return snapshotServerIdentity{}, newSnapshotDecodeError(
			"server",
			0,
			"identity",
			strconv.Itoa(len(rows)),
			"expected one identity record",
		)
	}
	identity, err := decodeSnapshotIdentity("server", 0, rows[0])
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	return s.normalizeSnapshotIdentityVersion(ctx, identity)
}

func (s Server) normalizeSnapshotIdentityVersion(
	ctx context.Context,
	identity snapshotServerIdentity,
) (snapshotServerIdentity, error) {
	if !isOpenBSDVersionToken(identity.version.String()) {
		return identity, nil
	}
	capabilities, err := s.Version(ctx)
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	if capabilities.String() != identity.version.String() {
		return snapshotServerIdentity{}, newVersionQueryError(
			CommandResult{},
			"tmux binary version differed from server version",
		)
	}
	identity.version.major = capabilities.major
	identity.version.minor = capabilities.minor
	identity.version.patch = capabilities.patch
	identity.version.development = capabilities.development
	return identity, nil
}

func sameSnapshotIdentity(left, right snapshotServerIdentity) bool {
	return left.version.String() == right.version.String() &&
		left.pid == right.pid &&
		left.startTime == right.startTime &&
		left.socketPath == right.socketPath
}

func formatSnapshotIdentity(identity snapshotServerIdentity) string {
	return identity.version.String() + "/" + identity.pid + "/" +
		identity.startTime + "/" + identity.socketPath
}
