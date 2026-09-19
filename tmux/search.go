package tmux

import "context"

var searchClientsFilterVersion34 = Version{raw: "3.4", major: 3, minor: 4}

type searchCollection uint8

type searchRowMatch struct {
	field string
	value string
}

const (
	searchSessions searchCollection = iota
	searchWindows
	searchPanes
	searchClients
)

// SearchSessions returns a newly materialized snapshot projection of sessions
// selected by tmux's live -f expression. A nil filter omits -f; a nonnil empty
// filter remains an explicit empty expression.
func (s Server) SearchSessions(
	ctx context.Context,
	filter *TmuxFilter,
) ([]Session, error) {
	snapshot, err := s.searchSnapshot(
		ctx, "list-sessions", nil, filter, searchSessions,
	)
	if err != nil {
		return nil, err
	}
	return snapshot.Sessions(), nil
}

// SearchClients returns a newly materialized snapshot projection of clients
// selected by tmux's live -f expression. A nil filter omits -f and requests the
// unfiltered listing on every supported tmux version. A nonnil filter,
// including an empty expression, requires tmux 3.4 or newer and otherwise
// returns [VersionTooLowError].
func (s Server) SearchClients(
	ctx context.Context,
	filter *TmuxFilter,
) ([]Client, error) {
	snapshot, err := s.searchSnapshot(
		ctx, "list-clients", nil, filter, searchClients,
	)
	if err != nil {
		return nil, err
	}
	return snapshot.Clients(), nil
}

// SearchWindows returns a newly materialized snapshot projection of winlinks
// selected by tmux's live -f expression. A nil filter omits -f and a nonnil
// empty filter sends an explicit expression.
func (s Server) SearchWindows(
	ctx context.Context,
	filter *TmuxFilter,
) ([]Window, error) {
	snapshot, err := s.searchSnapshot(
		ctx, "list-windows", []string{"-a"}, filter, searchWindows,
	)
	if err != nil {
		return nil, err
	}
	return snapshot.Windows(), nil
}

// SearchPanes returns a newly materialized snapshot projection of pane views
// selected by tmux's live -f expression. A nil filter omits -f and a nonnil
// empty filter sends an explicit expression.
func (s Server) SearchPanes(
	ctx context.Context,
	filter *TmuxFilter,
) ([]Pane, error) {
	snapshot, err := s.searchSnapshot(
		ctx, "list-panes", []string{"-a"}, filter, searchPanes,
	)
	if err != nil {
		return nil, err
	}
	return snapshot.Panes(), nil
}

// SearchWindows returns this session's winlinks selected by tmux's live -f
// expression. A nil filter omits -f and a nonnil empty filter sends an
// explicit expression. The session's stable identity limits the projection.
func (s Session) SearchWindows(
	ctx context.Context,
	filter *TmuxFilter,
) ([]Window, error) {
	identifier := s.sessionID.String()
	if err := validateTypedTarget(
		"list-windows", "SessionID", "session", identifier,
	); err != nil {
		return nil, err
	}
	snapshot, err := s.server.searchSnapshot(
		ctx,
		"list-windows",
		[]string{"-t", identifier},
		filter,
		searchWindows,
		searchRowMatch{field: "session_id", value: identifier},
	)
	if err != nil {
		return nil, err
	}
	return snapshot.Windows(), nil
}

// SearchPanes returns this session's pane views selected by tmux's live -f
// expression. A nil filter omits -f and a nonnil empty filter sends an
// explicit expression. The session's stable identity limits the projection.
func (s Session) SearchPanes(
	ctx context.Context,
	filter *TmuxFilter,
) ([]Pane, error) {
	identifier := s.sessionID.String()
	if err := validateTypedTarget(
		"list-panes", "SessionID", "session", identifier,
	); err != nil {
		return nil, err
	}
	snapshot, err := s.server.searchSnapshot(
		ctx,
		"list-panes",
		[]string{"-s", "-t", identifier},
		filter,
		searchPanes,
		searchRowMatch{field: "session_id", value: identifier},
	)
	if err != nil {
		return nil, err
	}
	return snapshot.Panes(), nil
}

// SearchPanes returns this window's pane views selected by tmux's live -f
// expression. A nil filter omits -f and a nonnil empty filter sends an
// explicit expression. Stable session and window identities limit the projection.
func (w Window) SearchPanes(
	ctx context.Context,
	filter *TmuxFilter,
) ([]Pane, error) {
	target, err := exactWindowTarget(w)
	if err != nil {
		return nil, err
	}
	snapshot, err := w.server.searchSnapshot(
		ctx,
		"list-panes",
		[]string{"-t", target},
		filter,
		searchPanes,
		searchRowMatch{field: "session_id", value: w.sessionID.String()},
		searchRowMatch{field: "window_id", value: w.windowID.String()},
	)
	if err != nil {
		return nil, err
	}
	return snapshot.Panes(), nil
}

