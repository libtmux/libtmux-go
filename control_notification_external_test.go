package tmux_test

import (
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go"
)

// libtmux:parity libtmux._internal.constants.Hooks
// libtmux:parity libtmux._internal.constants.Hooks.client_detached_control
// libtmux:parity libtmux._internal.constants.Hooks.client_session_changed_control
// libtmux:parity libtmux._internal.constants.Hooks.config_error
// libtmux:parity libtmux._internal.constants.Hooks.continue_control
// libtmux:parity libtmux._internal.constants.Hooks.exit_control
// libtmux:parity libtmux._internal.constants.Hooks.extended_output
// libtmux:parity libtmux._internal.constants.Hooks.layout_change
// libtmux:parity libtmux._internal.constants.Hooks.message_control
// libtmux:parity libtmux._internal.constants.Hooks.output
// libtmux:parity libtmux._internal.constants.Hooks.pane_mode_changed
// libtmux:parity libtmux._internal.constants.Hooks.paste_buffer_changed
// libtmux:parity libtmux._internal.constants.Hooks.paste_buffer_deleted
// libtmux:parity libtmux._internal.constants.Hooks.pause_control
// libtmux:parity libtmux._internal.constants.Hooks.session_changed_control
// libtmux:parity libtmux._internal.constants.Hooks.session_renamed_control
// libtmux:parity libtmux._internal.constants.Hooks.session_window_changed
// libtmux:parity libtmux._internal.constants.Hooks.sessions_changed
// libtmux:parity libtmux._internal.constants.Hooks.subscription_changed
// libtmux:parity libtmux._internal.constants.Hooks.unlinked_window_add
// libtmux:parity libtmux._internal.constants.Hooks.unlinked_window_close
// libtmux:parity libtmux._internal.constants.Hooks.unlinked_window_renamed
// libtmux:parity libtmux._internal.constants.Hooks.window_add
// libtmux:parity libtmux._internal.constants.Hooks.window_close
// libtmux:parity libtmux._internal.constants.Hooks.window_pane_changed
// libtmux:parity libtmux._internal.constants.Hooks.window_renamed_control
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
