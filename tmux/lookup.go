package tmux

import (
	"context"
	"errors"
)

// Session performs a canonical live lookup of id and returns a newly
// materialized record. Use [Session.Refresh] when beginning from a record.
// Canceling ctx stops this read-only lookup's local wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (s Server) Session(ctx context.Context, id SessionID) (Session, error) {
	values, version, identity, err := s.livePoint(ctx, "session", "session_id", id.String(), "list-sessions", false)
	if err != nil {
		return Session{}, err
	}
	// A point lookup lists one kind, so a relation reached from the record it
	// returns is unknown rather than empty.
	snapshot, err := newSnapshotWithIdentity(s, version, snapshotRecords{
		sessions: []formatValues{values},
	}, listedSessions, &identity)
	if err != nil {
		return Session{}, err
	}
	return snapshot.SessionByID(id)
}

// Window performs a canonical live lookup of id using tmux's canonical session
// and returns a newly materialized record. It does not preserve a linked-session
// view; use [Window.ResolveSession] for that exact relationship. Canceling ctx
// stops this read-only lookup's local wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (s Server) Window(ctx context.Context, id WindowID) (Window, error) {
	values, version, identity, err := s.livePoint(ctx, "window", "window_id", id.String(), "list-windows", true)
	if err != nil {
		return Window{}, err
	}
	// A point lookup lists one kind, so a relation reached from the record it
	// returns is unknown rather than empty.
	snapshot, err := newSnapshotWithIdentity(s, version, snapshotRecords{
		windows: []formatValues{values},
	}, listedWindows, &identity)
	if err != nil {
		return Window{}, err
	}
	return snapshot.WindowByID(id)
}

// Pane performs a canonical live lookup of id using tmux's canonical session
// and returns a newly materialized record. It does not preserve a linked-session
// view; use [Pane.ResolveWindow] for that exact relationship. Canceling ctx
// stops this read-only lookup's local wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (s Server) Pane(ctx context.Context, id PaneID) (Pane, error) {
	values, version, identity, err := s.livePoint(ctx, "pane", "pane_id", id.String(), "list-panes", true)
	if err != nil {
		return Pane{}, err
	}
	// A point lookup lists one kind, so a relation reached from the record it
	// returns is unknown rather than empty.
	snapshot, err := newSnapshotWithIdentity(s, version, snapshotRecords{
		panes: []formatValues{values},
	}, listedPanes, &identity)
	if err != nil {
		return Pane{}, err
	}
	return snapshot.PaneByID(id)
}

// Client performs a canonical live lookup of name and returns a newly
// materialized record. Canceling ctx stops this read-only lookup's local wait;
// errors.Is can detect context.Canceled or context.DeadlineExceeded as applicable.
func (s Server) Client(ctx context.Context, name ClientName) (Client, error) {
	values, version, identity, err := s.livePoint(ctx, "client", "client_name", name.String(), "list-clients", false)
	if err != nil {
		return Client{}, err
	}
	// A point lookup lists one kind, so a relation reached from the record it
	// returns is unknown rather than empty.
	snapshot, err := newSnapshotWithIdentity(s, version, snapshotRecords{
		clients: []formatValues{values},
	}, listedClients, &identity)
	if err != nil {
		return Client{}, err
	}
	return snapshot.ClientByName(name)
}

// Refresh performs a canonical live lookup for the session's stable ID and
// returns a new record without mutating the receiver. Canceling ctx stops this
// read-only lookup's local wait; errors.Is can detect context.Canceled or
// context.DeadlineExceeded as applicable.
func (s Session) Refresh(ctx context.Context) (Session, error) {
	return s.server.Session(ctx, s.sessionID)
}

// Refresh performs a canonical live lookup for the window's stable ID and
// returns a new record without mutating the receiver. It does not preserve a
// linked-session view; use [Window.ResolveSession] for exact relationships.
// Canceling ctx stops this read-only lookup's local wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (w Window) Refresh(ctx context.Context) (Window, error) {
	return w.server.Window(ctx, w.windowID)
}

// Refresh performs a canonical live lookup for the pane's stable ID and
// returns a new record without mutating the receiver. It does not preserve a
// linked-session view; use [Pane.ResolveWindow] for exact relationships.
// Canceling ctx stops this read-only lookup's local wait; errors.Is can detect
// context.Canceled or context.DeadlineExceeded as applicable.
func (p Pane) Refresh(ctx context.Context) (Pane, error) {
	return p.server.Pane(ctx, p.paneID)
}

// Refresh performs a canonical live lookup for the client's stable name and
// returns a new record without mutating the receiver. Canceling ctx stops this
// read-only lookup's local wait; errors.Is can detect context.Canceled or
// context.DeadlineExceeded as applicable.
func (c Client) Refresh(ctx context.Context) (Client, error) {
	return c.server.Client(ctx, c.clientName)
}

func (s Server) livePoint(
	ctx context.Context,
	object string,
	identityField string,
	identifier string,
	command string,
	targeted bool,
) (formatValues, Version, snapshotServerIdentity, error) {
	return s.livePointWithTargetValidation(
		ctx,
		object,
		identityField,
		identifier,
		command,
		targeted,
		true,
	)
}

