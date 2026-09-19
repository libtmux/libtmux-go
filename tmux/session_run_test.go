package tmux

import (
	"context"
	"errors"
	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrimScreenKeepsOnlyWhatTheCommandShowed(t *testing.T) {
	tests := []struct {
		name        string
		lines       []string
		fixedNotice bool
		want        []string
	}{
		{name: "padding only", lines: []string{"one", "two", "", ""}, want: []string{"one", "two"}},
		{
			name:        "notice on the bottom row below padding",
			lines:       []string{"one", "two", "", "", "Pane is dead (status 7, Wed Sep  9 14:42:18 2026)"},
			fixedNotice: true,
			want:        []string{"one", "two"},
		},
		{
			name:        "notice appended to the last line",
			lines:       []string{"built        Pane is dead (status 3, Wed Sep  9 14:42:18 2026)", ""},
			fixedNotice: true,
			want:        []string{"built"},
		},
		{
			name:        "a signal notice on the bottom row",
			lines:       []string{"watching", "", "Pane is dead (signal 9, Sat Sep 12 12:24:50 2026)"},
			fixedNotice: true,
			want:        []string{"watching"},
		},
		{
			name:  "a command's own line is not a notice where tmux writes none",
			lines: []string{"echo 'Pane is dead (status 1, faked)'", ""},
			want:  []string{"echo 'Pane is dead (status 1, faked)'"},
		},
		{name: "nothing shown", lines: []string{"", "", ""}, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := trimScreen(slices.Clone(test.lines), test.fixedNotice)
			if !slices.Equal(got, test.want) {
				t.Errorf("trimScreen(%q, %v) = %q, want %q", test.lines, test.fixedNotice, got, test.want)
			}
		})
	}
}

// TestOutcomeRecordedWaitsForTmuxToReapTheCommand covers the state a pane
// passes through on its way to dead: tmux closes the pane's terminal, which is
// all pane_dead reports through tmux 3.5a, and only reaps the command
// afterwards. A wait that accepted the first of those would report every
// command as having exited zero.
func TestOutcomeRecordedWaitsForTmuxToReapTheCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		formats map[string]string
		want    bool
	}{
		{
			name:    "terminal closed before tmux reaped the command",
			formats: map[string]string{"pane_dead": "1"},
			want:    false,
		},
		{
			name: "outcome fields present but still empty",
			formats: map[string]string{
				"pane_dead": "1", "pane_dead_status": "", "pane_dead_signal": "",
			},
			want: false,
		},
		{
			name:    "nonzero exit status recorded",
			formats: map[string]string{"pane_dead": "1", "pane_dead_status": "7"},
			want:    true,
		},
		{
			name:    "zero exit status recorded",
			formats: map[string]string{"pane_dead": "1", "pane_dead_status": "0"},
			want:    true,
		},
		{
			name:    "signal recorded, as tmux 3.3 and later report it",
			formats: map[string]string{"pane_dead": "1", "pane_dead_signal": "KILL"},
			want:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			pane := Pane{formats: formatValues{values: test.formats}}
			if got := outcomeRecorded(pane); got != test.want {
				t.Errorf("outcomeRecorded(%v) = %v, want %v", test.formats, got, test.want)
			}
		})
	}
}

// A deadline is the caller saying how long an answer is worth, so it raises
// the floor on waiting for tmux to reap rather than being overridden by it.
// A machine loaded enough to need more than five seconds is exactly where a
// caller who allowed a minute does not want ErrOutcomeUnrecorded.
func TestSettleLimitTakesTheLongerOfTheFloorAndTheDeadline(t *testing.T) {
	t.Parallel()

	if got := settleLimit(context.Background()); got != outcomeSettleLimit {
		t.Errorf("settleLimit(no deadline) = %v, want %v", got, outcomeSettleLimit)
	}

	short, cancelShort := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelShort()
	if got := settleLimit(short); got != outcomeSettleLimit {
		t.Errorf("settleLimit(short deadline) = %v, want the floor %v", got, outcomeSettleLimit)
	}

	long, cancelLong := context.WithTimeout(context.Background(), time.Minute)
	defer cancelLong()
	if got := settleLimit(long); got <= outcomeSettleLimit {
		t.Errorf("settleLimit(minute deadline) = %v, want more than %v", got, outcomeSettleLimit)
	}
}

