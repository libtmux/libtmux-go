package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// OwnershipOptions bounds each cleanup attempt. The zero value uses five seconds.
type OwnershipOptions struct {
	// CleanupTimeout must be nonnegative. Cleanup uses a fresh background context,
	// so cancellation of the acquisition or body context does not stop it.
	CleanupTimeout time.Duration
}

// Owned holds explicit responsibility for destroying one remote resource.
// Value returns a borrowed handle; copying that handle does not copy responsibility.
// Close is safe to call concurrently. A successful Close is idempotent; a failed
// Close returns its error and leaves cleanup retryable. The zero value owns nothing.
// Closing a window destroys its links in all sessions and its panes, unlike Unlink.
type Owned[T any] struct {
	value T
	state *ownershipState
}

type ownershipState struct {
	mu      sync.Mutex
	server  Server
	kind    string
	id      string
	timeout time.Duration
	done    bool
}

// Value returns the captured borrowed resource. A nil owner returns its zero value.
func (o *Owned[T]) Value() T {
	if o == nil {
		var zero T
		return zero
	}
	return o.value
}

// Close destroys the captured resource by daemon identity and stable ID.
// Cleanup remains available after a client Connection closes or a body cancels.
// A transport failure can have an unknown delivery result; callers may inspect
// the error, then retry Close, which accepts an absent resource as cleaned up.
func (o *Owned[T]) Close() error {
	if o == nil || o.state == nil {
		return nil
	}
	state := o.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.done {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), state.timeout)
	defer cancel()
	if err := state.destroy(ctx); err != nil {
		return fmt.Errorf("clean up owned %s %s: %w", state.kind, state.id, err)
	}
	state.done = true
	return nil
}

// CloseInto joins cleanup failure to a named return error. Use defer
// owner.CloseInto(&err) immediately after a successful acquisition. A deferred
// call also runs during panic unwinding; a caller recovering a panic can inspect
// the same error variable. CloseInto panics when result is nil.
func (o *Owned[T]) CloseInto(result *error) {
	if result == nil {
		panic("tmux: CloseInto requires an error pointer")
	}
	*result = errors.Join(*result, o.Close())
}

func ownershipTimeout(options OwnershipOptions) (time.Duration, error) {
	if options.CleanupTimeout < 0 {
		return 0, invalidLifecycleRequest("CleanupTimeout must be nonnegative")
	}
	if options.CleanupTimeout == 0 {
		return 5 * time.Second, nil
	}
	return options.CleanupTimeout, nil
}

func newOwned[T any](value T, server Server, kind, id string, timeout time.Duration) *Owned[T] {
	// Cleanup cannot depend on the caller retaining a control connection.
	server.connection = nil
	server.requiresProcess = false
	return &Owned[T]{value: value, state: &ownershipState{
		server: server, kind: kind, id: id, timeout: timeout,
	}}
}

