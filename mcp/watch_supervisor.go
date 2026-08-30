package mcp

import (
	"context"
	"errors"
	"time"
)

type watchPlanFunc func(context.Context, watchSelection) (watchPlan, error)

type watchOpenFunc func(context.Context, watchPlan, *watchObserverSet) error

// watch owns one revisioned supervisor until its last subscription disappears.
func (w *watchers) watch(generation *watchGeneration) {
	defer func() {
		w.mutex.Lock()
		current := generation.current
		opening := generation.opening
		generation.current = nil
		generation.opening = nil
		w.mutex.Unlock()
		_ = w.stopWatchObservers(current, opening)
	}()

	retry := time.Duration(-1)
	for w.awaitWatchWork(generation, retry) {
		retry = w.reconcileWatch(generation)
	}
}

func (w *watchers) awaitWatchWork(
	generation *watchGeneration,
	retry time.Duration,
) bool {
	if retry < 0 {
		select {
		case <-generation.ctx.Done():
			return false
		case <-generation.changes:
		}
	} else if retry > 0 {
		timer := time.NewTimer(retry)
		select {
		case <-generation.ctx.Done():
			timer.Stop()
			return false
		case <-generation.changes:
			timer.Stop()
		case <-timer.C:
		}
	}

	debounce := time.NewTimer(watchDebounceInterval)
	defer debounce.Stop()
	for {
		select {
		case <-generation.ctx.Done():
			return false
		case <-generation.changes:
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce.Reset(watchDebounceInterval)
		case <-debounce.C:
			return true
		}
	}
}

func (w *watchers) reconcileWatch(generation *watchGeneration) time.Duration {
	revision, selection, force, live := w.watchSnapshot(generation)
	if !live {
		return -1
	}
	plan, err := w.plan(generation.ctx, selection)
	if err != nil || plan.empty() {
		w.observeWatchError(err)
		return watchRetryInterval
	}
	if !force && w.publishUnchangedWatch(generation, revision, plan) {
		return -1
	}
	candidate := newWatchObserverSet(generation.ctx, plan)
	if !w.beginWatchOpening(generation, revision, candidate) {
		_ = w.stopWatchObservers(candidate)
		return 0
	}
	if err := w.open(generation.ctx, plan, candidate); err != nil {
		_ = w.discardWatchOpening(generation, candidate)
		w.observeWatchError(err)
		return watchRetryInterval
	}
	if !w.watchRevisionCurrent(generation, revision) {
		_ = w.discardWatchOpening(generation, candidate)
		return 0
	}
	confirmed, err := w.plan(generation.ctx, selection)
	if err != nil {
		_ = w.discardWatchOpening(generation, candidate)
		w.observeWatchError(err)
		return watchRetryInterval
	}
	if !plan.equal(confirmed) || !w.watchRevisionCurrent(generation, revision) {
		_ = w.discardWatchOpening(generation, candidate)
		return 0
	}

	old, published := w.publishWatch(generation, revision, candidate)
	if !published {
		_ = w.discardWatchOpening(generation, candidate)
		return 0
	}
	candidate.start(w, generation)
	w.tellEveryone()
	_ = w.stopWatchObservers(old)
	return -1
}

func (w *watchers) beginWatchOpening(
	generation *watchGeneration,
	revision uint64,
	candidate *watchObserverSet,
) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if !w.watchRevisionCurrentLocked(generation, revision) ||
		generation.opening != nil {
		return false
	}
	generation.opening = candidate
	return true
}

func (w *watchers) discardWatchOpening(
	generation *watchGeneration,
	candidate *watchObserverSet,
) error {
	candidate.signalStop()
	w.mutex.Lock()
	if generation.opening == candidate {
		generation.opening = nil
	}
	w.mutex.Unlock()
	return w.stopWatchObservers(candidate)
}

func (w *watchers) watchSnapshot(
	generation *watchGeneration,
) (uint64, watchSelection, bool, bool) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.active != generation || generation.stopping || len(w.subscribed) == 0 {
		return 0, watchSelection{}, false, false
	}
	return w.revision, w.watchSelectionLocked(), generation.force, true
}

func (w *watchers) watchRevisionCurrent(
	generation *watchGeneration,
	revision uint64,
) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.watchRevisionCurrentLocked(generation, revision)
}

