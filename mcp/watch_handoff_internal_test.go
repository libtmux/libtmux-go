package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestWatchHandoffPublishesCandidateBeforeClosingOldObservers(t *testing.T) {
	oldStream := newFakeWatchStream()
	candidateStream := newFakeWatchStream()
	attaching := make(chan struct{})
	release := make(chan struct{})
	var opens int
	var watchers *watchers
	planner := func(_ context.Context, selection watchSelection) (watchPlan, error) {
		if len(selection.panes) == 1 && !selection.metadata {
			return handoffTestPlan("$1"), nil
		}
		return handoffTestPlan("$2"), nil
	}
	opener := func(ctx context.Context, _ watchPlan, candidate *watchObserverSet) error {
		opens++
		if opens == 1 {
			candidate.add(watchObserver{stream: oldStream})
			return nil
		}
		close(attaching)
		select {
		case <-release:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		oldStream.onClose = func() {
			watchers.mutex.Lock()
			defer watchers.mutex.Unlock()
			if watchers.active == nil || watchers.active.current == nil ||
				!watchers.active.current.plan.equal(handoffTestPlan("$2")) {
				t.Error("old observer closed before the candidate was published")
			}
		}
		candidate.add(watchObserver{stream: candidateStream})
		return nil
	}
	watchers = newHandoffTestWatchers(t, planner, opener)

	ready, err := watchers.add(paneContentURI("%7"), paneContentURI("%7"))
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, ready, "initial observer readiness")

	nextReady, err := watchers.add(resourceSessions, resourceSessions)
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, attaching, "candidate attachment")
	assertWatchSignalOpen(t, oldStream.closed, "old observer closed while candidate attachment was blocked")
	close(release)
	waitWatchSignal(t, nextReady, "replacement observer readiness")
	waitWatchSignal(t, oldStream.closed, "old observer close after publication")
	assertWatchSignalOpen(t, candidateStream.closed, "published candidate was closed")
	if watchers.at(resourceSessions).IsZero() {
		t.Fatal("handoff publication did not invalidate the newly watched resource")
	}
}

func TestWatchHandoffDiscardsCandidateWhenRevisionChangesDuringAttach(t *testing.T) {
	oldStream := newFakeWatchStream()
	staleStream := newFakeWatchStream()
	attaching := make(chan struct{})
	release := make(chan struct{})
	thirdAttach := make(chan struct{})
	var mutex sync.Mutex
	opens := 0
	planner := func(_ context.Context, selection watchSelection) (watchPlan, error) {
		return handoffTestPlan(tmux.SessionID("$" + string(rune('1'+len(selection.panes))))), nil
	}
	opener := func(ctx context.Context, _ watchPlan, candidate *watchObserverSet) error {
		mutex.Lock()
		opens++
		call := opens
		mutex.Unlock()
		switch call {
		case 1:
			candidate.add(watchObserver{stream: oldStream})
			return nil
		case 2:
			close(attaching)
			select {
			case <-release:
				candidate.add(watchObserver{stream: staleStream})
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		default:
			select {
			case <-thirdAttach:
			default:
				close(thirdAttach)
			}
			<-ctx.Done()
			return context.Cause(ctx)
		}
	}
	watchers := newHandoffTestWatchers(t, planner, opener)

	ready, err := watchers.add(paneContentURI("%7"), paneContentURI("%7"))
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, ready, "initial observer readiness")
	if _, err := watchers.add(paneContentURI("%8"), paneContentURI("%8")); err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, attaching, "stale candidate attachment")
	latestReady, err := watchers.add(paneContentURI("%9"), paneContentURI("%9"))
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	waitWatchSignal(t, staleStream.closed, "stale candidate close")
	waitWatchSignal(t, thirdAttach, "latest candidate attachment")
	assertWatchSignalOpen(t, latestReady, "latest revision became ready while its attachment blocked")
	assertWatchSignalOpen(t, oldStream.closed, "old observer closed with stale candidate")

	watchers.mutex.Lock()
	current := watchers.active.current
	watchers.mutex.Unlock()
	if current == nil || !current.plan.equal(handoffTestPlan("$2")) {
		t.Fatalf("published plan after stale attach = %#v, want original plan", current)
	}
}

