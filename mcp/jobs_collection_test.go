package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAStatusIsNotCompleteUntilTheClosingMarkExists(t *testing.T) {
	directory := t.TempDir()
	entry := job{
		statusAt: filepath.Join(directory, "status"),
		closedAt: filepath.Join(directory, "closed"),
	}
	if err := os.WriteFile(entry.statusAt, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, ready, err := readCompletedJob(entry); err != nil || ready || status != 0 {
		t.Fatalf("status without close = (%d, %t, %v), want unfinished", status, ready, err)
	}
	if err := os.WriteFile(entry.closedAt, []byte("0 1 2 80 24"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, ready, err := readCompletedJob(entry); err != nil || !ready || status != 7 {
		t.Fatalf("committed status = (%d, %t, %v), want (7, true, nil)", status, ready, err)
	}
}

func TestAZeroTimeoutJobPollIsLocal(t *testing.T) {
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "job-zero-poll-unused"})
	if err != nil {
		t.Fatal(err)
	}
	instance, serverSession, request := connectJobSession(t.Context(), t, target)
	started := time.Now().Add(-time.Second)

	runningDirectory := t.TempDir()
	committedDirectory := t.TempDir()
	for path, contents := range map[string]string{
		filepath.Join(committedDirectory, "status"): "7",
		filepath.Join(committedDirectory, "closed"): "0 1 2 80 24",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []job{
		{
			id: "running", paneID: "%1", directory: runningDirectory,
			statusAt: filepath.Join(runningDirectory, "status"),
			closedAt: filepath.Join(runningDirectory, "closed"),
			started:  started,
		},
		{
			id: "committed", paneID: "%2", directory: committedDirectory,
			statusAt: filepath.Join(committedDirectory, "status"),
			closedAt: filepath.Join(committedDirectory, "closed"),
			started:  started,
		},
	} {
		if err := serverSession.scope.jobs.keep(&entry); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		id                string
		finished          bool
		collectionPending bool
		exitStatus        int
		hasExitStatus     bool
	}{
		{id: "running"},
		{
			id: "committed", finished: true, collectionPending: true,
			exitStatus: 7, hasExitStatus: true,
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			baseCtx, cancel := context.WithCancel(t.Context())
			ctx := withAcquiredServer(baseCtx, &runtimeAcquisition{
				server: target, runtime: instance.runtime, unbound: true,
			})
			type result struct {
				output getJobOutput
				err    error
			}
			returned := make(chan result, 1)
			go func() {
				_, output, err := instance.tools.getJob(
					ctx, request, getJobInput{JobID: test.id},
				)
				returned <- result{output: output, err: err}
			}()

			select {
			case got := <-returned:
				cancel()
				statusMatches := got.output.ExitStatus == nil && !test.hasExitStatus ||
					got.output.ExitStatus != nil && test.hasExitStatus &&
						*got.output.ExitStatus == test.exitStatus
				if got.err != nil || got.output.Finished != test.finished ||
					got.output.CollectionPending != test.collectionPending ||
					!statusMatches {
					t.Fatalf("zero-time get_job = (%+v, %v)", got.output, got.err)
				}
			case <-time.After(250 * time.Millisecond):
				cancel()
				got := <-returned
				t.Fatalf("zero-time get_job entered tmux: (%+v, %v)", got.output, got.err)
			}
		})
	}
}

func TestWaitForCompletedJobPrefersACommitToAnExpiredWait(t *testing.T) {
	directory := t.TempDir()
	entry := job{
		statusAt: filepath.Join(directory, "status"),
		closedAt: filepath.Join(directory, "closed"),
	}
	if err := os.WriteFile(entry.statusAt, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry.closedAt, []byte("0 1 2 80 24"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()
	status, ready, err := waitForCompletedJob(waitCtx, entry)
	if err != nil || !ready || status != 7 {
		t.Fatalf("expired wait with commit = (%d, %t, %v), want (7, true, nil)",
			status, ready, err)
	}
}

func TestGetJobSchemaRejectsNegativeTimeouts(t *testing.T) {
	schema, err := jsonschema.For[getJobInput](nil)
	if err != nil {
		t.Fatal(err)
	}
	constrain("get_job", schema)
	timeout := schema.Properties["timeoutSeconds"]
	if timeout == nil {
		t.Fatal("get_job schema has no timeoutSeconds property")
	}
	if timeout.Minimum == nil || *timeout.Minimum != 0 {
		t.Fatalf("get_job timeoutSeconds minimum = %v, want 0", timeout.Minimum)
	}
}

func TestANegativeTimeoutFollowerIsRejectedWithoutWaiting(t *testing.T) {
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "job-negative-unused"})
	if err != nil {
		t.Fatal(err)
	}
	instance, serverSession, request := connectJobSession(t.Context(), t, target)
	entry := job{id: "job", started: time.Now()}
	if err := serverSession.scope.jobs.keep(&entry); err != nil {
		t.Fatal(err)
	}
	_, elected, collectionDone, found := serverSession.scope.jobs.beginCollection(entry.id)
	if !found || !elected {
		t.Fatal("the existing collector was not elected")
	}

	type result struct{ err error }
	returned := make(chan result, 1)
	go func() {
		_, _, err := instance.tools.getJob(
			t.Context(), request, getJobInput{JobID: entry.id, TimeoutSeconds: -1},
		)
		returned <- result{err: err}
	}()
	select {
	case got := <-returned:
		serverSession.scope.jobs.endCollection(entry.id, collectionDone)
		if got.err == nil || !strings.Contains(got.err.Error(), "timeoutSeconds") {
			t.Fatalf("negative get_job timeout error = %v", got.err)
		}
	case <-time.After(250 * time.Millisecond):
		serverSession.scope.jobs.endCollection(entry.id, collectionDone)
		<-returned
		t.Fatal("negative get_job timeout waited behind the active collector")
	}
}

func TestACommittedJobStaysFinishedWhenPaneLookupReachesTheDeadline(t *testing.T) {
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "job-capture-deadline-unused"})
	if err != nil {
		t.Fatal(err)
	}
	instance, serverSession, request := connectJobSession(t.Context(), t, target)
	directory := t.TempDir()
	entry := job{
		id:       "job",
		paneID:   "%1",
		statusAt: filepath.Join(directory, "status"),
		closedAt: filepath.Join(directory, "closed"),
		started:  time.Now(),
	}
	if err := os.WriteFile(entry.statusAt, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry.closedAt, []byte("0 1 2 80 24"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := serverSession.scope.jobs.keep(&entry); err != nil {
		t.Fatal(err)
	}
	blockedTarget, err := tmux.NewServer(executableFixtureOptions(t, fixtureHang, tmux.ServerOptions{
		SocketPath:         target.SocketPath(),
		ConfigFile:         target.ConfigFile(),
		ProcessEnvironment: target.ProcessEnvironment(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := withAcquiredServer(t.Context(), &runtimeAcquisition{
		server:  blockedTarget,
		runtime: instance.runtime,
		unbound: true,
	})

	_, output, err := instance.tools.getJob(ctx, request, getJobInput{
		JobID: entry.id, TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !output.Finished || !output.CollectionPending || output.ExitStatus == nil ||
		*output.ExitStatus != 7 {
		t.Fatalf("committed get_job at capture deadline = %+v, want pending status 7", output)
	}
}

func blockingCaptureExecutable(t testing.TB, executable string) string {
	t.Helper()
	proxy := filepath.Join(t.TempDir(), "tmux-blocking-capture")
	script := "#!/bin/sh\nfor argument in \"$@\"; do\n" +
		"  case \"$argument\" in *capture-pane*) exec /bin/sleep 3600;; esac\n" +
		"done\nexec " + shellQuote(executable) + " \"$@\"\n"
	if err := os.WriteFile(proxy, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return proxy
}

//libtmux:real-tmux
func TestACommittedJobStaysFinishedWhenOutputCaptureReachesTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-capture-deadline"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance, serverSession, request := connectJobSession(ctx, t, target)
	directory := t.TempDir()
	entry := job{
		id:        "job",
		paneID:    pane.ID(),
		directory: directory,
		openedAt:  filepath.Join(directory, "opened"),
		statusAt:  filepath.Join(directory, "status"),
		closedAt:  filepath.Join(directory, "closed"),
		started:   time.Now(),
	}
	for path, contents := range map[string]string{
		entry.openedAt: "0 0 0 80 24",
		entry.statusAt: "7",
		entry.closedAt: "0 1 1 80 24",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := serverSession.scope.jobs.keep(&entry); err != nil {
		t.Fatal(err)
	}
	blockedTarget, err := tmux.NewServer(tmux.ServerOptions{
		Binary:             blockingCaptureExecutable(t, target.Executable()),
		SocketPath:         target.SocketPath(),
		ConfigFile:         target.ConfigFile(),
		ProcessEnvironment: target.ProcessEnvironment(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitCtx := withAcquiredServer(ctx, &runtimeAcquisition{
		server: blockedTarget, runtime: instance.runtime, unbound: true,
	})

	_, output, err := instance.tools.getJob(waitCtx, request, getJobInput{
		JobID: entry.id, TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !output.Finished || !output.CollectionPending || output.ExitStatus == nil ||
		*output.ExitStatus != 7 {
		t.Fatalf("committed get_job at output deadline = %+v, want pending status 7", output)
	}
}

func TestATimedOutDetachedJobReportsNoRecordedStart(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		want    string
	}{
		{name: "a shell that has not read the keys", want: "recorded no start"},
		{name: "a program holding the pane", command: "sleep 300", want: "respawn_pane"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
			created, err := target.NewSession(ctx, tmux.NewSessionRequest{
				Name: "job-unstarted", Command: test.command,
			})
			if err != nil {
				t.Fatal(err)
			}
			pane, ok, err := created.ResolveActivePane(ctx)
			if err != nil || !ok {
				t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
			}
			instance, serverSession, request := connectJobSession(ctx, t, target)
			// No marks at all: the wrapper recorded no start.
			directory := t.TempDir()
			entry := job{
				id:        "job",
				paneID:    pane.ID(),
				directory: directory,
				openedAt:  filepath.Join(directory, "opened"),
				statusAt:  filepath.Join(directory, "status"),
				closedAt:  filepath.Join(directory, "closed"),
				started:   time.Now(),
			}
			if err := serverSession.scope.jobs.keep(&entry); err != nil {
				t.Fatal(err)
			}

			_, output, err := instance.tools.getJob(ctx, request, getJobInput{
				JobID: entry.id, TimeoutSeconds: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if output.Finished || !strings.Contains(output.OutputUnavailable, test.want) {
				t.Fatalf("timed-out detached get_job = %+v, want %q", output, test.want)
			}
		})
	}
}

func TestAZeroTimeoutCommittedFollowerReturnsPendingBeforeSettlement(t *testing.T) {
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "job-follower-unused"})
	if err != nil {
		t.Fatal(err)
	}
	instance, serverSession, request := connectJobSession(t.Context(), t, target)
	entry, collectionDone := keepCommittedCollectingJob(t, serverSession.scope.jobs)

	type result struct {
		output getJobOutput
		err    error
	}
	returned := make(chan result, 1)
	go func() {
		_, output, err := instance.tools.getJob(
			t.Context(), request, getJobInput{JobID: "job"},
		)
		returned <- result{output: output, err: err}
	}()
	select {
	case got := <-returned:
		if got.err != nil || !got.output.Finished || !got.output.CollectionPending ||
			got.output.ExitStatus == nil || *got.output.ExitStatus != 7 {
			t.Fatalf("zero-time committed follower = (%+v, %v), want pending status 7",
				got.output, got.err)
		}
	case <-time.After(250 * time.Millisecond):
		serverSession.scope.jobs.settle(
			entry.id, 7, []string{"canonical"}, truncation{}, "", false,
			entry.started.Add(5*time.Second),
		)
		<-returned
		t.Fatal("zero-time committed follower waited for settlement")
	}

	serverSession.scope.jobs.settle(
		entry.id, 7, []string{"canonical"}, truncation{}, "", false,
		entry.started.Add(5*time.Second),
	)
	_, canonical, err := instance.tools.getJob(
		t.Context(), request, getJobInput{JobID: entry.id},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalJobOutput(t, canonical)
	serverSession.scope.jobs.endCollection(entry.id, collectionDone)
}

func TestACommittedFollowerCannotOutliveItsPositiveTimeout(t *testing.T) {
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "job-follower-timeout-unused"})
	if err != nil {
		t.Fatal(err)
	}
	instance, serverSession, request := connectJobSession(t.Context(), t, target)
	entry, collectionDone := keepCommittedCollectingJob(t, serverSession.scope.jobs)

	type result struct {
		output getJobOutput
		err    error
	}
	returned := make(chan result, 1)
	started := time.Now()
	go func() {
		_, output, err := instance.tools.getJob(
			t.Context(), request, getJobInput{JobID: entry.id, TimeoutSeconds: 1},
		)
		returned <- result{output: output, err: err}
	}()
	var got result
	select {
	case got = <-returned:
	case <-time.After(1500 * time.Millisecond):
		serverSession.scope.jobs.settle(
			entry.id, 7, []string{"canonical"}, truncation{}, "", false,
			entry.started.Add(5*time.Second),
		)
		got = <-returned
		t.Fatalf("positive-time committed follower outlived its timeout: (%+v, %v)",
			got.output, got.err)
	}
	elapsed := time.Since(started)
	if got.err != nil || got.output.Finished || got.output.ExitStatus != nil {
		t.Fatalf("timed committed follower = (%+v, %v), want unfinished", got.output, got.err)
	}
	if got.output.EffectiveTimeoutSeconds != 1 || got.output.TimeoutClamped {
		t.Fatalf("timed committed follower timeout = (%d, %t), want (1, false)",
			got.output.EffectiveTimeoutSeconds, got.output.TimeoutClamped)
	}
	if elapsed < 750*time.Millisecond {
		t.Fatalf("positive-time committed follower returned after %v, want a wait", elapsed)
	}

	serverSession.scope.jobs.settle(
		entry.id, 7, []string{"canonical"}, truncation{}, "", false,
		entry.started.Add(5*time.Second),
	)
	_, canonical, err := instance.tools.getJob(
		t.Context(), request, getJobInput{JobID: entry.id},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalJobOutput(t, canonical)
	serverSession.scope.jobs.endCollection(entry.id, collectionDone)
}

func keepCommittedCollectingJob(t *testing.T, owned *jobs) (job, chan struct{}) {
	t.Helper()
	directory := t.TempDir()
	entry := job{
		id: "job", directory: directory,
		statusAt: filepath.Join(directory, "status"),
		closedAt: filepath.Join(directory, "closed"),
		started:  time.Now().Add(-5 * time.Second),
	}
	if err := os.WriteFile(entry.statusAt, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry.closedAt, []byte("0 1 2 80 24"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := owned.keep(&entry); err != nil {
		t.Fatal(err)
	}
	_, elected, collectionDone, found := owned.beginCollection(entry.id)
	if !found || !elected {
		t.Fatal("the canonical collector was not elected")
	}
	return entry, collectionDone
}

func assertCanonicalJobOutput(t *testing.T, output getJobOutput) {
	t.Helper()
	if !output.Finished || output.ExitStatus == nil || *output.ExitStatus != 7 ||
		strings.Join(output.Output, "\n") != "canonical" {
		t.Fatalf("canonical get_job output = %+v", output)
	}
}

//libtmux:real-tmux
func TestTimedOutGetJobUsesCommitPolling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "job-timeout-release", Command: "sleep 300",
	})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance, _, request := connectJobSession(ctx, t, target)

	_, started, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo NEVER-RUNS", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, checked, err := instance.tools.getJob(ctx, request, getJobInput{
		JobID: started.JobID, TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Finished {
		t.Fatal("the command sent to a busy program reported finished")
	}
}

//libtmux:real-tmux
func TestElectedGetJobCannotOutliveItsEffectiveTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "job-wall-clock-timeout", Command: "sleep 300",
	})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance, _, request := connectJobSession(ctx, t, target)
	_, started, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo NEVER-RUNS", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		output getJobOutput
		err    error
	}
	returned := make(chan result, 1)
	startedAt := time.Now()
	wallClockLimit := 1500 * time.Millisecond
	go func() {
		_, output, err := instance.tools.getJob(ctx, request, getJobInput{
			JobID: started.JobID, TimeoutSeconds: 1,
		})
		returned <- result{output: output, err: err}
	}()

	var got result
	select {
	case got = <-returned:
	case <-time.After(wallClockLimit):
		got = <-returned
		t.Fatalf("get_job outlived its effective timeout: (%+v, %v)",
			got.output, got.err)
	}
	if got.err != nil || got.output.Finished ||
		got.output.EffectiveTimeoutSeconds != 1 || got.output.TimeoutClamped {
		t.Fatalf("timed get_job = (%+v, %v), want unfinished one-second result",
			got.output, got.err)
	}
	if elapsed := time.Since(startedAt); elapsed > wallClockLimit {
		t.Fatalf("get_job elapsed = %v, want at most %v", elapsed, wallClockLimit)
	}
}

//libtmux:real-tmux
func TestConcurrentGetJobFollowersJoinBeforeTheTmuxWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// A fixed shell keeps the election window measuring collector handoff
	// rather than how long an interactive shell takes to start.
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-followers"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance := mustInternalMCPServer(t, target)
	_, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	request := &sdk.CallToolRequest{Session: serverSession.sdk}

	_, started, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "sleep 1; echo COLLECTED", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := getJobInput{JobID: started.JobID, TimeoutSeconds: 5}
	type result struct {
		output getJobOutput
		err    error
	}
	collect := func(done chan<- result) {
		_, output, err := instance.tools.getJob(ctx, request, input)
		done <- result{output: output, err: err}
	}
	leaderDone := make(chan result, 1)
	go collect(leaderDone)

	electionDeadline := time.NewTimer(500 * time.Millisecond)
	defer electionDeadline.Stop()
	for {
		entry, found := serverSession.scope.jobs.find(started.JobID)
		if !found {
			t.Fatal("detached job disappeared before collection")
		}
		if entry.collecting {
			break
		}
		select {
		case <-electionDeadline.C:
			t.Fatal("collector entered tmux before publishing its election")
		case <-time.After(5 * time.Millisecond):
		}
	}

	followerDone := make(chan result, 1)
	go collect(followerDone)
	for name, done := range map[string]<-chan result{
		"leader": leaderDone, "follower": followerDone,
	} {
		select {
		case collected := <-done:
			if collected.err != nil {
				t.Fatalf("%s get_job: %v", name, collected.err)
			}
			if !collected.output.Finished || collected.output.ExitStatus == nil ||
				*collected.output.ExitStatus != 0 ||
				!strings.Contains(strings.Join(collected.output.Output, "\n"), "COLLECTED") {
				t.Errorf("%s get_job = %+v", name, collected.output)
			}
		case <-ctx.Done():
			t.Fatalf("%s get_job did not finish: %v", name, ctx.Err())
		}
	}
}
