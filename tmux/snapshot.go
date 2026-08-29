package tmux

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
)

// Snapshot error sentinels classify [SnapshotDecodeError] and
// [SnapshotLookupError] through errors.Is.
var (
	// ErrMalformedSnapshot identifies invalid required fields in decoded rows.
	// SnapshotDecodeError matches it through errors.Is.
	ErrMalformedSnapshot = errors.New("tmux: malformed snapshot")
	// ErrSnapshotNotFound identifies a point lookup with no matching view.
	// SnapshotLookupError matches it through errors.Is.
	ErrSnapshotNotFound = errors.New("tmux: snapshot object not found")
	// ErrSnapshotAmbiguous identifies a point lookup with multiple matching
	// views. SnapshotLookupError matches it through errors.Is.
	ErrSnapshotAmbiguous = errors.New("tmux: snapshot object is ambiguous")
)

// SnapshotDecodeError identifies one malformed decoded list row. It matches
// [ErrMalformedSnapshot] through errors.Is; callers can recover its redacted
// location fields with errors.As.
type SnapshotDecodeError struct {
	// Object names the tmux object whose listing contained the record.
	Object string
	// Record is the one-based physical record number in that listing.
	Record int
	// Field names the required format field that could not be decoded.
	Field string
	// Value is redacted for library-created errors because snapshot fields may
	// contain pane output, commands, paths, or other caller data.
	Value string
	// Reason describes why the field was malformed.
	Reason string
}

// Error implements error.
func (e *SnapshotDecodeError) Error() string {
	return fmt.Sprintf(
		"%v: %s record %d field %q value %q: %s",
		ErrMalformedSnapshot,
		e.Object,
		e.Record,
		e.Field,
		e.Value,
		e.Reason,
	)
}

// Unwrap makes SnapshotDecodeError compatible with ErrMalformedSnapshot.
func (e *SnapshotDecodeError) Unwrap() error { return ErrMalformedSnapshot }

// SnapshotLookupError reports the cardinality of an unsuccessful point lookup.
// It matches [ErrSnapshotNotFound] or [ErrSnapshotAmbiguous] through errors.Is;
// callers can recover its target and count with errors.As.
type SnapshotLookupError struct {
	// Object names the requested snapshot object kind.
	Object string
	// Identifier is the requested stable identifier.
	Identifier string
	// Matches is the number of matching materialized views.
	Matches int
}

// Error implements error.
func (e *SnapshotLookupError) Error() string {
	if e.Matches == 0 {
		return fmt.Sprintf("%v: %s %q", ErrSnapshotNotFound, e.Object, e.Identifier)
	}
	return fmt.Sprintf(
		"%v: %s %q has %d views",
		ErrSnapshotAmbiguous,
		e.Object,
		e.Identifier,
		e.Matches,
	)
}

// Unwrap classifies the failed lookup by cardinality.
func (e *SnapshotLookupError) Unwrap() error {
	if e.Matches == 0 {
		return ErrSnapshotNotFound
	}
	return ErrSnapshotAmbiguous
}

// Snapshot is an immutable, observational view of one tmux server. It is
// normally returned by [Server.Snapshot]. Its zero value has a zero [Version],
// a zero [Server], non-nil empty collection slices, and not-found point
// lookups. Accessors never query tmux; returned slices are newly allocated and
// their contained records are shallow copies.
type Snapshot struct {
	state *snapshotState
}

type snapshotState struct {
	server  Server
	version Version
	listed  snapshotCollections

	sessions []Session
	windows  []Window
	panes    []Pane
	clients  []Client

	sessionsByID     map[SessionID][]int
	windowsByID      map[WindowID][]int
	windowsBySession map[SessionID][]int
	windowsByWinlink map[winlinkKey][]int
	panesByID        map[PaneID][]int
	panesBySession   map[SessionID][]int
	panesByWinlink   map[winlinkKey][]int
	panesByView      map[paneViewKey][]int
	clientsByName    map[ClientName][]int
}

type winlinkKey struct {
	sessionID SessionID
	windowID  WindowID
	index     int
}

type paneViewKey struct {
	winlinkKey
	paneID PaneID
}

type snapshotServerIdentity struct {
	version    Version
	pid        string
	startTime  string
	socketPath string
}

type snapshotRecords struct {
	sessions []formatValues
	windows  []formatValues
	panes    []formatValues
	clients  []formatValues
}

