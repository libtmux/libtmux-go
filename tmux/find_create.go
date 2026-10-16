package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Found distinguishes a newly owned resource from a reused borrowed resource.
// Owner is nonnil only for Created resources or a creation whose rollback failed.
// Defer Owner.CloseInto(&err) after success; the nil owner of a reuse is harmless.
type Found[T any] struct {
	// Value is the borrowed handle returned by lookup or creation.
	Value T
	// Created reports creation by this call, including a failed rollback.
	Created bool
	// Owner is nil for reused resources.
	Owner *Owned[T]
}

func foundOwned[T any](owner *Owned[T]) Found[T] {
	if owner == nil {
		return Found[T]{}
	}
	return Found[T]{Value: owner.Value(), Created: true, Owner: owner}
}

func (shared *serverShared) acquireLifecycle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case shared.lifecycle <- struct{}{}:
		if err := ctx.Err(); err != nil {
			shared.releaseLifecycle()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (shared *serverShared) releaseLifecycle() {
	<-shared.lifecycle
}

// FindOrCreateSession matches an exact nonempty session name. Reuse is borrowed.
// Calls on copies of this Server and its environment-derived handles serialize.
// Waiting for another call respects context cancellation.
// Independently constructed handles and unrelated tmux clients do not share that
// lock: tmux rejects duplicate session names, and concurrent external mutations
// can make a call fail. This operation is not a transaction with other clients.
func (s Server) FindOrCreateSession(ctx context.Context, request NewSessionRequest, options OwnershipOptions) (Found[Session], error) {
	if err := validateLifecycleSessionName("name", request.Name); err != nil {
		return Found[Session]{}, err
	}
	if request.KillExisting {
		return Found[Session]{}, invalidLifecycleRequest("find-or-create does not replace sessions")
	}
	state, err := s.stateForUse()
	if err != nil {
		return Found[Session]{}, err
	}
	if err := state.shared.acquireLifecycle(ctx); err != nil {
		return Found[Session]{}, err
	}
	defer state.shared.releaseLifecycle()
	value, err := s.SessionByName(ctx, request.Name)
	if err == nil {
		return Found[Session]{Value: value}, nil
	}
	if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNoServer) {
		return Found[Session]{}, err
	}
	owner, err := s.OwnSession(ctx, request, options)
	return foundOwned(owner), err
}

// FindOrCreateWindow matches an exact window name within this session.
// Multiple matching winlinks return ErrSnapshotAmbiguous. Names containing '#',
// backslashes, or control bytes are rejected to avoid tmux's name expansion.
// Calls sharing the Server's coordination serialize; waiting respects ctx.
// Independent handles and other tmux clients can create duplicate names;
// a later lookup reports ambiguity.
func (s Session) FindOrCreateWindow(ctx context.Context, request NewWindowRequest, options OwnershipOptions) (Found[Window], error) {
	if request.Name == nil || !literalWindowName(*request.Name) {
		return Found[Window]{}, invalidLifecycleRequest("find-or-create requires a literal nonempty window name")
	}
	if request.KillExisting || request.SelectExisting {
		return Found[Window]{}, invalidLifecycleRequest("find-or-create does not replace or select windows")
	}
	request = captureNewWindowRequest(request)
	state, err := s.server.stateForUse()
	if err != nil {
		return Found[Window]{}, err
	}
	if err := state.shared.acquireLifecycle(ctx); err != nil {
		return Found[Window]{}, err
	}
	defer state.shared.releaseLifecycle()
	windows, err := s.SearchWindows(ctx, nil)
	if err != nil {
		return Found[Window]{}, err
	}
	var matches []Window
	for _, window := range windows {
		if name, ok := window.Name(); ok && name == *request.Name {
			matches = append(matches, window)
		}
	}
	if len(matches) > 1 {
		return Found[Window]{}, &SnapshotLookupError{Object: "window", Identifier: *request.Name, Matches: len(matches)}
	}
	if len(matches) == 1 {
		return Found[Window]{Value: matches[0]}, nil
	}
	owner, err := s.OwnWindow(ctx, request, options)
	if err == nil {
		if name, ok := owner.Value().Name(); !ok || name != *request.Name {
			owner, err = rollbackAcquisition(owner, "new-window", errors.New("tmux: created window name differs from selection name"))
		}
	}
	return foundOwned(owner), err
}