func TestInitialWatchAttachReplansBeforePublication(t *testing.T) {
	staleStream := newFakeWatchStream()
	currentStream := newFakeWatchStream()
	var mutex sync.Mutex
	plans := []watchPlan{
		handoffTestPlan("$1"), // initial plan
		handoffTestPlan("$2"), // validation rejects the first candidate
		handoffTestPlan("$2"), // retry plan
		handoffTestPlan("$2"), // validation accepts the retry
	}
	planner := func(context.Context, watchSelection) (watchPlan, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if len(plans) == 0 {
			return handoffTestPlan("$2"), nil
		}
		plan := plans[0]
		plans = plans[1:]
		return plan, nil
	}
	opener := func(_ context.Context, plan watchPlan, candidate *watchObserverSet) error {
		stream := watchNotificationStream(currentStream)
		if plan.equal(handoffTestPlan("$1")) {
			stream = staleStream
		}
		candidate.add(watchObserver{stream: stream})
		return nil
	}
	watchers := newHandoffTestWatchers(t, planner, opener)

	ready, err := watchers.add(resourceSessions, resourceSessions)
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, ready, "validated initial readiness")
	waitWatchSignal(t, staleStream.closed, "stale initial candidate close")
	assertWatchSignalOpen(t, currentStream.closed, "validated initial observer closed")

	watchers.mutex.Lock()
	current := watchers.active.current
	watchers.mutex.Unlock()
	if current == nil || !current.plan.equal(handoffTestPlan("$2")) {
		t.Fatalf("initial published plan = %#v, want replanned $2", current)
	}
}

func TestUnchangedTopologyPlanKeepsCurrentObservers(t *testing.T) {
	stream := newFakeWatchStream()
	replanned := make(chan struct{})
	var counts sync.Mutex
	plans, opens := 0, 0
	planner := func(context.Context, watchSelection) (watchPlan, error) {
		counts.Lock()
		defer counts.Unlock()
		plans++
		if plans == 3 {
			close(replanned)
		}
		return handoffTestPlan("$1"), nil
	}
	opener := func(_ context.Context, _ watchPlan, candidate *watchObserverSet) error {
		counts.Lock()
		opens++
		counts.Unlock()
		candidate.add(watchObserver{stream: stream})
		return nil
	}
	watchers := newHandoffTestWatchers(t, planner, opener)
	ready, err := watchers.add(resourceSessions, resourceSessions)
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, ready, "initial observer readiness")

	watchers.mutex.Lock()
	generation, current := watchers.active, watchers.active.current
	watchers.mutex.Unlock()
	notification, err := tmux.ParseControlNotification([]byte(
		"%layout-change @3 tiled tiled *",
	))
	if err != nil {
		t.Fatal(err)
	}
	watchers.handleNotification(generation, current, notification)
	waitWatchSignal(t, replanned, "unchanged topology replan")
	counts.Lock()
	defer counts.Unlock()
	if opens != 1 {
		t.Fatalf("observer sets opened = %d, want the original set only", opens)
	}
	assertWatchSignalOpen(t, stream.closed, "unchanged topology closed its observer")
}

func TestWatchStreamFailureForcesReplacementWithAnUnchangedPlan(t *testing.T) {
	failure := errors.New("test notification stream failure")
	failed := &triggeredErrorWatchStream{
		fail:   make(chan struct{}),
		closed: make(chan struct{}),
		err:    failure,
	}
	replacement := newFakeWatchStream()
	replacementOpened := make(chan struct{})
	var opens int
	opener := func(_ context.Context, _ watchPlan, candidate *watchObserverSet) error {
		opens++
		stream := watchNotificationStream(failed)
		if opens > 1 {
			stream = replacement
			close(replacementOpened)
		}
		candidate.add(watchObserver{stream: stream})
		return nil
	}
	watchers := newHandoffTestWatchers(
		t,
		func(context.Context, watchSelection) (watchPlan, error) {
			return handoffTestPlan("$1"), nil
		},
		opener,
	)
	ready, err := watchers.add(resourceSessions, resourceSessions)
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, ready, "initial observer readiness")
	before := watchers.at(resourceSessions)
	close(failed.fail)
	waitWatchSignal(t, replacementOpened, "replacement after stream failure")
	waitWatchSignal(t, failed.closed, "failed observer close after replacement")
	if !watchers.at(resourceSessions).After(before) {
		t.Fatal("stream failure replacement did not invalidate the watched resource")
	}
	assertWatchSignalOpen(t, replacement.closed, "replacement observer closed")
}

