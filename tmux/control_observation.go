package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// ErrPaneObservationLost identifies a pane observation whose control client
// can no longer receive every byte after its baseline. Stream failures that
// wrap it also preserve their underlying cause.
var ErrPaneObservationLost = errors.New("tmux: pane observation lost")

// PaneObservation is one visible pane baseline followed by the exact control
// notification stream after that baseline. Create one with
// [Pane.OpenObservation] and close it when finished. Copies share notification
// reader ownership, terminal loss, and client lifetime; closing one closes them
// all.
type PaneObservation struct {
	client    *ControlClient
	paneID    PaneID
	windowID  WindowID
	sessionID SessionID
	after     uint64
	baseline  []string
	state     *paneObservationState
}

type paneObservationState struct {
	readToken chan struct{}
	loss      error
	// exitReason is what tmux said as it ended the stream, kept so the loss
	// that follows names a cause instead of a bare EOF.
	exitReason string
}

func newPaneObservationState() *paneObservationState {
	state := &paneObservationState{readToken: make(chan struct{}, 1)}
	state.releaseReadToken()
	return state
}

// PaneID returns the pane selected at the observation boundary.
func (o *PaneObservation) PaneID() PaneID {
	if o == nil {
		return ""
	}
	return o.paneID
}

// Baseline returns an owned copy of the pane's visible text at the observation
// boundary.
func (o *PaneObservation) Baseline() []string {
	if o == nil {
		return nil
	}
	return slices.Clone(o.baseline)
}

// NextNotification returns the next notification strictly after the pane
// baseline. Notifications queued before that boundary are discarded.
// Concurrent calls are serialized. Caller context errors and
// [ControlNotificationError] affect only one call. Explicit close returns an
// error matching [os.ErrClosed].
//
// Topology loss, natural stream EOF, and terminal reader errors return
// [ErrPaneObservationLost]. Stream failures preserve their cause. Later calls
// return that loss without consuming more notifications.
func (o *PaneObservation) NextNotification(
	ctx context.Context,
) (ControlNotification, error) {
	if o == nil || o.client == nil {
		return ControlNotification{}, ErrControlClosed
	}
	state := o.state
	if state == nil {
		return ControlNotification{}, ErrControlClosed
	}
	if err := state.acquireReadToken(ctx); err != nil {
		return ControlNotification{}, err
	}
	defer state.releaseReadToken()
	if state.loss != nil {
		return ControlNotification{}, state.loss
	}
	notification, err := o.client.nextNotificationAfter(ctx, o.after)
	if err != nil {
		return ControlNotification{}, state.classifyReadError(
			ctx,
			err,
			o.client.closeRequested.Load(),
		)
	}
	arguments := notification.Arguments()
	if notification.Kind() == ControlNotificationUnlinkedWindowClose &&
		len(arguments) != 0 && WindowID(arguments[0]) == o.windowID {
		state.loss = fmt.Errorf(
			"%w: observed window is no longer linked into the attached session",
			ErrPaneObservationLost,
		)
		return ControlNotification{}, state.loss
	}
	if notification.Kind() == ControlNotificationSessionChanged &&
		len(arguments) != 0 && SessionID(arguments[0]) != o.sessionID {
		state.loss = fmt.Errorf(
			"%w: control client changed sessions",
			ErrPaneObservationLost,
		)
		return ControlNotification{}, state.loss
	}
	if notification.Kind() == ControlNotificationExit && len(arguments) != 0 {
		state.exitReason = arguments[0]
	}
	// tmux has no pane-close notification: a pane that ends while its window
	// survives is only visible as the window's new arrangement, which no longer
	// lists it.
	if notification.Kind() == ControlNotificationLayoutChange &&
		len(arguments) > 1 && WindowID(arguments[0]) == o.windowID &&
		!layoutListsPane(arguments[1], o.paneID) {
		state.loss = fmt.Errorf(
			"%w: the observed pane left its window's arrangement",
			ErrPaneObservationLost,
		)
		return ControlNotification{}, state.loss
	}
	return notification, nil
}

func (s *paneObservationState) acquireReadToken(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.readToken:
		if err := ctx.Err(); err != nil {
			s.releaseReadToken()
			return err
		}
		return nil
	}
}

func (s *paneObservationState) releaseReadToken() {
	s.readToken <- struct{}{}
}

func (s *paneObservationState) classifyReadError(
	ctx context.Context,
	err error,
	closeRequested bool,
) error {
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return err
	}
	if errors.Is(err, os.ErrClosed) {
		return err
	}
	if _, ok := errors.AsType[*ControlNotificationError](err); ok {
		return err
	}
	if closeRequested {
		return os.ErrClosed
	}
	if s.exitReason != "" {
		s.loss = fmt.Errorf("%w: tmux ended the stream: %s: %w",
			ErrPaneObservationLost, s.exitReason, err)
		return s.loss
	}
	s.loss = fmt.Errorf("%w: %w", ErrPaneObservationLost, err)
	return s.loss
}