// snapshotCollections distinguishes materialized relations from unknown ones.
type snapshotCollections uint8

const (
	listedSessions snapshotCollections = 1 << iota
	listedWindows
	listedPanes
	listedClients

	// listedEverything is what [Server.Snapshot] materializes.
	listedEverything = listedSessions | listedWindows | listedPanes | listedClients
)

// holds reports whether every kind in wanted was listed.
func (c snapshotCollections) holds(wanted snapshotCollections) bool {
	return c&wanted == wanted
}

// Version returns the tmux version that selected this snapshot's format fields.
// The zero Snapshot returns a zero Version.
func (s Snapshot) Version() Version {
	if s.state == nil {
		return Version{}
	}
	return s.state.version
}

// Server returns the configured handle that produced this snapshot. The zero
// Snapshot returns a zero Server.
func (s Snapshot) Server() Server {
	if s.state == nil {
		return Server{}
	}
	return s.state.server
}

// Sessions returns a newly allocated shallow copy of materialized sessions.
// It never queries tmux.
func (s Snapshot) Sessions() []Session {
	if s.state == nil {
		return make([]Session, 0)
	}
	return cloneValues(s.state.sessions)
}

// Windows returns a newly allocated shallow copy with one view per winlink. It
// never queries tmux.
func (s Snapshot) Windows() []Window {
	if s.state == nil {
		return make([]Window, 0)
	}
	return cloneValues(s.state.windows)
}

// Panes returns a newly allocated shallow copy of pane views for every winlink.
// It never queries tmux.
func (s Snapshot) Panes() []Pane {
	if s.state == nil {
		return make([]Pane, 0)
	}
	return cloneValues(s.state.panes)
}

// Clients returns a newly allocated shallow copy of materialized clients. It
// never queries tmux.
func (s Snapshot) Clients() []Client {
	if s.state == nil {
		return make([]Client, 0)
	}
	return cloneValues(s.state.clients)
}

// SessionsSeq returns an iterator over materialized session values. It never
// queries tmux.
func (s Snapshot) SessionsSeq() iter.Seq[Session] {
	if s.state == nil {
		return sequenceValues[Session](nil)
	}
	return sequenceValues(s.state.sessions)
}

// WindowsSeq returns an iterator over materialized winlink values. It never
// queries tmux.
func (s Snapshot) WindowsSeq() iter.Seq[Window] {
	if s.state == nil {
		return sequenceValues[Window](nil)
	}
	return sequenceValues(s.state.windows)
}

// PanesSeq returns an iterator over materialized pane-view values. It never
// queries tmux.
func (s Snapshot) PanesSeq() iter.Seq[Pane] {
	if s.state == nil {
		return sequenceValues[Pane](nil)
	}
	return sequenceValues(s.state.panes)
}

// ClientsSeq returns an iterator over materialized client values. It never
// queries tmux.
func (s Snapshot) ClientsSeq() iter.Seq[Client] {
	if s.state == nil {
		return sequenceValues[Client](nil)
	}
	return sequenceValues(s.state.clients)
}

// SessionByID returns the sole session view with id. It never queries tmux and
// returns a [SnapshotLookupError] matching [ErrSnapshotNotFound] otherwise.
func (s Snapshot) SessionByID(id SessionID) (Session, error) {
	if s.state == nil {
		return lookupSnapshotValue[Session](nil, nil, "session", id.String())
	}
	return lookupSnapshotValue(s.state.sessions, s.state.sessionsByID[id], "session", id.String())
}

// WindowByID returns the sole winlink view with id. Linked sessions can make an
// ID ambiguous, so it can return a [SnapshotLookupError] matching
// [ErrSnapshotAmbiguous]. It never queries tmux.
func (s Snapshot) WindowByID(id WindowID) (Window, error) {
	if s.state == nil {
		return lookupSnapshotValue[Window](nil, nil, "window", id.String())
	}
	return lookupSnapshotValue(s.state.windows, s.state.windowsByID[id], "window", id.String())
}

// WindowsByID returns newly allocated shallow copies of every winlink view
// with id. It never queries tmux.
func (s Snapshot) WindowsByID(id WindowID) []Window {
	if s.state == nil {
		return make([]Window, 0)
	}
	return valuesAt(s.state.windows, s.state.windowsByID[id])
}