func literalWindowName(name string) bool {
	if name == "" || !utf8.ValidString(name) || strings.ContainsAny(name, "#\\") {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

// PaneIdentity names a pane-local user option and an exact nonempty value.
// Key must start with '@'. FindOrCreatePane sets the option on a new pane before
// returning it; the caller owns the namespace and must retain it for later reuse.
type PaneIdentity struct {
	// Key is a pane-local user option beginning with '@'.
	Key string
	// Value is the exact nonempty selection value.
	Value string
}

// FindOrCreatePane matches PaneIdentity within this stable window.
// Multiple matches return ErrSnapshotAmbiguous. Calls sharing this Server's
// coordination serialize; waiting respects ctx. Unrelated clients can alter
// options or add duplicates.
// Setting the identity is part of acquisition: failure rolls back the known pane.
func (w Window) FindOrCreatePane(ctx context.Context, identity PaneIdentity, request SplitPaneRequest, options OwnershipOptions) (Found[Pane], error) {
	if !strings.HasPrefix(identity.Key, "@") || len(identity.Key) == 1 || identity.Key == ownerGenerationOption || identity.Value == "" ||
		strings.ContainsAny(identity.Key+identity.Value, "\r\n\x00") {
		return Found[Pane]{}, invalidLifecycleRequest("pane identity requires a user option and a nonempty single-line value")
	}
	state, err := w.server.stateForUse()
	if err != nil {
		return Found[Pane]{}, err
	}
	if err := state.shared.acquireLifecycle(ctx); err != nil {
		return Found[Pane]{}, err
	}
	defer state.shared.releaseLifecycle()
	parent, err := w.Refresh(ctx)
	if err != nil {
		return Found[Pane]{}, err
	}
	panes, err := parent.SearchPanes(ctx, nil)
	if err != nil {
		return Found[Pane]{}, err
	}
	var matches []Pane
	for _, pane := range panes {
		value, ok, err := pane.RawOption(ctx, identity.Key)
		if err != nil {
			return Found[Pane]{}, err
		}
		if ok && value == identity.Value {
			matches = append(matches, pane)
		}
	}
	if len(matches) > 1 {
		return Found[Pane]{}, &SnapshotLookupError{Object: "pane", Identifier: identity.Key, Matches: len(matches)}
	}
	if len(matches) == 1 {
		return Found[Pane]{Value: matches[0]}, nil
	}
	owner, err := parent.OwnPane(ctx, request, options)
	if err != nil {
		return foundOwned(owner), err
	}
	err = owner.Value().SetOption(ctx, identity.Key, identity.Value, SetOptionOptions{})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		owner, err = rollbackAcquisition(owner, "set pane identity", err)
	}
	return foundOwned(owner), err
}

// FindOrCreate matches this handle's one endpoint; use Discover to search
// multiple endpoints. Reuse remains borrowed. A new daemon retains a keeper
// session described by request until its owner closes. KillExisting is rejected.
// The daemon's initial global environment proves which client started it, so an
// unrelated starter that wins the endpoint remains borrowed. Selected tmux config
// loading is unchanged. Calls sharing this Server's coordination serialize;
// waiting respects ctx.
// Configurations that remove the private startup marker prevent ownership proof
// and return borrowed after removing only this call's session.
func (s Server) FindOrCreate(ctx context.Context, request NewSessionRequest, options OwnershipOptions) (Found[Server], error) {
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return Found[Server]{}, err
	}
	if request.KillExisting {
		return Found[Server]{}, invalidLifecycleRequest("find-or-create does not replace a server")
	}
	state, err := s.stateForUse()
	if err != nil {
		return Found[Server]{}, err
	}
	if err := state.shared.acquireLifecycle(ctx); err != nil {
		return Found[Server]{}, err
	}
	defer state.shared.releaseLifecycle()
	found, session, err := s.findOrCreateLocked(ctx, request, options, timeout)
	if err != nil || found.Created || session == nil {
		return found, err
	}
	if err := session.Close(); err != nil {
		return found, &AcquisitionError{
			Operation: "remove startup session on borrowed server", ResourceID: session.Value().ID().String(), Rollback: err, Cleanup: session,
		}
	}
	return found, ctx.Err()
}

