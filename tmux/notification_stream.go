package tmux

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// NotificationStream owns one observation-only control client attached to a
// session. It yields tmux notifications in wire order and must be closed.
// Before tmux 3.6, destroying the attached session follows that session's
// detach-on-destroy policy and may end the stream.
type NotificationStream struct {
	client *ControlClient
}

// NotificationOptions configures which notifications a stream receives. The
// zero value retains notifications other than pane output.
type NotificationOptions struct {
	// IncludePaneOutput includes output and extended-output notifications.
	IncludePaneOutput bool
	// PauseAfter asks tmux to hold a pane's output once this stream is that
	// far behind on it, and to say so with a pause notification. Zero leaves
	// tmux's own behavior, which ends a stream that falls five minutes behind
	// rather than pausing it. tmux resolves this to whole seconds, and it
	// governs pane output, so it requires IncludePaneOutput.
	PauseAfter time.Duration
}

func (o NotificationOptions) validate() error {
	if o.PauseAfter == 0 {
		return nil
	}
	if o.PauseAfter < time.Second {
		return invalidServerCommandRequest(
			"refresh-client", "PauseAfter", o.PauseAfter.String(),
			"is below the whole second tmux resolves this flag to",
		)
	}
	if !o.IncludePaneOutput {
		return invalidServerCommandRequest(
			"refresh-client", "PauseAfter", o.PauseAfter.String(),
			"holds pane output, which this stream does not receive without IncludePaneOutput",
		)
	}
	return nil
}

// OpenNotifications starts an owned observation-only stream attached to
// session. Session must belong to the same configured server selector. A
// connection-bound server returns [ErrConnectionRequiresProcess].
func (s Server) OpenNotifications(
	ctx context.Context,
	session Session,
	options NotificationOptions,
) (*NotificationStream, error) {
	profile := controlNotificationsNoPaneOutput
	if options.IncludePaneOutput {
		profile = controlNotificationsFull
	}
	if err := options.validate(); err != nil {
		return nil, err
	}
	client, err := s.openControl(ctx, session, profile)
	if err != nil {
		return nil, err
	}
	stream := &NotificationStream{client: client}
	if options.PauseAfter > 0 {
		if err := stream.armPause(ctx, options.PauseAfter); err != nil {
			return nil, errors.Join(err, client.CloseContext(ctx))
		}
	}
	return stream, nil
}

// armPause arms tmux's per-pane hold for this stream's own client.
func (s *NotificationStream) armPause(ctx context.Context, after time.Duration) error {
	_, err := s.client.Cmd(ctx, "refresh-client", "-f",
		"pause-after="+strconv.Itoa(int(after/time.Second)))
	return err
}

// ContinuePane resumes pane output tmux held after this stream fell behind on
// it. tmux does not replay what it held, so read the pane to recover that span
// rather than waiting for it. Resuming a pane tmux never paused does nothing
// and reports nothing, because tmux answers neither.
func (s *NotificationStream) ContinuePane(ctx context.Context, pane PaneID) error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	// tmux discards a resume argument it cannot parse without reporting it, so
	// an unusable identifier would leave the pane held with nothing to show why.
	if !resumablePaneID(pane) {
		return invalidServerCommandRequest(
			"refresh-client", "Pane", pane.String(),
			"is not a pane identifier tmux will resume, such as %1",
		)
	}
	_, err := s.client.Cmd(ctx, "refresh-client", "-A", pane.String()+":continue")
	return err
}

// resumablePaneID reports whether tmux's resume parser accepts id, which needs
// a per cent sign and then digits.
func resumablePaneID(id PaneID) bool {
	digits, ok := strings.CutPrefix(string(id), "%")
	if !ok || digits == "" {
		return false
	}
	return !strings.ContainsFunc(digits, func(r rune) bool { return r < '0' || r > '9' })
}

// OpenNotifications starts an owned notification stream attached to the
// receiver. It does not change the receiver's transport or expose control
// commands. A connection-bound receiver returns [ErrConnectionRequiresProcess].
func (s Session) OpenNotifications(
	ctx context.Context,
	options NotificationOptions,
) (*NotificationStream, error) {
	return s.server.OpenNotifications(ctx, s, options)
}

// Next returns the next ordered control-mode notification. Exactly one caller
// may use Next at a time.
func (s *NotificationStream) Next(
	ctx context.Context,
) (ControlNotification, error) {
	if s == nil || s.client == nil {
		return ControlNotification{}, ErrControlClosed
	}
	return s.client.NextNotification(ctx)
}

// CloseContext starts idempotent stream shutdown and waits within ctx. The
// context bounds only the wait; a later call may resume waiting for shutdown.
func (s *NotificationStream) CloseContext(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	return s.client.CloseContext(ctx)
}

// Close stops the notification stream. It is safe to call concurrently and
// more than once.
func (s *NotificationStream) Close() error {
	if s == nil || s.client == nil {
		return ErrControlClosed
	}
	return s.client.Close()
}
