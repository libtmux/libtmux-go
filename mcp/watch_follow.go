package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// watch retries initial and dropped connections until the last subscription
// cancels it.
func (w *watchers) watch(generation *watchGeneration) {
	for built := generation.restoring; generation.ctx.Err() == nil; {
		attended := w.follow(generation, built)
		built = built || attended
		// Restore a previously live set sooner than an initial empty server.
		wait := watchRetryInterval
		if attended {
			wait = watchRebuildInterval
		}
		select {
		case <-generation.ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (w *watchers) retire(generation *watchGeneration) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.active != generation {
		close(generation.done)
		return
	}
	w.active = nil
	runtimeDone := w.runtime != nil && w.runtime.ctx.Err() != nil
	if w.closed || runtimeDone || len(w.subscribed) == 0 {
		if w.nextReady != nil {
			close(w.nextReady)
			w.nextReady = nil
		}
	} else {
		ready := w.nextReady
		w.nextReady = nil
		restoring := false
		select {
		case <-generation.ready:
			restoring = true
		default:
		}
		w.startLocked(ready, restoring)
	}
	close(generation.done)
}

func (w *watchers) follow(generation *watchGeneration, rebuilt bool) bool {
	ctx := generation.ctx
	acquired, err := w.runtime.acquire(ctx)
	if err != nil {
		return false
	}
	defer acquired.release()
	ctx = withAcquiredServer(ctx, acquired)
	command := acquired.server
	process, err := w.runtime.process(ctx)
	if err != nil {
		return false
	}
	sessions, err := w.attending(ctx, command)
	if err != nil || len(sessions) == 0 {
		w.runtime.observe(err)
		return false
	}

	// tmux reports pane output only to clients attached to the owning session.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rebuild := &watchRebuild{cancel: cancel}
	w.mutex.Lock()
	if w.active != generation || generation.stopping {
		w.mutex.Unlock()
		return false
	}
	generation.rebuild = rebuild
	generation.attaching = len(sessions)
	w.mutex.Unlock()
	defer w.clearRebuild(generation, rebuild)

	var attending sync.WaitGroup
	var invalidated sync.Once
	invalidate := func() {
		invalidated.Do(func() { w.invalidateGenerationLoss(generation) })
	}
	for _, session := range sessions {
		attending.Add(1)
		go func() {
			defer attending.Done()
			// Any connection ending rebuilds the session set.
			defer cancel()
			w.followSession(ctx, generation, process, session, rebuilt, invalidate)
		}()
	}
	attending.Wait()
	return true
}

func (w *watchers) clearRebuild(
	generation *watchGeneration,
	rebuild *watchRebuild,
) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.active == generation && generation.rebuild == rebuild {
		generation.rebuild = nil
	}
}

// attending selects every session needed by the current resource set.
func (w *watchers) attending(ctx context.Context, server tmux.Server) ([]tmux.Session, error) {
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return nil, err
	}
	wanted, metadata := w.watchSelection()
	if metadata {
		return sessions, nil
	}
	if len(wanted) == 0 {
		return sessions[:1], nil
	}
	// Do not guess a watched pane's owning session.
	panes, err := server.Panes(ctx)
	if err != nil {
		return nil, err
	}
	owning := map[tmux.SessionID]struct{}{}
	resolved := map[string]struct{}{}
	for _, pane := range panes {
		if _, ok := wanted[pane.ID().String()]; ok {
			owning[pane.SessionID()] = struct{}{}
			resolved[pane.ID().String()] = struct{}{}
		}
	}
	if len(resolved) != len(wanted) {
		return sessions, nil
	}
	chosen := make([]tmux.Session, 0, len(owning))
	for _, session := range sessions {
		if _, ok := owning[session.ID()]; ok {
			chosen = append(chosen, session)
		}
	}
	return chosen, nil
}

func (w *watchers) watchSelection() (map[string]struct{}, bool) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	panes := map[string]struct{}{}
	metadata := false
	for uri := range w.subscribed {
		if id, ok := paneOfContentURI(uri); ok {
			panes[id] = struct{}{}
		} else {
			metadata = true
		}
	}
	return panes, metadata
}

func (w *watchers) followSession(
	ctx context.Context,
	generation *watchGeneration,
	server tmux.Server,
	session tmux.Session,
	rebuilt bool,
	invalidate func(),
) {
	control, err := server.OpenControl(ctx, session)
	if err != nil {
		w.runtime.observe(err)
		return
	}
	defer func() { _ = control.Close() }()
	defer invalidate()
	attached := w.attached(generation)
	if rebuilt && attached {
		w.tellEveryone()
	}

	for {
		notification, err := control.NextNotification(ctx)
		if err != nil {
			w.runtime.observe(err)
			return
		}
		if w.handleNotification(notification, invalidate) {
			return
		}
	}
}

func (w *watchers) handleNotification(
	notification tmux.ControlNotification,
	invalidate func(),
) bool {
	// Rebuild so newly created sessions can gain a watcher. Invalidate first:
	// the changed set may leave no session that can complete the next rebuild.
	if notification.Kind() == tmux.ControlNotificationSessionsChanged {
		invalidate()
		return true
	}
	for _, uri := range w.affected(notification) {
		w.notify(uri)
	}
	return false
}

func (w *watchers) invalidateGenerationLoss(generation *watchGeneration) {
	w.mutex.Lock()
	live := w.active == generation && !generation.stopping &&
		generation.ctx.Err() == nil
	if live {
		select {
		case <-generation.ready:
		default:
			live = false
		}
	}
	w.mutex.Unlock()
	if live {
		w.tellEveryone()
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
		// Copy mode changes pane content reads without pane output.
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
		// These notifications do not change a subscribable resource.
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