func (s Server) livePaneFromEnvironment(
	ctx context.Context,
	identifier string,
) (formatValues, Version, snapshotServerIdentity, error) {
	return s.livePointWithTargetValidation(
		ctx,
		"pane",
		"pane_id",
		identifier,
		"list-panes",
		true,
		false,
	)
}

func (s Server) livePointWithTargetValidation(
	ctx context.Context,
	object string,
	identityField string,
	identifier string,
	command string,
	targeted bool,
	validateTarget bool,
) (formatValues, Version, snapshotServerIdentity, error) {
	if validateTarget {
		if err := validateTypedTarget(command, identityField, object, identifier); err != nil {
			return formatValues{}, Version{}, snapshotServerIdentity{}, err
		}
	}
	identity, err := s.probeSnapshotIdentity(ctx)
	if err != nil {
		return formatValues{}, Version{}, snapshotServerIdentity{}, err
	}
	minimum, err := ParseVersion(MinimumSupportedVersion)
	if err != nil {
		return formatValues{}, Version{}, snapshotServerIdentity{}, err
	}
	if !identity.version.AtLeast(minimum) {
		return formatValues{}, Version{}, snapshotServerIdentity{}, &VersionTooLowError{
			Current: identity.version,
			Minimum: minimum,
		}
	}

	extra := make([]string, 0, 2)
	if targeted {
		extra = append(extra, "-t", identifier)
	}
	rows, err := s.snapshotListing(ctx, command, extra, identity.version)
	if err != nil {
		if targeted && targetNotFoundCommand(err) {
			return formatValues{}, Version{}, snapshotServerIdentity{}, s.liveLookupAbsence(
				ctx,
				identity,
				object,
				identifier,
				err,
			)
		}
		return formatValues{}, Version{}, snapshotServerIdentity{}, err
	}

	matches := make([]formatValues, 0, 1)
	for _, row := range rows {
		if value, ok := row.get(identityField); ok && value == identifier {
			matches = append(matches, row)
		}
	}
	selected, err := selectLivePoint(matches, object, identifier, targeted)
	if err != nil {
		if errors.Is(err, ErrSnapshotNotFound) {
			err = s.liveLookupAbsence(ctx, identity, object, identifier, nil)
		}
		return formatValues{}, Version{}, snapshotServerIdentity{}, err
	}
	closing, err := s.probeClosingIdentity(ctx, identity)
	if err != nil {
		return formatValues{}, Version{}, snapshotServerIdentity{}, err
	}
	if !sameSnapshotIdentity(identity, closing) {
		return formatValues{}, Version{}, snapshotServerIdentity{}, snapshotIdentityChangeError(closing)
	}
	return selected, identity.version, identity, nil
}

func (s Server) liveLookupAbsence(
	ctx context.Context,
	opening snapshotServerIdentity,
	object string,
	identifier string,
	listingErr error,
) error {
	closing, err := s.probeClosingIdentity(ctx, opening)
	if err != nil {
		if contextError(err) {
			return err
		}
		// The closing probe only guards against a second tmux server having
		// answered the listing that proved this absence. A probe that cannot
		// run at all rules that out rather than leaving it open, because a
		// server that has gone did not replace the one that answered, so the
		// absence stands and the probe's own failure adds nothing to report.
		return liveLookupError(object, identifier, 0)
	}
	if !sameSnapshotIdentity(opening, closing) {
		changeErr := snapshotIdentityChangeError(closing)
		if listingErr != nil {
			return errors.Join(listingErr, changeErr)
		}
		return changeErr
	}
	return liveLookupError(object, identifier, 0)
}

func selectLivePoint(
	matches []formatValues,
	object string,
	identifier string,
	winlink bool,
) (formatValues, error) {
	if len(matches) == 0 {
		return formatValues{}, liveLookupError(object, identifier, 0)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if !winlink {
		return formatValues{}, liveLookupError(object, identifier, len(matches))
	}

	for _, row := range matches {
		if active, ok := row.get("window_active"); ok && active == "1" {
			return row, nil
		}
	}
	best := 0
	bestIndex, err := requiredSnapshotIndex(object, 0, matches[0], "window_index")
	if err != nil {
		return formatValues{}, err
	}
	for index := 1; index < len(matches); index++ {
		windowIndex, err := requiredSnapshotIndex(object, index, matches[index], "window_index")
		if err != nil {
			return formatValues{}, err
		}
		if windowIndex < bestIndex {
			best = index
			bestIndex = windowIndex
		}
	}
	return matches[best], nil
}

func targetNotFoundCommand(err error) bool {
	var commandError *CommandError
	return errors.As(err, &commandError) && commandError.targetNotFound
}

func liveLookupError(object, identifier string, matches int) *SnapshotLookupError {
	return &SnapshotLookupError{
		Object:     object,
		Identifier: identifier,
		Matches:    matches,
	}
}
