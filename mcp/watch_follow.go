package mcp

import (
	"errors"

	"github.com/libtmux/libtmux-go/tmux"
)

func (w *watchers) readWatchNotifications(
	generation *watchGeneration,
	set *watchObserverSet,
	observer *watchObserver,
) {
	for {
		notification, err := observer.stream.Next(set.ctx)
		if err != nil {
			if set.ctx.Err() != nil {
				return
			}
			var notificationErr *tmux.ControlNotificationError
			recoverable := errors.As(err, &notificationErr)
			if w.requestWatchReplan(generation, set, !recoverable) {
				w.observeWatchError(err)
				w.tellEveryone()
			}
			if recoverable {
				continue
			}
			return
		}
		if observer.accept(notification) {
			w.handleNotification(generation, set, notification)
		}
	}
}

func (observer *watchObserver) accept(notification tmux.ControlNotification) bool {
	if notification.Kind() != tmux.ControlNotificationSessionChanged ||
		observer.initialSession == "" {
		return true
	}
	initial := observer.initialSession
	observer.initialSession = ""
	arguments := notification.Arguments()
	return len(arguments) == 0 || arguments[0] != initial.String()
}

func (w *watchers) handleNotification(
	generation *watchGeneration,
	set *watchObserverSet,
	notification tmux.ControlNotification,
) {
	for _, uri := range w.affected(notification) {
		w.notify(uri)
	}
	if ownershipChangingNotification(notification.Kind()) {
		w.requestWatchReplan(
			generation,
			set,
			notification.Kind() == tmux.ControlNotificationSessionChanged,
		)
	}
}

func ownershipChangingNotification(kind tmux.ControlNotificationKind) bool {
	switch kind {
	case tmux.ControlNotificationLayoutChange,
		tmux.ControlNotificationWindowAdd,
		tmux.ControlNotificationWindowClose,
		tmux.ControlNotificationUnlinkedWindowAdd,
		tmux.ControlNotificationUnlinkedWindowClose,
		tmux.ControlNotificationSessionsChanged,
		tmux.ControlNotificationSessionChanged:
		return true
	case tmux.ControlNotificationClientDetached,
		tmux.ControlNotificationClientSessionChanged,
		tmux.ControlNotificationConfigError,
		tmux.ControlNotificationContinue,
		tmux.ControlNotificationExit,
		tmux.ControlNotificationExtendedOutput,
		tmux.ControlNotificationMessage,
		tmux.ControlNotificationOutput,
		tmux.ControlNotificationPaneModeChanged,
		tmux.ControlNotificationPasteBufferChanged,
		tmux.ControlNotificationPasteBufferDeleted,
		tmux.ControlNotificationPause,
		tmux.ControlNotificationSessionRenamed,
		tmux.ControlNotificationSessionWindowChanged,
		tmux.ControlNotificationSubscriptionChanged,
		tmux.ControlNotificationUnlinkedWindowRenamed,
		tmux.ControlNotificationWindowPaneChanged,
		tmux.ControlNotificationWindowRenamed:
		return false
	default:
		return false
	}
}

func (w *watchers) affected(notification tmux.ControlNotification) []string {
	switch notification.Kind() {
	case tmux.ControlNotificationOutput, tmux.ControlNotificationExtendedOutput:
		pane, _, ok := notification.Output()
		if !ok {
			return nil
		}
		return []string{paneContentURI(pane.String())}
	case tmux.ControlNotificationPaneModeChanged:
		arguments := notification.Arguments()
		if len(arguments) == 0 {
			return nil
		}
		return []string{paneContentURI(arguments[0])}
	case tmux.ControlNotificationLayoutChange,
		tmux.ControlNotificationWindowAdd,
		tmux.ControlNotificationWindowClose,
		tmux.ControlNotificationWindowRenamed,
		tmux.ControlNotificationWindowPaneChanged,
		tmux.ControlNotificationUnlinkedWindowAdd,
		tmux.ControlNotificationUnlinkedWindowClose,
		tmux.ControlNotificationUnlinkedWindowRenamed,
		tmux.ControlNotificationSessionsChanged,
		tmux.ControlNotificationSessionRenamed,
		tmux.ControlNotificationSessionChanged,
		tmux.ControlNotificationSessionWindowChanged:
		return w.watchedURIs()
	case tmux.ControlNotificationClientDetached,
		tmux.ControlNotificationClientSessionChanged,
		tmux.ControlNotificationConfigError,
		tmux.ControlNotificationContinue,
		tmux.ControlNotificationExit,
		tmux.ControlNotificationMessage,
		tmux.ControlNotificationPasteBufferChanged,
		tmux.ControlNotificationPasteBufferDeleted,
		tmux.ControlNotificationPause,
		tmux.ControlNotificationSubscriptionChanged:
		return nil
	default:
		return nil
	}
}

func (w *watchers) watchedURIs() []string {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	resources := make([]string, 0, len(w.subscribed))
	for uri := range w.subscribed {
		resources = append(resources, uri)
	}
	return resources
}
