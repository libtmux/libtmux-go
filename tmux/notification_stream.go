package tmux

import "context"

// NotificationStream owns one observation-only control client attached to a
// session. It yields tmux notifications in wire order and must be closed.
type NotificationStream struct {
	client *ControlClient
}

// OpenNotifications starts an owned notification stream attached to the
// receiver. It does not change the receiver's transport or expose control
// commands.
func (s Session) OpenNotifications(
	ctx context.Context,
) (*NotificationStream, error) {
	client, err := s.server.openControl(ctx, s, controlNotificationsRetained)
	if err != nil {
		return nil, err
	}
	return &NotificationStream{client: client}, nil
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
