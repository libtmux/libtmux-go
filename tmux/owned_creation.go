package tmux

import (
	"context"
	"errors"
)

// OwnSession creates a detached session and accepts destruction responsibility.
// KillExisting is rejected. A creation response carries daemon identity with the
// session ID, so later refresh or cancellation failures roll back on that daemon.
// Unknown initial results return AcquisitionError; a failed rollback also returns
// its owner for inspection and retry. The body should defer owner.CloseInto(&err).
func (s Server) OwnSession(ctx context.Context, request NewSessionRequest, options OwnershipOptions) (*Owned[Session], error) {
	return s.ownSession(ctx, request, options, true)
}

func (s Server) ownSession(ctx context.Context, request NewSessionRequest, options OwnershipOptions, materialize bool) (*Owned[Session], error) {
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return nil, err
	}
	if request.KillExisting {
		return nil, invalidLifecycleRequest("OwnSession does not replace existing sessions")
	}
	if _, err := s.stateForUse(); err != nil {
		return nil, err
	}
	request = captureNewSessionRequest(request)
	fields := append([]formatField{{name: "session_id", kind: formatKindSessionID}}, ownershipIdentityFields()...)
	args, err := renderNewSessionArguments(request, formatTemplate(fields), true, "")
	if err != nil {
		return nil, err
	}
	if err := validateLiteralCommandArguments(args); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, raw, dispatchErr := s.ownershipCommand(ctx, true, args)
	if errors.Is(dispatchErr, ErrInvalidOwnerGeneration) {
		return nil, dispatchErr
	}
	rows, decodeErr := decodeFormatRecords(raw, Version{}, fields)
	var owner *Owned[Session]
	if decodeErr == nil && len(rows) == 1 {
		id, idErr := requiredSnapshotValue("session", 0, rows[0], "session_id")
		identity, identityErr := decodeOwnershipIdentity(rows[0])
		if idErr == nil && validateStableTarget("session", id) == nil && identityErr == nil {
			bound := s.withDaemon(identity)
			value := Session{server: bound, sessionID: SessionID(id)}
			owner = newOwned(value, bound, "session", id, timeout)
		}
	}
	if owner != nil && s.daemon != nil && (!sameSnapshotIdentity(*s.daemon, *owner.state.server.daemon) ||
		s.daemon.ownerGeneration != "" && s.daemon.ownerGeneration != owner.state.server.daemon.ownerGeneration) {
		return rollbackAcquisition[Session](nil, "new-session", ErrDaemonReplaced)
	}
	if dispatchErr != nil {
		if !materialize && owner != nil {
			return owner, dispatchErr
		}
		return rollbackAcquisition(owner, "new-session", dispatchErr)
	}
	if result.ExitCode != 0 {
		if !materialize && owner != nil {
			return owner, newRedactedCommandError("new-session", result)
		}
		return rollbackAcquisition(owner, "new-session", newRedactedCommandError("new-session", result))
	}
	if owner == nil {
		if decodeErr == nil {
			decodeErr = ErrInvalidCommandOutput
		}
		return rollbackAcquisition(owner, "new-session", decodeErr)
	}
	identity, err := s.normalizeSnapshotIdentityVersion(ctx, *owner.state.server.daemon)
	if err != nil {
		if !materialize {
			return owner, err
		}
		return rollbackAcquisition(owner, "new-session", err)
	}
	owner.state.server = owner.state.server.withDaemon(identity)
	owner.value.server = owner.value.server.withDaemon(identity)
	if !materialize {
		return owner, nil
	}
	value, err := owner.value.Refresh(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return rollbackAcquisition(owner, "new-session", err)
	}
	owner.value = value
	return owner, nil
}

// OwnWindow creates and owns a new window. KillExisting and SelectExisting are
// rejected because they do not describe acquisition of a new resource.
// Known-ID failures roll back; a failed rollback returns its retryable owner.
func (s Session) OwnWindow(ctx context.Context, request NewWindowRequest, options OwnershipOptions) (*Owned[Window], error) {
	request = captureNewWindowRequest(request)
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return nil, err
	}
	if request.KillExisting || request.SelectExisting {
		return nil, invalidLifecycleRequest("OwnWindow requires new-window creation")
	}
	s.server, err = s.server.acceptOwnership(ctx)
	if err != nil {
		return nil, err
	}
	parent, err := s.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	value, err := parent.NewWindow(ctx, request)
	var owner *Owned[Window]
	if value.ID() != "" {
		owner = newOwned(value, parent.server, "window", value.ID().String(), timeout)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return rollbackAcquisition(owner, "new-window", err)
	}
	return owner, nil
}

// OwnPane splits this window and owns the new pane. It uses SplitPaneRequest
// across supported tmux versions. Known-ID refresh and cancellation failures
// roll back by pane ID, independent of its cached window or session.
func (w Window) OwnPane(ctx context.Context, request SplitPaneRequest, options OwnershipOptions) (*Owned[Pane], error) {
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return nil, err
	}
	w.server, err = w.server.acceptOwnership(ctx)
	if err != nil {
		return nil, err
	}
	parent, err := w.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	value, err := parent.SplitPane(ctx, request)
	var owner *Owned[Pane]
	if value.ID() != "" {
		owner = newOwned(value, parent.server, "pane", value.ID().String(), timeout)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return rollbackAcquisition(owner, "split-window", err)
	}
	return owner, nil
}