// The limit is read once. Reading it each pass would shrink it by the same
// step the settled total grows by, and the wait would end at half the time
// the caller allowed rather than at the deadline.
func TestSettleLimitIsReadOnceRatherThanEachPass(t *testing.T) {
	t.Parallel()

	deadline := time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	once := settleLimit(ctx)
	var settledOnce, settledEachPass time.Duration
	for settledOnce < once {
		settledOnce += outcomeSettleDelay
	}
	// Replay the loop as it would run if the limit moved with the clock.
	for remaining := deadline; settledEachPass < max(outcomeSettleLimit, remaining); {
		settledEachPass += outcomeSettleDelay
		remaining -= outcomeSettleDelay
	}
	if settledEachPass >= settledOnce {
		t.Fatalf("recomputing each pass settled for %v, reading once %v: the "+
			"replay does not reproduce the halving it guards against",
			settledEachPass, settledOnce)
	}
	if settledOnce < deadline-time.Second {
		t.Errorf("settled for %v of a %v deadline, want nearly all of it",
			settledOnce, deadline)
	}
}

// deadPaneRunner answers every listing with one pane tmux calls dead and has
// not recorded an outcome for, and never signals the wait-for channel. It is
// the state waitForExit settles through.
type deadPaneRunner struct {
	version Version
	mu      sync.Mutex
	settles int
}

func (r *deadPaneRunner) Run(
	ctx context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	switch {
	case slices.Contains(request.Arguments, "-V"):
		return tmuxcmd.Result{Stdout: []string{"tmux " + r.version.String()}}, nil
	case slices.Contains(request.Arguments, "display-message"):
		return tmuxcmd.Result{RawStdout: framedSnapshotRecord(
			snapshotIdentityFields(), snapshotRowValues(r.version, nil),
		)}, nil
	case slices.Contains(request.Arguments, "list-panes"):
		r.mu.Lock()
		r.settles++
		r.mu.Unlock()
		fields, err := formatFieldsFor("list-panes", r.version)
		if err != nil {
			return tmuxcmd.Result{}, err
		}
		return tmuxcmd.Result{RawStdout: framedSnapshotRecord(fields, snapshotRowValues(
			r.version, map[string]string{
				"session_id": "$1", "window_id": "@1", "window_index": "0",
				"pane_id": "%1", "pane_index": "0", "pane_dead": "1",
			},
		))}, nil
	case slices.Contains(request.Arguments, "wait-for"):
		<-ctx.Done()
		return tmuxcmd.Result{ExitCode: -1}, ctx.Err()
	}
	return tmuxcmd.Result{}, nil
}

// waitForExit reads its settle limit once. Reading it each pass shrinks it by
// the same step the settled total grows by, so the wait ends at half the time
// the caller allowed - which is what this drives the real loop to catch.
func TestWaitForExitReadsItsSettleLimitOnce(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := &deadPaneRunner{version: version}
	server := serverWithRunner(runner)

	var asked atomic.Int32
	running := &Running{
		session: Session{server: server, sessionID: "$1"},
		pane: Pane{
			server: server, sessionID: "$1", windowID: "@1", paneID: "%1",
		},
		channel: "settle",
		settle: func(context.Context) time.Duration {
			asked.Add(1)
			return 3 * outcomeSettleDelay
		},
	}

	err := running.waitForExit(context.Background())
	if !errors.Is(err, ErrOutcomeUnrecorded) {
		t.Fatalf("waitForExit() error = %v, want ErrOutcomeUnrecorded", err)
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("settle limit read %d times, want once: reading it per pass "+
			"is what halves the caller's deadline", got)
	}
}

// The settle poll backs off. A caller who allowed minutes should not spend
// them listing panes fifty times a second while tmux catches up.
func TestSettlePollBacksOff(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := &deadPaneRunner{version: version}
	server := serverWithRunner(runner)
	running := &Running{
		session: Session{server: server, sessionID: "$1"},
		pane: Pane{
			server: server, sessionID: "$1", windowID: "@1", paneID: "%1",
		},
		channel: "settle",
		settle:  func(context.Context) time.Duration { return 500 * time.Millisecond },
	}

	started := time.Now()
	if err := running.waitForExit(context.Background()); !errors.Is(err, ErrOutcomeUnrecorded) {
		t.Fatalf("waitForExit() error = %v, want ErrOutcomeUnrecorded", err)
	}
	elapsed := time.Since(started)

	runner.mu.Lock()
	settles := runner.settles
	runner.mu.Unlock()
	// Flat 20ms polling over half a second would be twenty-five listings.
	if settles > 10 {
		t.Errorf("settled with %d listings in %v, want a backing-off handful",
			settles, elapsed)
	}
	if settles < 2 {
		t.Errorf("settled with %d listings, want it to keep asking", settles)
	}
}