func (s *ownershipState) destroy(ctx context.Context) error {
	if s.server.daemon == nil || s.server.daemon.ownerGeneration == "" {
		return errors.New("tmux: cleanup requires daemon identity")
	}
	args := []string{"kill-" + s.kind}
	if s.kind != "server" {
		args = append(args, "-t", s.id)
	}
	result, err := s.server.literalCmd(ctx, args...)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		err = newCommandError(args[0], result)
		if errors.Is(err, ErrNoServer) {
			return s.waitProcess(ctx)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if s.kind != "server" {
		return nil
	}
	// An acknowledged kill precedes process exit. Observe disappearance of the
	// accepted daemon before callers remove its directory or start another one.
	for {
		identity, err := s.server.withoutDaemon().probeSnapshotIdentity(ctx)
		if errors.Is(err, ErrNoServer) || err == nil && !sameSnapshotIdentity(identity, *s.server.daemon) {
			return s.waitProcess(ctx)
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *ownershipState) waitProcess(ctx context.Context) error {
	pid, err := strconv.Atoi(s.server.daemon.pid)
	if err != nil {
		return err
	}
	return waitOwnedProcess(ctx, pid)
}

// Adopt accepts responsibility for destroying the daemon answering this endpoint.
// It does not start a daemon. An owner of a replaced daemon returns
// ErrDaemonReplaced instead of killing the replacement. Use a disposable endpoint
// when the application owns a whole server.
// Adoption initializes the reserved server option @libtmux_owner_generation when
// absent, reuses a valid 32-character hexadecimal value, and rejects empty or
// malformed metadata with ErrInvalidOwnerGeneration. Callers must not alter it.
func (s Server) Adopt(ctx context.Context, options OwnershipOptions) (*Owned[Server], error) {
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return nil, err
	}
	bound, err := s.acceptOwnership(ctx)
	if err != nil {
		return nil, err
	}
	return newOwned(bound, bound, "server", "", timeout), nil
}

// Adopt accepts destruction responsibility for this session's stable ID.
// Lookup and client Connection.Close alone leave the session alive.
// It initializes or validates the reserved daemon metadata described by Server.Adopt.
func (s Session) Adopt(ctx context.Context, options OwnershipOptions) (*Owned[Session], error) {
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return nil, err
	}
	s.server, err = s.server.acceptOwnership(ctx)
	if err != nil {
		return nil, err
	}
	value, err := s.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return newOwned(value, value.server, "session", value.ID().String(), timeout), nil
}

// Adopt accepts destruction responsibility for this window's stable ID.
// Cleanup follows the window after moves and removes all links and panes.
// It initializes or validates the reserved daemon metadata described by Server.Adopt.
func (w Window) Adopt(ctx context.Context, options OwnershipOptions) (*Owned[Window], error) {
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return nil, err
	}
	w.server, err = w.server.acceptOwnership(ctx)
	if err != nil {
		return nil, err
	}
	value, err := w.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return newOwned(value, value.server, "window", value.ID().String(), timeout), nil
}

// Adopt accepts destruction responsibility for this pane's stable ID.
// Cleanup follows the pane after moves and ignores cached parent targets.
// It initializes or validates the reserved daemon metadata described by Server.Adopt.
func (p Pane) Adopt(ctx context.Context, options OwnershipOptions) (*Owned[Pane], error) {
	timeout, err := ownershipTimeout(options)
	if err != nil {
		return nil, err
	}
	p.server, err = p.server.acceptOwnership(ctx)
	if err != nil {
		return nil, err
	}
	value, err := p.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return newOwned(value, value.server, "pane", value.ID().String(), timeout), nil
}

// AcquisitionError preserves a creation failure and its rollback result.
// Unknown means the initial dispatch did not provide a trustworthy resource and
// daemon identity; no resource is guessed or destroyed. Inspect the selected
// endpoint before retrying such a create, since tmux may have accepted it.
// A known resource whose rollback fails is also returned as an Owned value;
// callers can inspect ResourceID and retry its Close.
type AcquisitionError struct {
	// Operation names the failed creation or handoff step.
	Operation string
	// ResourceID identifies a known session, window or pane. Servers have no object ID.
	ResourceID string
	// Unknown reports an initial response that did not establish destruction authority.
	Unknown bool
	// Cause retains the creation, refresh or cancellation failure.
	Cause error
	// Rollback retains a failed cleanup attempt; nil means no rollback failure.
	Rollback error
	// Cleanup retains a failed rollback, including a keeper session from a
	// failed server acquisition. Its Close can retry that exact cleanup.
	Cleanup io.Closer
}

// Error describes the operation and preserves uncertainty in its message.
func (e *AcquisitionError) Error() string {
	return fmt.Sprintf("tmux: %s acquisition failed (resource %q, unknown result %t): %v",
		e.Operation, e.ResourceID, e.Unknown, errors.Join(e.Cause, e.Rollback))
}

// Unwrap exposes both creation and rollback failures through errors.Is and errors.As.
func (e *AcquisitionError) Unwrap() []error {
	var errs []error
	if e.Cause != nil {
		errs = append(errs, e.Cause)
	}
	if e.Rollback != nil {
		errs = append(errs, e.Rollback)
	}
	return errs
}

func rollbackAcquisition[T any](owner *Owned[T], operation string, cause error) (*Owned[T], error) {
	err := &AcquisitionError{Operation: operation, Cause: cause, Unknown: owner == nil}
	if owner != nil {
		err.ResourceID = owner.state.id
		err.Rollback = owner.Close()
		if err.Rollback == nil {
			owner = nil
		} else {
			err.Cleanup = owner
		}
	}
	return owner, err
}