func TestWatchParseErrorKeepsTheStreamAndDeliversTheNextNotification(t *testing.T) {
	stream := &sequenceWatchStream{
		results: make(chan watchStreamResult, 2),
		closed:  make(chan struct{}),
	}
	replacement := newFakeWatchStream()
	var mutex sync.Mutex
	opens := 0
	opener := func(_ context.Context, _ watchPlan, candidate *watchObserverSet) error {
		mutex.Lock()
		opens++
		call := opens
		mutex.Unlock()
		observer := watchNotificationStream(stream)
		if call > 1 {
			observer = replacement
		}
		candidate.add(watchObserver{stream: observer})
		return nil
	}
	watchers := newHandoffTestWatchers(
		t,
		func(context.Context, watchSelection) (watchPlan, error) {
			return handoffTestPlan("$1"), nil
		},
		opener,
	)
	ready, err := watchers.add(resourceSessions, resourceSessions)
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, ready, "initial observer readiness")
	before := watchers.at(resourceSessions)
	stream.results <- watchStreamResult{err: &tmux.ControlNotificationError{
		Category: tmux.ErrUnknownControlNotification,
		Reason:   "test future notification",
	}}
	afterParse := waitWatchTimestampAfter(t, watchers, resourceSessions, before)
	notification, err := tmux.ParseControlNotification([]byte(
		"%session-renamed $1 renamed",
	))
	if err != nil {
		t.Fatal(err)
	}
	stream.results <- watchStreamResult{notification: notification}
	waitWatchTimestampAfter(t, watchers, resourceSessions, afterParse)
	mutex.Lock()
	defer mutex.Unlock()
	if opens != 1 {
		t.Fatalf("observer sets opened after parse error = %d, want 1", opens)
	}
	assertWatchSignalOpen(t, stream.closed, "parse error closed the current observer")
}

func TestOnlyOwnershipChangingNotificationsRequestWatchReplan(t *testing.T) {
	t.Parallel()

	for _, kind := range []tmux.ControlNotificationKind{
		tmux.ControlNotificationLayoutChange,
		tmux.ControlNotificationWindowAdd,
		tmux.ControlNotificationWindowClose,
		tmux.ControlNotificationUnlinkedWindowAdd,
		tmux.ControlNotificationUnlinkedWindowClose,
		tmux.ControlNotificationSessionsChanged,
		tmux.ControlNotificationSessionChanged,
	} {
		if !ownershipChangingNotification(kind) {
			t.Errorf("%s did not request replanning", kind)
		}
	}
	for _, kind := range []tmux.ControlNotificationKind{
		tmux.ControlNotificationSessionRenamed,
		tmux.ControlNotificationWindowRenamed,
		tmux.ControlNotificationUnlinkedWindowRenamed,
		tmux.ControlNotificationWindowPaneChanged,
		tmux.ControlNotificationSessionWindowChanged,
	} {
		if ownershipChangingNotification(kind) {
			t.Errorf("%s requested topology replanning", kind)
		}
	}
}

func TestWatchObserverIgnoresItsInitialSessionChangeButAcceptsAHop(t *testing.T) {
	t.Parallel()

	observer := watchObserver{initialSession: "$1"}
	const uri = resourceSessions
	generation := &watchGeneration{changes: make(chan struct{}, 1)}
	set := &watchObserverSet{}
	generation.current = set
	watchers := &watchers{
		subscribed: map[string]int{uri: 1},
		notified:   map[string]time.Time{},
		owed:       map[string]*time.Timer{},
		routes:     map[string]map[*watchDelivery]map[string]*watchRoute{},
		active:     generation,
	}
	deliver := func(notification tmux.ControlNotification) {
		if observer.accept(notification) {
			watchers.handleNotification(generation, set, notification)
		}
	}
	initial, err := tmux.ParseControlNotification([]byte("%session-changed $1 initial"))
	if err != nil {
		t.Fatal(err)
	}
	deliver(initial)
	if watchers.revision != 0 || !watchers.at(uri).IsZero() {
		t.Fatal("initial attachment invalidated resources or requested replanning")
	}
	hop, err := tmux.ParseControlNotification([]byte("%session-changed $2 replacement"))
	if err != nil {
		t.Fatal(err)
	}
	deliver(hop)
	if watchers.revision != 1 || watchers.at(uri).IsZero() {
		t.Fatal("later attachment hop did not invalidate and request replanning")
	}
}

