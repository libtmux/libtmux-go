package tmux

import (
	"context"
	"errors"
)

type snapshotRecords struct {
	sessions []formatValues
	windows  []formatValues
	panes    []formatValues
	clients  []formatValues
}

// Snapshot materializes sessions, winlinks, panes, and clients. Its sequential
// commands produce an observationally consistent result, not an atomic transaction.
// Failures remain errors rather than empty snapshots; cancellation may occur after
// earlier listings completed.
//
// Identity probes reject a daemon replacement during subprocess collection.
// A connection is already bound to the exact daemon and skips the closing
// probe; the opening probe still selects version-specific fields.
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