// watchRevisionCurrentLocked requires w.mutex.
func (w *watchers) watchRevisionCurrentLocked(
	generation *watchGeneration,
	revision uint64,
) bool {
	return w.active == generation && !generation.stopping &&
		w.revision == revision && generation.ctx.Err() == nil
}

func (w *watchers) publishWatch(
	generation *watchGeneration,
	revision uint64,
	candidate *watchObserverSet,
) (*watchObserverSet, bool) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if !w.watchRevisionCurrentLocked(generation, revision) ||
		generation.opening != candidate {
		return nil, false
	}
	old := generation.current
	generation.current = candidate
	generation.opening = nil
	generation.force = false
	w.closeWatchWaitersLocked(generation, revision)
	return old, true
}

func (w *watchers) publishUnchangedWatch(
	generation *watchGeneration,
	revision uint64,
	plan watchPlan,
) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if !w.watchRevisionCurrentLocked(generation, revision) || generation.force ||
		generation.current == nil || !generation.current.plan.equal(plan) {
		return false
	}
	w.closeWatchWaitersLocked(generation, revision)
	return true
}

// closeWatchWaitersLocked requires w.mutex.
func (w *watchers) closeWatchWaitersLocked(
	generation *watchGeneration,
	revision uint64,
) {
	remaining := generation.waiters[:0]
	for _, waiter := range generation.waiters {
		if waiter.revision <= revision {
			close(waiter.ready)
		} else {
			remaining = append(remaining, waiter)
		}
	}
	generation.waiters = remaining
}

func (w *watchers) planWithRuntime(
	ctx context.Context,
	selection watchSelection,
) (watchPlan, error) {
	acquired, err := w.runtime.acquire(ctx)
	if err != nil {
		return watchPlan{}, err
	}
	defer acquired.release()
	ctx = withAcquiredServer(ctx, acquired)
	topology, err := readWatchTopology(ctx, acquired.server, len(selection.panes) != 0)
	if err != nil {
		return watchPlan{}, err
	}
	return projectWatchPlan(selection, topology), nil
}

func (w *watchers) openWithRuntime(
	ctx context.Context,
	plan watchPlan,
	set *watchObserverSet,
) error {
	server, err := w.runtime.process(ctx)
	if err != nil {
		return err
	}
	for _, projection := range plan.projections {
		for _, target := range projection.sessions {
			stream, openErr := server.OpenNotifications(
				ctx,
				target.session,
				projection.options,
			)
			if openErr != nil {
				return openErr
			}
			set.add(watchObserver{
				stream:         stream,
				initialSession: target.id,
			})
		}
	}
	return nil
}

func (w *watchers) stopWatchObservers(sets ...*watchObserverSet) error {
	for _, set := range sets {
		set.signalStop()
	}
	w.mutex.Lock()
	within := w.shutdownWait
	if within <= 0 {
		within = watchShutdownWait
	}
	deadline := time.Now().Add(within)
	if w.closed {
		deadline = w.shutdownDeadlineLocked()
	}
	w.mutex.Unlock()
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	var closeErr error
	for _, set := range sets {
		closeErr = errors.Join(closeErr, set.stop(ctx))
	}
	return closeErr
}

func (w *watchers) observeWatchError(err error) {
	if err != nil && w.runtime != nil {
		w.runtime.observe(err)
	}
}

func (w *watchers) requestWatchReplan(
	generation *watchGeneration,
	set *watchObserverSet,
	force bool,
) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.active != generation || generation.stopping || generation.current != set {
		return false
	}
	w.revision++
	generation.force = generation.force || force
	w.signalLocked(generation)
	return true
}

// watchSelectionLocked requires w.mutex.
func (w *watchers) watchSelectionLocked() watchSelection {
	selection := watchSelection{panes: map[string]struct{}{}}
	for uri := range w.subscribed {
		if id, ok := paneOfContentURI(uri); ok {
			selection.panes[id] = struct{}{}
		} else {
			selection.metadata = true
		}
	}
	return selection
}

func (w *watchers) retire(generation *watchGeneration) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	for _, waiter := range generation.waiters {
		close(waiter.ready)
	}
	generation.waiters = nil
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
		w.startLocked(ready)
	}
	close(generation.done)
}