func (s Server) searchSnapshot(
	ctx context.Context,
	command string,
	extra []string,
	filter *TmuxFilter,
	collection searchCollection,
	matches ...searchRowMatch,
) (Snapshot, error) {
	return s.searchSnapshotProjected(ctx, command, extra, filter, collection, false, matches...)
}

// searchSnapshotProjected is searchSnapshot with hierarchy reporting whether
// each row also materializes the session and window it names.
func (s Server) searchSnapshotProjected(
	ctx context.Context,
	command string,
	extra []string,
	filter *TmuxFilter,
	collection searchCollection,
	hierarchy bool,
	matches ...searchRowMatch,
) (Snapshot, error) {
	filterArguments, err := captureSearchFilter(command, filter)
	if err != nil {
		return Snapshot{}, err
	}
	extra = append(append([]string(nil), extra...), filterArguments...)

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
	if collection == searchClients && filter != nil &&
		!identity.version.AtLeast(searchClientsFilterVersion34) {
		return Snapshot{}, &VersionTooLowError{
			Current: identity.version,
			Minimum: searchClientsFilterVersion34,
		}
	}

	rows, listErr := s.snapshotListing(ctx, command, extra, identity.version)
	if listErr != nil {
		if snapshotCollectionError(listErr) {
			return s.snapshotAfterListingFailure(ctx, identity, listErr)
		}
		return Snapshot{}, listErr
	}
	if len(matches) != 0 {
		rows = matchingSearchRows(rows, matches)
	}

	closing, err := s.probeClosingIdentity(ctx, identity)
	if err != nil {
		return Snapshot{}, err
	}
	if !sameSnapshotIdentity(identity, closing) {
		return Snapshot{}, snapshotIdentityChangeError(closing)
	}
	records, listed := searchSnapshotRecords(collection, rows), searchListedCollection(collection)
	if hierarchy {
		records = paneRowHierarchy(rows)
		listed = listedSessions | listedWindows | listedPanes
	}
	return newSnapshotWithIdentity(s, identity.version, records, listed, &identity)
}

// paneRowHierarchy reads the sessions and windows holding rows out of the rows
// themselves, which carry every session- and window-scope field. Each is kept
// once: a window's panes each name it, and a record that appeared twice would
// be as ambiguous to a relation lookup as two different windows.
func paneRowHierarchy(rows []formatValues) snapshotRecords {
	records := snapshotRecords{panes: rows}
	sessions := make(map[string]struct{}, len(rows))
	windows := make(map[winlinkKey]struct{}, len(rows))
	for _, row := range rows {
		sessionID, _ := row.get("session_id")
		if _, seen := sessions[sessionID]; !seen {
			sessions[sessionID] = struct{}{}
			records.sessions = append(records.sessions, row)
		}
		windowID, _ := row.get("window_id")
		index, _ := row.getInt("window_index")
		key := winlinkKey{
			sessionID: SessionID(sessionID),
			windowID:  WindowID(windowID),
			index:     index,
		}
		if _, seen := windows[key]; !seen {
			windows[key] = struct{}{}
			records.windows = append(records.windows, row)
		}
	}
	return records
}

func captureSearchFilter(command string, filter *TmuxFilter) ([]string, error) {
	if filter == nil {
		return nil, nil
	}
	value := string(*filter)
	if err := validateServerCommandArgument(command, "Filter", value, true); err != nil {
		return nil, err
	}
	return []string{"-f", value}, nil
}

func matchingSearchRows(rows []formatValues, matches []searchRowMatch) []formatValues {
	matching := make([]formatValues, 0, len(rows))
	for _, row := range rows {
		matched := true
		for _, match := range matches {
			current, ok := row.get(match.field)
			if !ok || current != match.value {
				matched = false
				break
			}
		}
		if matched {
			matching = append(matching, row)
		}
	}
	return matching
}

func searchListedCollection(collection searchCollection) snapshotCollections {
	switch collection {
	case searchSessions:
		return listedSessions
	case searchWindows:
		return listedWindows
	case searchPanes:
		return listedPanes
	case searchClients:
		return listedClients
	}
	return 0
}

// hierarchyFromPanes lists panes and reads the sessions and windows holding
// them out of the same rows, which carry every session- and window-scope
// field. One listing therefore materializes the graph those panes sit in,
// where listing each kind separately costs a command apiece and asks tmux for
// the same values again.
func (s Server) hierarchyFromPanes(
	ctx context.Context,
	extra []string,
	matches ...searchRowMatch,
) (Snapshot, error) {
	return s.searchSnapshotProjected(
		ctx, "list-panes", extra, nil, searchPanes, true, matches...,
	)
}

func searchSnapshotRecords(collection searchCollection, rows []formatValues) snapshotRecords {
	switch collection {
	case searchSessions:
		return snapshotRecords{sessions: rows}
	case searchWindows:
		return snapshotRecords{windows: rows}
	case searchPanes:
		return snapshotRecords{panes: rows}
	case searchClients:
		return snapshotRecords{clients: rows}
	default:
		panic("unknown search collection")
	}
}