// PaneByID returns the sole pane view with id. Linked-session views can make
// an ID ambiguous, so it can return a [SnapshotLookupError] matching
// [ErrSnapshotAmbiguous]. It never queries tmux.
func (s Snapshot) PaneByID(id PaneID) (Pane, error) {
	if s.state == nil {
		return lookupSnapshotValue[Pane](nil, nil, "pane", id.String())
	}
	return lookupSnapshotValue(s.state.panes, s.state.panesByID[id], "pane", id.String())
}

// PanesByID returns newly allocated shallow copies of every pane view with id.
// It never queries tmux.
func (s Snapshot) PanesByID(id PaneID) []Pane {
	if s.state == nil {
		return make([]Pane, 0)
	}
	return valuesAt(s.state.panes, s.state.panesByID[id])
}

// ClientByName returns the sole client view with name. It never queries tmux
// and returns a [SnapshotLookupError] matching [ErrSnapshotNotFound] or
// [ErrSnapshotAmbiguous] when cardinality is not one.
func (s Snapshot) ClientByName(name ClientName) (Client, error) {
	if s.state == nil {
		return lookupSnapshotValue[Client](nil, nil, "client", name.String())
	}
	return lookupSnapshotValue(s.state.clients, s.state.clientsByName[name], "client", name.String())
}

