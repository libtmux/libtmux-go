package tmux

import (
	"errors"
	"fmt"
	"iter"
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
