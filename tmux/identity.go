package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
		return left.daemon == nil && right.daemon == nil && left.Equal(right)
	}
	return sameSnapshotIdentity(*left.daemon, *right.daemon)
}

func (s Server) withDaemon(identity snapshotServerIdentity) Server {
	s.daemon = &identity
	s.startupVersion = nil
	return s
}

func (s Server) withoutDaemon() Server {
	s.daemon = nil
	s.startupVersion = nil
	return s
}

func (s Server) guardCommand(
	arguments []string,
	commandList bool,
) ([]string, *daemonCommandGuard, error) {
	if (s.daemon == nil && s.startupVersion == nil) || s.boundToInstance() {
		return arguments, nil, nil
	}
	if !commandList {
		arguments = escapeCommandListSeparators(arguments)
	}
	guard := &daemonCommandGuard{
		failure: "__libtmux_daemon_replaced_" + rand.Text() + "__",
	}
	var condition string
	if s.daemon == nil {
		// A daemon can appear after the absent-socket probe. Its version must
		// still match the executable checked to create it.
		condition = "#{==:#{version}," + escapeFormatLiteral(s.startupVersion.String()) + "}"
	} else {
		// pid and start_time identify the daemon generation. The socket path
		// is not compared: no format escaping makes tmux match every byte of
		// it (braces never match), and a handle addresses its socket with -S.
		condition = fmt.Sprintf(
			"#{&&:#{==:#{pid},%s},#{==:#{start_time},%s}}",
			s.daemon.pid,
			s.daemon.startTime,
		)
	}
	guarded := []string{"if-shell", "-F", condition, "", guard.failure, ";"}
	return append(guarded, arguments...), guard, nil
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
	return s.probeServerIdentity(ctx, snapshotIdentityFields())
}

func (s Server) probeServerIdentity(ctx context.Context, fields []formatField) (snapshotServerIdentity, error) {
	if s.daemon != nil {
		return *s.daemon, nil
	}
	marker := newIdentityProbeMarker()
	probe := s
	probe.probesIdentity = true
	result, err := probe.runCheckedCommand(ctx, commandServer,
		identityProbeArguments(marker, fields), nil, false)
	commandResult := cloneCommandResult(CommandResult{
		Command: result.Command, Stdout: result.Stdout, RawStdout: result.RawStdout,
		Stderr: result.Stderr, ExitCode: result.ExitCode,
	})
	if err != nil {
		return snapshotServerIdentity{}, commandTransportFailure(err)
	}
	values, ok := identityProbeReport(marker, fields, result.ExitCode, result.Stderr)
	if !ok {
		return snapshotServerIdentity{}, newCommandError("run-shell", commandResult)
	}
	row, err := newFormatValues(Version{}, fields, values)
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	rows := []formatValues{row}
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

// identityProbeMarkerPrefix opens the private word the identity probe asks
// tmux to run. No command carries that name, so tmux refuses it and reports
// the word, with every format already expanded, on stderr.
const identityProbeMarkerPrefix = "libtmux-version-"

// newIdentityProbeMarker is replaced only by this package's tests, which pin
// the marker so scripted executors can answer the probe.
var newIdentityProbeMarker = randomIdentityProbeMarker

func randomIdentityProbeMarker() string {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		panic("tmux: read random bytes: " + err.Error())
	}
	return identityProbeMarkerPrefix + hex.EncodeToString(random) + "-"
}

// identityProbeArguments builds the hook-free identity probe. Unlike
// display-message, run-shell has no after-hook, and with -C the expanded
// string fails in the parser before any command-error hook can fire.
//
// tmux reports only the first unparseable word, so every field expands into
// one. The parser removes backslash quoting before it prints that word, so the
// socket path, the only field with arbitrary bytes, is single-quoted with its
// own quotes re-opened and goes last; the others are quoted by #{q:}.
func identityProbeArguments(marker string, fields []formatField) []string {
	var template strings.Builder
	template.WriteString(marker)
	for _, field := range identityProbeOrder(fields) {
		if field.name == "socket_path" {
			template.WriteString(`'#{s/'/'\\''/:socket_path}'`)
		} else {
			template.WriteString("#{q:" + field.name + "}")
			template.WriteByte(formatFieldSeparator)
		}
	}
	template.WriteByte(formatRecordTerminator)
	return []string{"run-shell", "-C", template.String()}
}

// identityProbeOrder keeps the socket path, whose bytes are unconstrained,
// last so it can be recovered without escaping rules.
func identityProbeOrder(fields []formatField) []formatField {
	ordered := make([]formatField, 0, len(fields))
	for _, field := range fields {
		if field.name != "socket_path" {
			ordered = append(ordered, field)
		}
	}
	for _, field := range fields {
		if field.name == "socket_path" {
			ordered = append(ordered, field)
		}
	}
	return ordered
}

// identityProbeReport extracts the field values, in the order of fields, from
// the refusal tmux prints for the probe's marker word. Anything other than one
// matching line is not an identity report; a path with a newline is refused.
func identityProbeReport(
	marker string,
	fields []formatField,
	exitCode int,
	stderr []string,
) ([]string, bool) {
	if exitCode != 1 || len(stderr) != 1 {
		return nil, false
	}
	record, ok := strings.CutPrefix(stderr[0], "unknown command: "+marker)
	if !ok {
		return nil, false
	}
	record, ok = strings.CutSuffix(record, string(formatRecordTerminator))
	if !ok {
		return nil, false
	}
	ordered := identityProbeOrder(fields)
	parts := strings.SplitN(record, string(formatFieldSeparator), len(ordered))
	if len(parts) != len(ordered) {
		return nil, false
	}
	byName := make(map[string]string, len(ordered))
	for index, field := range ordered {
		byName[field.name] = parts[index]
	}
	values := make([]string, len(fields))
	for index, field := range fields {
		values[index] = byName[field.name]
	}
	return values, true
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
