package tmux

import "context"

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
	client, err := s.openControl(ctx, session, profile)
	if err != nil {
		return nil, err
	}
	return &NotificationStream{client: client}, nil
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