func TestWatchObserverShutdownSignalsAllBeforeOneJoinDeadline(t *testing.T) {
	firstSignal := make(chan struct{})
	secondSignal := make(chan struct{})
	set := newWatchObserverSet(t.Context(), watchPlan{})
	set.add(watchObserver{stream: &orderedStopStream{signaled: firstSignal, peer: secondSignal}})
	set.add(watchObserver{stream: &orderedStopStream{signaled: secondSignal, peer: firstSignal}})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := set.stop(ctx); err != nil {
		t.Fatalf("observer shutdown order: %v", err)
	}

	const budget = 20 * time.Millisecond
	blocking := newWatchObserverSet(t.Context(), watchPlan{})
	blocking.add(watchObserver{stream: blockingStopStream{}})
	blocking.add(watchObserver{stream: blockingStopStream{}})
	watchers := &watchers{shutdownWait: budget}
	started := time.Now()
	err := watchers.stopWatchObservers(blocking)
	if elapsed := time.Since(started); elapsed > 5*budget {
		t.Fatalf("two observer joins took %v, want one %v budget", elapsed, budget)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("observer shutdown error = %v, want deadline", err)
	}
}

func TestWatchObserverStopWaitsForItsSignalPassAndSignalsLateAdds(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	lateSignal := make(chan struct{})
	set := newWatchObserverSet(t.Context(), watchPlan{})
	set.add(watchObserver{stream: &blockingSignalStream{
		entered: entered,
		release: release,
	}})
	firstDone := make(chan struct{})
	go func() {
		set.signalStop()
		close(firstDone)
	}()
	waitWatchSignal(t, entered, "first observer signal")
	concurrentDone := make(chan struct{})
	go func() {
		set.signalStop()
		close(concurrentDone)
	}()
	select {
	case <-concurrentDone:
		t.Fatal("concurrent stop returned before signaling finished")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	waitWatchSignal(t, firstDone, "first signal pass completion")
	waitWatchSignal(t, concurrentDone, "concurrent signal pass completion")
	set.add(watchObserver{stream: &signalStopStream{signaled: lateSignal}})
	waitWatchSignal(t, lateSignal, "late candidate shutdown")
}

func TestWatchersCloseSignalsPublishedAndPartialObserversWhileReplacementOpenBlocks(t *testing.T) {
	oldStream := newFakeWatchStream()
	candidateStream := newFakeWatchStream()
	attaching := make(chan struct{})
	release := make(chan struct{})
	var opens int
	planner := func(_ context.Context, selection watchSelection) (watchPlan, error) {
		if selection.metadata {
			return handoffTestPlan("$2"), nil
		}
		return handoffTestPlan("$1"), nil
	}
	opener := func(_ context.Context, _ watchPlan, candidate *watchObserverSet) error {
		opens++
		if opens == 1 {
			candidate.add(watchObserver{stream: oldStream})
			return nil
		}
		candidate.add(watchObserver{stream: candidateStream})
		close(attaching)
		<-release // Model startup or cleanup that has not returned on cancellation yet.
		return nil
	}
	watchers := newHandoffTestWatchers(t, planner, opener)
	watchers.shutdownWait = 20 * time.Millisecond
	ready, err := watchers.add(paneContentURI("%7"), paneContentURI("%7"))
	if err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, ready, "initial observer readiness")
	if _, err := watchers.add(resourceSessions, resourceSessions); err != nil {
		t.Fatal(err)
	}
	waitWatchSignal(t, attaching, "replacement attachment")

	closed := make(chan struct{})
	go func() {
		watchers.close()
		close(closed)
	}()
	waitWatchSignal(t, oldStream.closed, "published observer close signal")
	waitWatchSignal(t, candidateStream.closed, "partial candidate close signal")
	waitWatchSignal(t, closed, "bounded watcher shutdown")
	close(release)
}

type fakeWatchStream struct {
	closed  chan struct{}
	onClose func()
	once    sync.Once
}