// Close stops the dedicated control client. It is safe to call more than once.
func (o *PaneObservation) Close() error {
	if o == nil || o.client == nil {
		return nil
	}
	return o.client.Close()
}

// CloseContext starts idempotent observation shutdown and waits within ctx.
func (o *PaneObservation) CloseContext(ctx context.Context) error {
	if o == nil || o.client == nil {
		return nil
	}
	return o.client.CloseContext(ctx)
}

// OpenObservation captures the pane's visible text and returns an owned
// observation whose notifications begin at the capture's exact control-wire
// boundary. It resolves the pane's exact linked session before opening a
// dedicated control client. The caller must close the result.
func (p Pane) OpenObservation(
	ctx context.Context,
) (*PaneObservation, error) {
	session, err := p.ResolveSession(ctx)
	if err != nil {
		return nil, err
	}
	client, err := p.server.OpenControl(ctx, session)
	if err != nil {
		return nil, err
	}
	observation, err := client.observePane(ctx, p)
	if err != nil {
		return nil, errors.Join(err, client.CloseContext(ctx))
	}
	return observation, nil
}

// observePane stages the capture through an ephemeral, uniquely named tmux
// buffer because printed pane content can imitate control protocol guards.
//
// The pane must select the control client's server. The returned observation
// borrows the client; closing the client ends its notification stream.
func (c *ControlClient) observePane(
	ctx context.Context,
	pane Pane,
) (*PaneObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pane.ID() == "" {
		return nil, invalidServerCommandRequest(
			"observe-pane", "Pane", "", "must have a materialized pane ID",
		)
	}
	if !pane.Server().Equal(c.server) {
		return nil, invalidServerCommandRequest(
			"observe-pane", "Pane", "[redacted]", "belongs to a different server selector",
		)
	}
	if pane.server.daemon != nil && c.server.daemon != nil &&
		!sameSnapshotIdentity(*pane.server.daemon, *c.server.daemon) {
		return nil, ErrDaemonReplaced
	}

	directory, err := os.MkdirTemp("", "libtmux-go-observe-")
	if err != nil {
		return nil, fmt.Errorf("create pane observation directory: %w", err)
	}
	path := filepath.Join(directory, "pane")
	defer func() {
		_ = os.Remove(path)
		_ = os.Remove(directory)
	}()

	buffer := "libtmux-go-observe-" + rand.Text()
	results, err := c.cmd(
		ctx,
		true,
		"refresh-client", "-A", pane.ID().String()+":on", ";",
		"list-windows", "-t", c.session.ID().String(), "-F", "#{window_id}", ";",
		"capture-pane", "-b", buffer, "-t", pane.ID().String(), ";",
		"save-buffer", "-b", buffer, "--", path, ";",
		"delete-buffer", "-b", buffer,
	)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("stage pane observation: %w", err),
			c.cleanupObservationBuffer(buffer),
		)
	}
	actions := []string{
		"enable pane observation output",
		"list attached-session windows",
		"capture pane observation",
		"save pane observation",
		"delete pane observation buffer",
	}
	for index, action := range actions[:min(len(results), len(actions))] {
		if results[index].Failed {
			failure := failedPaneObservationCommand(action, results[index])
			if index <= 2 {
				return nil, failure
			}
			return nil, errors.Join(
				failure,
				c.cleanupObservationBuffer(buffer),
			)
		}
	}
	if len(results) != len(actions) {
		var cleanupErr error
		if len(results) > 2 && !results[2].Failed {
			cleanupErr = c.cleanupObservationBuffer(buffer)
		}
		return nil, errors.Join(
			fmt.Errorf(
				"stage pane observation: got %d command results, want %d",
				len(results), len(actions),
			),
			cleanupErr,
		)
	}
	if !slices.Contains(
		tmuxcmd.SplitStdout(results[1].RawStdout),
		pane.WindowID().String(),
	) {
		return nil, invalidServerCommandRequest(
			"observe-pane",
			"Pane",
			"[redacted]",
			"window is not linked into the control client's attached session",
		)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pane observation: %w", err)
	}
	return &PaneObservation{
		client:    c,
		paneID:    pane.ID(),
		windowID:  pane.WindowID(),
		sessionID: c.session.ID(),
		after:     results[2].notificationSequence,
		baseline:  tmuxcmd.SplitStdout(contents),
		state:     newPaneObservationState(),
	}, nil
}

func (c *ControlClient) cleanupObservationBuffer(buffer string) error {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		controlClientStopGrace,
	)
	defer cancel()
	if err := c.server.DeleteBuffer(ctx, &buffer); err != nil {
		return fmt.Errorf("clean pane observation buffer: %w", err)
	}
	return nil
}

func failedPaneObservationCommand(
	action string,
	result ControlCommandResult,
) error {
	detail := strings.TrimSpace(string(result.RawStdout))
	if detail == "" {
		detail = "tmux rejected the command"
	}
	return fmt.Errorf("%s: %s", action, detail)
}