func (s Server) findOrCreateLocked(ctx context.Context, request NewSessionRequest, options OwnershipOptions, timeout time.Duration) (Found[Server], *Owned[Session], error) {
	identity, err := s.probeSnapshotIdentity(ctx)
	if err == nil {
		bound := s.withDaemon(identity)
		if err := bound.CheckAlive(ctx); err != nil {
			return Found[Server]{}, nil, err
		}
		return Found[Server]{Value: bound}, nil, nil
	}
	if !errors.Is(err, ErrNoServer) {
		return Found[Server]{}, nil, err
	}
	marker := "LIBTMUX_START_" + rand.Text()
	launcher, err := s.WithProcessEnvironmentValue(marker, marker)
	if err != nil {
		return Found[Server]{}, nil, err
	}
	// Classification must finish even when cancellation arrives after creation.
	acquire, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	session, createErr := launcher.ownSession(acquire, request, options, false)
	if session == nil {
		return Found[Server]{}, nil, &AcquisitionError{Operation: "start server", Cause: createErr, Unknown: true}
	}
	bound := s.withDaemon(*session.state.server.daemon)
	value, ok, err := bound.GetEnvironment(acquire, marker)
	if err != nil {
		_, rollbackErr := rollbackAcquisition(session, "identify server startup", errors.Join(createErr, err))
		return Found[Server]{}, nil, rollbackErr
	}
	if !ok || value.Removed || value.Value != marker {
		if createErr != nil {
			_, err := rollbackAcquisition(session, "create startup session on borrowed server", createErr)
			return Found[Server]{Value: bound}, nil, err
		}
		return Found[Server]{Value: bound}, session, nil
	}
	owner := newOwned(bound, bound, "server", "", timeout)
	err = errors.Join(createErr, bound.UnsetEnvironment(acquire, marker))
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		owner, err = rollbackAcquisition(owner, "start server", err)
	}
	return foundOwned(owner), session, err
}

// Ensure returns an ordinary handle to the selected running daemon, starting it
// when absent. Reuse preserves the daemon's sessions, options and environment.
// The returned handle carries no destruction responsibility.
//
// Startup loads the selected tmux configuration, creates a temporary detached
// session running cat, sets exit-empty to off, and removes that session. This
// leaves a new daemon usable with no sessions. Configuration-created resources
// remain, and session hooks can observe the temporary session. Startup also
// initializes the ownership metadata described by [Server.Adopt] for rollback.
// A daemon started by another client remains borrowed.
// If configuration removes the startup marker, the daemon also remains borrowed.
// Ensure keeps its own detached session when removing it would leave that daemon
// empty with exit-empty enabled; it does not change an unproven daemon's options.
//
// Calls sharing this Server serialize startup; waiting respects ctx. Independent
// clients can race. A failed acquisition can be delivery-ambiguous; inspect
// [AcquisitionError]. Its Cleanup retains any failed rollback for retry.
// Use [Server.FindOrCreate] when a newly created daemon should have an owner.
func (s Server) Ensure(ctx context.Context) (Server, error) {
	state, err := s.stateForUse()
	if err != nil {
		return Server{}, err
	}
	if err := state.shared.acquireLifecycle(ctx); err != nil {
		return Server{}, err
	}
	defer state.shared.releaseLifecycle()
	name := "libtmux-start-" + rand.Text()
	options := OwnershipOptions{}
	timeout, _ := ownershipTimeout(options)
	found, session, err := s.findOrCreateLocked(ctx, NewSessionRequest{Name: name, Command: "cat"}, options, timeout)
	if err != nil {
		return Server{}, err
	}
	if session == nil {
		return found.Value, nil
	}
	remove := true
	if found.Created {
		err = found.Value.SetOption(ctx, "exit-empty", "off", SetOptionOptions{})
	} else {
		remove, err = ensureCanRemoveSession(ctx, found.Value, session.Value().ID())
	}
	if err == nil && remove {
		result, commandErr := session.Value().server.literalCmd(ctx, "kill-session", "-t", session.Value().ID().String())
		_, err = requireRedactedLifecycleSuccess("kill-session", result, commandErr)
	}
	if err == nil {
		err = found.Value.CheckAlive(ctx)
	}
	if err != nil {
		if found.Created {
			_, err = rollbackAcquisition(found.Owner, "ensure server", err)
		} else {
			_, err = rollbackAcquisition(session, "ensure server", err)
		}
		return Server{}, err
	}
	return found.Value, nil
}

func ensureCanRemoveSession(ctx context.Context, server Server, id SessionID) (bool, error) {
	value, ok, err := server.RawOption(ctx, "exit-empty")
	if err != nil || ok && value == "off" {
		return err == nil, err
	}
	sessions, err := server.Sessions(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.ID() != id {
			return true, nil
		}
	}
	return false, nil
}
