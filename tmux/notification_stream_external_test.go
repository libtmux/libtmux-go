package tmux_test

import (
	"context"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

type openNotificationsSignature func(
	tmux.Session,
	context.Context,
) (*tmux.NotificationStream, error)

type nextNotificationStreamSignature func(
	*tmux.NotificationStream,
	context.Context,
) (tmux.ControlNotification, error)

type closeNotificationStreamContextSignature func(
	*tmux.NotificationStream,
	context.Context,
) error

type closeNotificationStreamSignature func(*tmux.NotificationStream) error

func TestNotificationStreamPublicSurfaceCompiles(_ *testing.T) {
	var _ openNotificationsSignature = tmux.Session.OpenNotifications
	var _ nextNotificationStreamSignature = (*tmux.NotificationStream).Next
	var _ closeNotificationStreamContextSignature = (*tmux.NotificationStream).CloseContext
	var _ closeNotificationStreamSignature = (*tmux.NotificationStream).Close
}