// aliveThenDeadRunner reports a live pane for a while and a dead, unreaped one
// after, which is what a command that runs before it exits looks like. A pane
// that is already dead on the first look never exercises the liveness backoff.
type aliveThenDeadRunner struct {
	version    Version
	mu         sync.Mutex
	livePolls  int
	reapAsked  bool
	settlePoll int
}

func (r *aliveThenDeadRunner) Run(
	ctx context.Context,
	request tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	switch {
	case slices.Contains(request.Arguments, "-V"):
		return tmuxcmd.Result{Stdout: []string{"tmux " + r.version.String()}}, nil
	case slices.Contains(request.Arguments, "display-message"):
		return tmuxcmd.Result{RawStdout: framedSnapshotRecord(
			snapshotIdentityFields(), snapshotRowValues(r.version, nil),
		)}, nil
	case slices.Contains(request.Arguments, "list-panes"):
		r.mu.Lock()
		dead := "0"
		if r.livePolls >= 3 {
			dead = "1"
			r.settlePoll++
		} else {
			r.livePolls++
		}
		r.mu.Unlock()
		fields, err := formatFieldsFor("list-panes", r.version)
		if err != nil {
			return tmuxcmd.Result{}, err
		}
		return tmuxcmd.Result{RawStdout: framedSnapshotRecord(fields, snapshotRowValues(
			r.version, map[string]string{
				"session_id": "$1", "window_id": "@1", "window_index": "0",
				"pane_id": "%1", "pane_index": "0", "pane_dead": dead,
			},
		))}, nil
	case slices.Contains(request.Arguments, "wait-for"):
		<-ctx.Done()
		return tmuxcmd.Result{ExitCode: -1}, ctx.Err()
	}
	r.mu.Lock()
	r.reapAsked = true
	r.mu.Unlock()
	return tmuxcmd.Result{}, nil
}

// Settling is timed on its own. Charging it the liveness backoff that ran
// before the pane died spends the whole allowance on the poll that found it,
// which both cuts the wait short and skips the reap nudge that exists for a
// tmux which lost the child's signal.
func TestSettlingIsNotChargedTheLivenessBackoff(t *testing.T) {
	t.Parallel()

	version := mustParseVersion(t, "3.7")
	runner := &aliveThenDeadRunner{version: version}
	server := serverWithRunner(runner)
	running := &Running{
		session: Session{server: server, sessionID: "$1"},
		pane: Pane{
			server: server, sessionID: "$1", windowID: "@1", paneID: "%1",
		},
		channel: "settle",
		settle:  func(context.Context) time.Duration { return 2 * time.Second },
	}

	if err := running.waitForExit(context.Background()); !errors.Is(err, ErrOutcomeUnrecorded) {
		t.Fatalf("waitForExit() error = %v, want ErrOutcomeUnrecorded", err)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	// One lump of stale liveness delay would end the settle in a poll or two.
	if runner.settlePoll < 4 {
		t.Errorf("settled over %d polls, want the allowance spent on settling",
			runner.settlePoll)
	}
	if !runner.reapAsked {
		t.Error("the reap nudge never fired, which the settle allowance is for")
	}
}

// The settle poll doubles and then stops doubling. Without the ceiling a wait
// bounded by a long deadline would sleep for minutes between asks and answer
// long after tmux caught up.
func TestSettleDelayDoublesToACeiling(t *testing.T) {
	t.Parallel()

	delay := outcomeSettleDelay / 2
	for range 4 {
		doubled := nextSettleDelay(delay)
		if doubled != delay*2 {
			t.Fatalf("nextSettleDelay(%v) = %v, want it doubled", delay, doubled)
		}
		delay = doubled
	}
	if got := nextSettleDelay(maximumSettleDelay); got != maximumSettleDelay {
		t.Errorf("nextSettleDelay(%v) = %v, want the ceiling held",
			maximumSettleDelay, got)
	}
	if got := nextSettleDelay(maximumSettleDelay * 4); got != maximumSettleDelay {
		t.Errorf("nextSettleDelay(%v) = %v, want the ceiling", maximumSettleDelay*4, got)
	}
}