// Snapshot materializes sessions, winlinks, panes, and clients. Its sequential
// commands produce an observationally consistent result, not an atomic transaction.
// Failures remain errors rather than empty snapshots; cancellation may occur after
// earlier listings completed.
//
// Identity probes reject a daemon replacement during collection. An
// [InstanceBoundEngine] skips the closing probe; the opening probe still selects
// version-specific fields.
func (s Server) Snapshot(ctx context.Context) (Snapshot, error) {
	identity, err := s.probeSnapshotIdentity(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	minimum, err := ParseVersion(MinimumSupportedVersion)
	if err != nil {
		return Snapshot{}, err
	}
	if !identity.version.AtLeast(minimum) {
		return Snapshot{}, &VersionTooLowError{Current: identity.version, Minimum: minimum}
	}

	type listing struct {
		command string
		extra   []string
		target  *[]formatValues
	}
	records := snapshotRecords{}
	listings := [...]listing{
		{command: "list-sessions", target: &records.sessions},
		{command: "list-windows", extra: []string{"-a"}, target: &records.windows},
		{command: "list-panes", extra: []string{"-a"}, target: &records.panes},
		{command: "list-clients", target: &records.clients},
	}
	for _, current := range listings {
		values, listErr := s.snapshotListing(
			ctx,
			current.command,
			current.extra,
			identity.version,
		)
		if listErr != nil {
			if snapshotCollectionError(listErr) {
				return s.snapshotAfterListingFailure(ctx, identity, listErr)
			}
			return Snapshot{}, listErr
		}
		*current.target = values
	}
	closingIdentity, err := s.probeClosingIdentity(ctx, identity)
	if err != nil {
		return Snapshot{}, err
	}
	if !sameSnapshotIdentity(identity, closingIdentity) {
		return Snapshot{}, snapshotIdentityChangeError(closingIdentity)
	}
	return newSnapshotWithIdentity(s, identity.version, records, listedEverything, &identity)
}

// Preserve the listing error if the closing identity cannot be read; otherwise
// join it with any detected daemon replacement.
func (s Server) snapshotAfterListingFailure(
	ctx context.Context,
	opening snapshotServerIdentity,
	listErr error,
) (Snapshot, error) {
	closing, err := s.probeClosingIdentity(ctx, opening)
	if err != nil {
		if contextError(err) {
			return Snapshot{}, err
		}
		return Snapshot{}, listErr
	}
	if !sameSnapshotIdentity(opening, closing) {
		return Snapshot{}, errors.Join(listErr, snapshotIdentityChangeError(closing))
	}
	return Snapshot{}, listErr
}

func snapshotCollectionError(err error) bool {
	if contextError(err) {
		return false
	}
	var commandError *CommandError
	var transportError *commandTransportError
	return errors.As(err, &commandError) || errors.As(err, &transportError)
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

func (s Server) snapshotListing(
	ctx context.Context,
	command string,
	extra []string,
	version Version,
) ([]formatValues, error) {
	fields, err := formatFieldsFor(command, version)
	if err != nil {
		return nil, err
	}
	arguments := make([]string, 0, 2+len(extra))
	arguments = append(arguments, command)
	arguments = append(arguments, extra...)
	arguments = append(arguments, "-F"+formatTemplate(fields))
	result, rawOutput, err := s.literalCmdWithRaw(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, newCommandError(command, result)
	}
	values, err := decodeFormatRecords(rawOutput, version, fields)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func contextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func newSnapshot(server Server, version Version, records snapshotRecords) (Snapshot, error) {
	return newSnapshotWithIdentity(server, version, records, listedEverything, nil)
}

func newSnapshotWithIdentity(
	server Server,
	version Version,
	records snapshotRecords,
	listed snapshotCollections,
	expectedIdentity *snapshotServerIdentity,
) (Snapshot, error) {
	state := &snapshotState{
		server:           server,
		version:          version,
		listed:           listed,
		sessions:         make([]Session, 0, len(records.sessions)),
		windows:          make([]Window, 0, len(records.windows)),
		panes:            make([]Pane, 0, len(records.panes)),
		clients:          make([]Client, 0, len(records.clients)),
		sessionsByID:     make(map[SessionID][]int),
		windowsByID:      make(map[WindowID][]int),
		windowsBySession: make(map[SessionID][]int),
		windowsByWinlink: make(map[winlinkKey][]int),
		panesByID:        make(map[PaneID][]int),
		panesBySession:   make(map[SessionID][]int),
		panesByWinlink:   make(map[winlinkKey][]int),
		panesByView:      make(map[paneViewKey][]int),
		clientsByName:    make(map[ClientName][]int),
	}
	identity := snapshotIdentityValidator{version: version, expected: expectedIdentity}

	for index, values := range records.sessions {
		if err := identity.validate("session", index, values); err != nil {
			return Snapshot{}, err
		}
		id, err := requiredSnapshotIdentifier("session", index, values, "session_id", '$')
		if err != nil {
			return Snapshot{}, err
		}
		viewIndex := len(state.sessions)
		view := Session{
			formats:   values,
			server:    server,
			snapshot:  state,
			sessionID: SessionID(id),
		}
		state.sessions = append(state.sessions, view)
		state.sessionsByID[view.sessionID] = append(state.sessionsByID[view.sessionID], viewIndex)
	}

	for index, values := range records.windows {
		if err := identity.validate("window", index, values); err != nil {
			return Snapshot{}, err
		}
		sessionID, err := requiredSnapshotIdentifier("window", index, values, "session_id", '$')
		if err != nil {
			return Snapshot{}, err
		}
		windowID, err := requiredSnapshotIdentifier("window", index, values, "window_id", '@')
		if err != nil {
			return Snapshot{}, err
		}
		windowIndex, err := requiredSnapshotIndex("window", index, values, "window_index")
		if err != nil {
			return Snapshot{}, err
		}
		viewIndex := len(state.windows)
		view := Window{
			formats:     values,
			server:      server,
			snapshot:    state,
			sessionID:   SessionID(sessionID),
			windowID:    WindowID(windowID),
			windowIndex: windowIndex,
		}
		state.windows = append(state.windows, view)
		key := winlinkKey{sessionID: view.sessionID, windowID: view.windowID, index: view.windowIndex}
		state.windowsByID[view.windowID] = append(state.windowsByID[view.windowID], viewIndex)
		state.windowsBySession[view.sessionID] = append(state.windowsBySession[view.sessionID], viewIndex)
		state.windowsByWinlink[key] = append(state.windowsByWinlink[key], viewIndex)
	}

	for index, values := range records.panes {
		if err := identity.validate("pane", index, values); err != nil {
			return Snapshot{}, err
		}
		sessionID, err := requiredSnapshotIdentifier("pane", index, values, "session_id", '$')
		if err != nil {
			return Snapshot{}, err
		}
		windowID, err := requiredSnapshotIdentifier("pane", index, values, "window_id", '@')
		if err != nil {
			return Snapshot{}, err
		}
		windowIndex, err := requiredSnapshotIndex("pane", index, values, "window_index")
		if err != nil {
			return Snapshot{}, err
		}
		paneID, err := requiredSnapshotIdentifier("pane", index, values, "pane_id", '%')
		if err != nil {
			return Snapshot{}, err
		}
		paneIndex, err := requiredSnapshotIndex("pane", index, values, "pane_index")
		if err != nil {
			return Snapshot{}, err
		}
		viewIndex := len(state.panes)
		view := Pane{
			formats:     values,
			server:      server,
			snapshot:    state,
			sessionID:   SessionID(sessionID),
			windowID:    WindowID(windowID),
			windowIndex: windowIndex,
			paneID:      PaneID(paneID),
			paneIndex:   paneIndex,
		}
		state.panes = append(state.panes, view)
		winlink := winlinkKey{sessionID: view.sessionID, windowID: view.windowID, index: view.windowIndex}
		paneView := paneViewKey{winlinkKey: winlink, paneID: view.paneID}
		state.panesByID[view.paneID] = append(state.panesByID[view.paneID], viewIndex)
		state.panesBySession[view.sessionID] = append(state.panesBySession[view.sessionID], viewIndex)
		state.panesByWinlink[winlink] = append(state.panesByWinlink[winlink], viewIndex)
		state.panesByView[paneView] = append(state.panesByView[paneView], viewIndex)
	}

	for index, values := range records.clients {
		if err := identity.validate("client", index, values); err != nil {
			return Snapshot{}, err
		}
		name, err := requiredSnapshotValue("client", index, values, "client_name")
		if err != nil {
			return Snapshot{}, err
		}
		attachment, err := decodeClientAttachment(index, values)
		if err != nil {
			return Snapshot{}, err
		}
		viewIndex := len(state.clients)
		view := Client{
			formats:    values,
			server:     server,
			snapshot:   state,
			clientName: ClientName(name),
			attachment: attachment,
		}
		state.clients = append(state.clients, view)
		state.clientsByName[view.clientName] = append(state.clientsByName[view.clientName], viewIndex)
	}
	provenance := expectedIdentity
	if provenance == nil {
		provenance = identity.observed
	}
	if provenance != nil {
		bound := server.withDaemon(*provenance)
		state.server = bound
		for index := range state.sessions {
			state.sessions[index].server = bound
		}
		for index := range state.windows {
			state.windows[index].server = bound
		}
		for index := range state.panes {
			state.panes[index].server = bound
		}
		for index := range state.clients {
			state.clients[index].server = bound
		}
	}

	return Snapshot{state: state}, nil
}

func decodeClientAttachment(record int, values formatValues) (clientAttachment, error) {
	var attachment clientAttachment
	sessionID, hasSession := nonemptySnapshotValue(values, "session_id")
	if !hasSession {
		for _, field := range []string{"window_id", "window_index", "pane_id"} {
			if value, ok := nonemptySnapshotValue(values, field); ok {
				return clientAttachment{}, newSnapshotDecodeError(
					"client",
					record,
					field,
					value,
					"attachment field requires an attached session",
				)
			}
		}
		return attachment, nil
	}
	attachment.sessionID = SessionID(sessionID)
	attachment.hasSession = true
	if err := validateSnapshotIdentifier("client", record, "session_id", sessionID, '$'); err != nil {
		return clientAttachment{}, err
	}

	windowID, hasWindow := nonemptySnapshotValue(values, "window_id")
	windowIndexText, hasWindowIndex := nonemptySnapshotValue(values, "window_index")
	if hasWindow != hasWindowIndex {
		return clientAttachment{}, newSnapshotDecodeError(
			"client",
			record,
			"window_id/window_index",
			windowID+"/"+windowIndexText,
			"both attached-window fields must be present",
		)
	}
	if hasWindow {
		if err := validateSnapshotIdentifier("client", record, "window_id", windowID, '@'); err != nil {
			return clientAttachment{}, err
		}
		windowIndex, err := parseSnapshotIndex("client", record, "window_index", windowIndexText)
		if err != nil {
			return clientAttachment{}, err
		}
		attachment.windowID = WindowID(windowID)
		attachment.windowIndex = windowIndex
		attachment.hasWindow = true
	}

	paneID, hasPane := nonemptySnapshotValue(values, "pane_id")
	if hasPane && !attachment.hasWindow {
		return clientAttachment{}, newSnapshotDecodeError(
			"client",
			record,
			"pane_id",
			paneID,
			"attached pane requires an attached window",
		)
	}
	if hasPane {
		if err := validateSnapshotIdentifier("client", record, "pane_id", paneID, '%'); err != nil {
			return clientAttachment{}, err
		}
		attachment.paneID = PaneID(paneID)
		attachment.hasPane = true
	}
	return attachment, nil
}

type snapshotIdentityValidator struct {
	version  Version
	expected *snapshotServerIdentity
	observed *snapshotServerIdentity
}

func (v *snapshotIdentityValidator) validate(object string, record int, values formatValues) error {
	if values.tmuxVersion().String() != v.version.String() {
		return newSnapshotDecodeError(
			object,
			record,
			"version",
			values.tmuxVersion().String(),
			"decoder version differs from snapshot version "+v.version.String(),
		)
	}
	identity, err := decodeSnapshotIdentity(object, record, values)
	if err != nil {
		return err
	}
	if identity.version.String() != v.version.String() {
		return newSnapshotDecodeError(
			object,
			record,
			"version",
			identity.version.String(),
			"row version differs from snapshot version "+v.version.String(),
		)
	}
	baseline := v.expected
	if baseline == nil {
		baseline = v.observed
	}
	if baseline != nil && !sameSnapshotIdentity(*baseline, identity) {
		return newSnapshotDecodeError(
			object,
			record,
			"server_identity",
			formatSnapshotIdentity(identity),
			"row was produced by a different tmux server",
		)
	}
	if v.observed == nil {
		v.observed = &identity
	}
	return nil
}

func decodeSnapshotIdentity(
	object string,
	record int,
	values formatValues,
) (snapshotServerIdentity, error) {
	rawVersion, err := requiredSnapshotValue(object, record, values, "version")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	version, err := ParseVersion(rawVersion)
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	pid, err := requiredSnapshotDecimal(object, record, values, "pid")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	startTime, err := requiredSnapshotDecimal(object, record, values, "start_time")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	socketPath, err := requiredSnapshotValue(object, record, values, "socket_path")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	return snapshotServerIdentity{
		version:    version,
		pid:        pid,
		startTime:  startTime,
		socketPath: socketPath,
	}, nil
}

func requiredSnapshotDecimal(
	object string,
	record int,
	values formatValues,
	field string,
) (string, error) {
	value, err := requiredSnapshotValue(object, record, values, field)
	if err != nil {
		return "", err
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return "", newSnapshotDecodeError(
				object,
				record,
				field,
				value,
				"expected a nonnegative decimal value",
			)
		}
	}
	return value, nil
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

func requiredSnapshotValue(
	object string,
	record int,
	values formatValues,
	field string,
) (string, error) {
	value, ok := nonemptySnapshotValue(values, field)
	if ok {
		return value, nil
	}
	return "", newSnapshotDecodeError(object, record, field, value, "required nonempty field is absent")
}

func requiredSnapshotIdentifier(
	object string,
	record int,
	values formatValues,
	field string,
	sigil byte,
) (string, error) {
	value, err := requiredSnapshotValue(object, record, values, field)
	if err != nil {
		return "", err
	}
	if err := validateSnapshotIdentifier(object, record, field, value, sigil); err != nil {
		return "", err
	}
	return value, nil
}

func validateSnapshotIdentifier(object string, record int, field, value string, sigil byte) error {
	if len(value) < 2 || value[0] != sigil {
		return newSnapshotDecodeError(object, record, field, value, "invalid tmux identifier sigil")
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return newSnapshotDecodeError(object, record, field, value, "identifier body is not decimal")
		}
	}
	return nil
}

func requiredSnapshotIndex(
	object string,
	record int,
	values formatValues,
	field string,
) (int, error) {
	value, err := requiredSnapshotValue(object, record, values, field)
	if err != nil {
		return 0, err
	}
	return parseSnapshotIndex(object, record, field, value)
}

func parseSnapshotIndex(object string, record int, field, value string) (int, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return 0, newSnapshotDecodeError(object, record, field, value, "expected a nonnegative integer")
	}
	return index, nil
}

func nonemptySnapshotValue(values formatValues, field string) (string, bool) {
	value, ok := values.get(field)
	return value, ok && value != ""
}

func newSnapshotDecodeError(object string, record int, field, _ string, reason string) *SnapshotDecodeError {
	return &SnapshotDecodeError{
		Object: object,
		Record: record + 1,
		Field:  field,
		Value:  "[redacted]",
		Reason: reason,
	}
}

func lookupSnapshotValue[T any](
	values []T,
	indexes []int,
	object string,
	identifier string,
) (T, error) {
	if len(indexes) == 1 {
		return values[indexes[0]], nil
	}
	var zero T
	return zero, &SnapshotLookupError{
		Object:     object,
		Identifier: identifier,
		Matches:    len(indexes),
	}
}

func cloneValues[T any](values []T) []T {
	result := make([]T, len(values))
	copy(result, values)
	return result
}

func sequenceValues[T any](values []T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, value := range values {
			if !yield(value) {
				return
			}
		}
	}
}