type orderedStopStream struct {
	signaled chan struct{}
	peer     <-chan struct{}
	once     sync.Once
}

func (s *orderedStopStream) Next(ctx context.Context) (tmux.ControlNotification, error) {
	<-ctx.Done()
	return tmux.ControlNotification{}, context.Cause(ctx)
}

func (s *orderedStopStream) CloseContext(ctx context.Context) error {
	if ctx.Err() != nil {
		s.once.Do(func() { close(s.signaled) })
		return ctx.Err()
	}
	select {
	case <-s.peer:
		return nil
	default:
		return errors.New("joined before every observer was signaled")
	}
}

type blockingStopStream struct{}

type blockingSignalStream struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

type triggeredErrorWatchStream struct {
	fail   chan struct{}
	closed chan struct{}
	err    error
	once   sync.Once
}

type watchStreamResult struct {
	notification tmux.ControlNotification
	err          error
}

type sequenceWatchStream struct {
	results chan watchStreamResult
	closed  chan struct{}
	once    sync.Once
}

type signalStopStream struct {
	signaled chan struct{}
	once     sync.Once
}

func (s *signalStopStream) Next(ctx context.Context) (tmux.ControlNotification, error) {
	<-ctx.Done()
	return tmux.ControlNotification{}, context.Cause(ctx)
}

func (s *signalStopStream) CloseContext(ctx context.Context) error {
	if ctx.Err() != nil {
		s.once.Do(func() { close(s.signaled) })
	}
	return ctx.Err()
}

func (blockingStopStream) Next(ctx context.Context) (tmux.ControlNotification, error) {
	<-ctx.Done()
	return tmux.ControlNotification{}, context.Cause(ctx)
}

func (s *triggeredErrorWatchStream) Next(
	ctx context.Context,
) (tmux.ControlNotification, error) {
	select {
	case <-s.fail:
		return tmux.ControlNotification{}, s.err
	case <-ctx.Done():
		return tmux.ControlNotification{}, context.Cause(ctx)
	}
}

func (s *triggeredErrorWatchStream) CloseContext(context.Context) error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *sequenceWatchStream) Next(
	ctx context.Context,
) (tmux.ControlNotification, error) {
	select {
	case result := <-s.results:
		return result.notification, result.err
	case <-ctx.Done():
		return tmux.ControlNotification{}, context.Cause(ctx)
	}
}

func (s *sequenceWatchStream) CloseContext(context.Context) error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (blockingStopStream) CloseContext(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *blockingSignalStream) Next(ctx context.Context) (tmux.ControlNotification, error) {
	<-ctx.Done()
	return tmux.ControlNotification{}, context.Cause(ctx)
}

func (s *blockingSignalStream) CloseContext(ctx context.Context) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return ctx.Err()
}

func newFakeWatchStream() *fakeWatchStream {
	return &fakeWatchStream{closed: make(chan struct{})}
}

func (s *fakeWatchStream) Next(ctx context.Context) (tmux.ControlNotification, error) {
	<-ctx.Done()
	return tmux.ControlNotification{}, context.Cause(ctx)
}

func (s *fakeWatchStream) CloseContext(context.Context) error {
	s.once.Do(func() {
		if s.onClose != nil {
			s.onClose()
		}
		close(s.closed)
	})
	return nil
}

func newHandoffTestWatchers(
	t *testing.T,
	planner watchPlanFunc,
	opener watchOpenFunc,
) *watchers {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	watchers := newWatchers(&tmuxRuntime{ctx: ctx})
	watchers.plan = planner
	watchers.open = opener
	t.Cleanup(func() {
		cancel()
		watchers.close()
	})
	return watchers
}

func handoffTestPlan(id tmux.SessionID) watchPlan {
	return watchPlan{projections: []watchProjection{{
		sessions: []watchTopologySession{{id: id}},
	}}}
}

func waitWatchSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertWatchSignalOpen(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal(failure)
	default:
	}
}

func waitWatchTimestampAfter(
	t *testing.T,
	watchers *watchers,
	uri string,
	before time.Time,
) time.Time {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		observed := watchers.at(uri)
		if observed.After(before) {
			return observed
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s invalidation", uri)
		case <-time.After(time.Millisecond):
		}
	}
}

var _ watchNotificationStream = (*fakeWatchStream)(nil)
