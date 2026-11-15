package tmux_test

import (
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestControlNotificationPublicSurface(t *testing.T) {
	t.Parallel()

	kinds := []tmux.ControlNotificationKind{
		tmux.ControlNotificationClientDetached,
		tmux.ControlNotificationClientSessionChanged,
		tmux.ControlNotificationConfigError,
		tmux.ControlNotificationContinue,
		tmux.ControlNotificationExit,
		tmux.ControlNotificationExtendedOutput,
		tmux.ControlNotificationLayoutChange,
		tmux.ControlNotificationMessage,
		tmux.ControlNotificationOutput,
		tmux.ControlNotificationPaneModeChanged,
		tmux.ControlNotificationPasteBufferChanged,
		tmux.ControlNotificationPasteBufferDeleted,
		tmux.ControlNotificationPause,
		tmux.ControlNotificationSessionChanged,
		tmux.ControlNotificationSessionRenamed,
		tmux.ControlNotificationSessionWindowChanged,
		tmux.ControlNotificationSessionsChanged,
		tmux.ControlNotificationSubscriptionChanged,
		tmux.ControlNotificationUnlinkedWindowAdd,
		tmux.ControlNotificationUnlinkedWindowClose,
		tmux.ControlNotificationUnlinkedWindowRenamed,
		tmux.ControlNotificationWindowAdd,
		tmux.ControlNotificationWindowClose,
		tmux.ControlNotificationWindowPaneChanged,
		tmux.ControlNotificationWindowRenamed,
	}
	if len(kinds) != 25 {
		t.Fatalf("public notification kinds = %d, want 25", len(kinds))
	}
	notification, err := tmux.ParseControlNotification([]byte("%session-renamed $1 public name"))
	if err != nil {
		t.Fatalf("ParseControlNotification() error = %v", err)
	}
	if notification.Kind() != tmux.ControlNotificationSessionRenamed {
		t.Fatalf("Kind() = %q", notification.Kind())
	}
	if !slices.Equal(notification.Arguments(), []string{"$1", "public name"}) {
		t.Fatalf("Arguments() = %#v", notification.Arguments())
	}
	if _, ok := notification.Kind().MinimumVersion(); !ok {
		t.Fatal("MinimumVersion() reports unknown public kind")
	}
}
