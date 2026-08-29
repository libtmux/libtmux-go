package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConcurrentJobCollectorsUseSettledResult(t *testing.T) {
	directory := t.TempDir()
	started := time.Unix(100, 0)
	owned := newJobs()
	if err := owned.keep(&job{
		id: "job", directory: directory, started: started,
	}); err != nil {
		t.Fatalf("keep() error = %v", err)
	}
	leader, elected, done, found := owned.beginCollection("job")
	if !found || !elected || leader.finished || done == nil {
		t.Fatalf("first beginCollection() = (%#v, %t, %p, %t)", leader, elected, done, found)
	}
	follower, elected, sameDone, found := owned.beginCollection("job")
	if !found || elected || follower.finished || sameDone != done {
		t.Fatalf("second beginCollection() = (%#v, %t, %p, %t)", follower, elected, sameDone, found)
	}

	ended := started.Add(5 * time.Second)
	settled, ok := owned.settle(
		"job", 7, []string{"canonical"}, truncation{TruncatedLines: 2},
		"capture unavailable", true, ended,
	)
	if !ok || !settled.finished {
		t.Fatalf("settle() = (%#v, %t), want a finished job", settled, ok)
	}
	select {
	case <-done:
	default:
		t.Fatal("settle() did not wake collection followers")
	}
	owned.endCollection("job", done)
	canonical, ok := owned.find("job")
	if !ok || !canonical.finished || canonical.exitStatus != 7 ||
		canonical.ended != ended || len(canonical.output) != 1 ||
		canonical.output[0] != "canonical" || canonical.directory != "" ||
		canonical.outputUnavailable != "capture unavailable" || !canonical.linesMissed {
		t.Errorf("find() = %#v, want the canonical settled result", canonical)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settled directory still exists: %v", err)
	}
}

func TestClosingJobsWakesCollectionFollowers(t *testing.T) {
	owned := newJobs()
	if err := owned.keep(&job{id: "job", directory: t.TempDir()}); err != nil {
		t.Fatalf("keep() error = %v", err)
	}
	_, elected, done, found := owned.beginCollection("job")
	if !found || !elected {
		t.Fatal("beginCollection() did not elect a collector")
	}
	owned.close()
	select {
	case <-done:
	default:
		t.Fatal("close() did not wake collection followers")
	}
}

func TestJobEvictionPreservesAnActiveCollector(t *testing.T) {
	base := t.TempDir()
	owned := newJobs()
	directories := make([]string, jobsRetained+1)
	for index := range jobsRetained {
		directory := filepath.Join(base, fmt.Sprintf("job-%02d", index))
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		directories[index] = directory
		if err := owned.keep(&job{
			id: fmt.Sprintf("job-%02d", index), directory: directory,
		}); err != nil {
			t.Fatalf("keep job %d: %v", index, err)
		}
	}
	_, elected, done, found := owned.beginCollection("job-00")
	if !found || !elected {
		t.Fatal("oldest job did not elect a collector")
	}

	directories[jobsRetained] = filepath.Join(base, "new")
	if err := os.Mkdir(directories[jobsRetained], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := owned.keep(&job{
		id: "new", directory: directories[jobsRetained],
	}); err != nil {
		t.Fatalf("keep new job: %v", err)
	}
	if _, retained := owned.find("job-00"); !retained {
		t.Fatal("eviction removed the active collector")
	}
	if _, retained := owned.find("job-01"); retained {
		t.Fatal("eviction retained the oldest idle job")
	}
	if _, err := os.Stat(directories[0]); err != nil {
		t.Fatalf("collector directory was removed: %v", err)
	}
	if _, err := os.Stat(directories[1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evicted job directory still exists: %v", err)
	}
	owned.endCollection("job-00", done)
}

func TestJobCapacityRefusesToEvictCollectors(t *testing.T) {
	base := t.TempDir()
	owned := newJobs()
	for index := range jobsRetained {
		id := fmt.Sprintf("job-%02d", index)
		directory := filepath.Join(base, id)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := owned.keep(&job{id: id, directory: directory}); err != nil {
			t.Fatalf("keep job %d: %v", index, err)
		}
		if _, elected, _, found := owned.beginCollection(id); !found || !elected {
			t.Fatalf("job %d did not elect a collector", index)
		}
	}
	refused := filepath.Join(base, "refused")
	if err := os.Mkdir(refused, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := owned.keep(&job{id: "refused", directory: refused}); !errors.Is(err, errJobCapacity) {
		t.Fatalf("keep beyond collector capacity = %v, want %v", err, errJobCapacity)
	}
	if _, retained := owned.find("refused"); retained {
		t.Fatal("a refused job was retained")
	}
	if _, err := os.Stat(refused); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused job directory still exists: %v", err)
	}
	for index := range jobsRetained {
		if _, retained := owned.find(fmt.Sprintf("job-%02d", index)); !retained {
			t.Fatalf("collector %d was evicted", index)
		}
	}
	owned.close()
}

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
func TestRunCommandTimeoutIncludesCommandSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "run-wall-clock-timeout"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
	instance, _, request := connectJobSession(ctx, t, target)
	command, err := instance.runtime.command(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const gate = "run-command-setup-gate"
	held := make(chan error, 1)
	go func() {
		held <- command.WaitFor(ctx, tmux.WaitForRequest{Channel: gate})
	}()
	waitForWaitTextLaneBlock(t, ctx, command)
	released := make(chan error, 1)
	time.AfterFunc(1250*time.Millisecond, func() {
		if err := target.WaitFor(ctx, tmux.WaitForRequest{
			Channel: gate, Mode: tmux.WaitForModeSignal,
		}); err != nil {
			released <- err
			return
		}
		released <- <-held
	})

	startedAt := time.Now()
	_, output, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "sleep 300", TimeoutSeconds: 1,
	})
	elapsed := time.Since(startedAt)
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if err != nil || !output.TimedOut ||
		output.EffectiveTimeoutSeconds != 1 || output.TimeoutClamped {
		t.Fatalf("timed run_command = (%+v, %v)", output, err)
	}
	if elapsed < 750*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Fatalf("run_command elapsed = %v, want whole-operation one-second budget", elapsed)
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
func TestDetachedAdmissionPrecedesPaneDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{
		Name: "job-capacity-admission", Command: "sleep 300",
	})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	instance, serverSession, request := connectJobSession(ctx, t, target)
	for index := range jobsRetained {
		id := fmt.Sprintf("job-%02d", index)
		if err := serverSession.scope.jobs.keep(&job{id: id}); err != nil {
			t.Fatal(err)
		}
		if _, elected, _, found := serverSession.scope.jobs.beginCollection(id); !found || !elected {
			t.Fatalf("job %d did not elect a collector", index)
		}
	}

	_, _, err = instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo MUST-NOT-BE-DELIVERED", Detach: true,
	})
	if !errors.Is(err, errJobCapacity) {
		t.Fatalf("runCommand() error = %v, want %v", err, errJobCapacity)
	}
	time.Sleep(100 * time.Millisecond)
	lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), "libtmux-mcp-run") {
		t.Fatalf("a refused detached command still reached the pane: %q", lines)
	}
}

//libtmux:real-tmux
func TestOneJobCompletionDoesNotFinishAnother(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	firstSession, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-shared-first"})
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-shared-second"})
	if err != nil {
		t.Fatal(err)
	}
	firstPane, ok, err := firstSession.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve first pane = (%v, %t, %v)", firstPane, ok, err)
	}
	secondPane, ok, err := secondSession.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve second pane = (%v, %t, %v)", secondPane, ok, err)
	}
	instance, serverSession, request := connectJobSession(ctx, t, target)

	_, first, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: firstPane.ID().String(), Command: "tmux wait-for job-gate-first; echo FIRST", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: secondPane.ID().String(), Command: "tmux wait-for job-gate-second; echo SECOND", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstJob, firstFound := serverSession.scope.jobs.find(first.JobID)
	if !firstFound {
		t.Fatal("detached jobs were not retained")
	}
	if first.JobID == second.JobID {
		t.Fatalf("job ids = (%q, %q), want distinct handles", first.JobID, second.JobID)
	}

	type result struct {
		output getJobOutput
		err    error
	}
	returned := make(chan result, 1)
	go func() {
		_, output, err := instance.tools.getJob(ctx, request, getJobInput{
			JobID: second.JobID, TimeoutSeconds: 10,
		})
		returned <- result{output: output, err: err}
	}()
	waitForJobCollection(t, ctx, serverSession.scope.jobs, second.JobID)
	if err := target.WaitFor(ctx, tmux.WaitForRequest{
		Channel: "job-gate-first", Mode: tmux.WaitForModeSignal,
	}); err != nil {
		t.Fatal(err)
	}
	waitForPath(t, ctx, firstJob.closedAt)
	select {
	case got := <-returned:
		t.Fatalf("another job's completion ended the wait: (%+v, %v)", got.output, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := target.WaitFor(ctx, tmux.WaitForRequest{
		Channel: "job-gate-second", Mode: tmux.WaitForModeSignal,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-returned:
		if got.err != nil || !got.output.Finished || got.output.ExitStatus == nil ||
			*got.output.ExitStatus != 0 ||
			!strings.Contains(strings.Join(got.output.Output, "\n"), "SECOND") {
			t.Fatalf("second job collection = (%+v, %v)", got.output, got.err)
		}
	case <-ctx.Done():
		t.Fatalf("second job did not finish: %v", ctx.Err())
	}
}

//libtmux:real-tmux
func TestCompletedJobsDoNotDependOnSharedSignalEdges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "job-signal-edge"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
	instance, _, request := connectJobSession(ctx, t, target)

	_, first, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo FIRST", TimeoutSeconds: 5,
	})
	if err != nil || first.ExitStatus == nil || *first.ExitStatus != 0 {
		t.Fatalf("first run = (%+v, %v)", first, err)
	}
	_, detached, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID: pane.ID().String(), Command: "echo SECOND", Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	began := time.Now()
	_, collected, err := instance.tools.getJob(ctx, request, getJobInput{
		JobID: detached.JobID, TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(began); elapsed >= 4*time.Second {
		t.Fatalf("completed get_job waited for its signal deadline: %v", elapsed)
	}
	if !collected.Finished || collected.ExitStatus == nil || *collected.ExitStatus != 0 ||
		!strings.Contains(strings.Join(collected.Output, "\n"), "SECOND") {
		t.Fatalf("completed get_job = %+v", collected)
	}
}

//libtmux:real-tmux
func TestRunCommandCannotEscapeItsBookkeepingSubshell(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{FixedShell: true})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "command-wrapper"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
	}
	tmuxtest.WaitForShellReady(ctx, t, pane)
	instance, _, request := connectJobSession(ctx, t, target)
	escaped := filepath.Join(t.TempDir(), "escaped")

	_, output, err := instance.tools.runCommand(ctx, request, runCommandInput{
		PaneID:         pane.ID().String(),
		Command:        ") ; printf ESCAPED > " + shellQuote(escaped) + "; #",
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(escaped); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid command escaped the bookkeeping subshell: %v", err)
	}
	if output.ExitStatus == nil || *output.ExitStatus == 0 || output.TimedOut {
		t.Fatalf("invalid command result = %+v, want a recorded nonzero status", output)
	}
}

//libtmux:real-tmux
func TestRunCommandBookkeepingUsesTheConfiguredTmuxExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bookkeeping wrapper is a POSIX shell script")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux is not installed: %v", err)
	}
	realTmux, err = filepath.Abs(realTmux)
	if err != nil {
		t.Fatal(err)
	}
	proxyDirectory := filepath.Join(t.TempDir(), "configured proxy")
	if err := os.Mkdir(proxyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(proxyDirectory, "custom tmux")
	proxyScript := "#!/bin/sh\nexec " + shellQuote(realTmux) + " \"$@\"\n"
	if err := os.WriteFile(proxy, []byte(proxyScript), 0o700); err != nil {
		t.Fatal(err)
	}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		Binary: proxy, FixedShell: true,
	})
	panePath := filepath.Join(t.TempDir(), "pane-path-without-tmux")
	if err := os.Mkdir(panePath, 0o700); err != nil {
		t.Fatal(err)
	}
	mv, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mv, filepath.Join(panePath, "mv")); err != nil {
		t.Fatal(err)
	}

	for _, detached := range []bool{false, true} {
		name := "waited"
		if detached {
			name = "detached"
		}
		t.Run(name, func(t *testing.T) {
			paneShell := "PATH=" + shellQuote(panePath) + " ENV= PS1=" +
				shellQuote(tmuxtest.ShellPrompt) + " /bin/sh -i"
			created, err := target.NewSession(ctx, tmux.NewSessionRequest{
				Name: "configured-binary-" + name, Command: paneShell,
			})
			if err != nil {
				t.Fatal(err)
			}
			pane, ok, err := created.ResolveActivePane(ctx)
			if err != nil || !ok {
				t.Fatalf("resolve active pane = (%v, %t, %v)", pane, ok, err)
			}
			tmuxtest.WaitForShellReady(ctx, t, pane)
			assertPaneCannotResolveTmux(ctx, t, pane)
			instance, _, request := connectJobSession(ctx, t, target)
			marker := strings.ToUpper(name) + "-CUSTOM-BINARY"
			_, started, err := instance.tools.runCommand(ctx, request, runCommandInput{
				PaneID: pane.ID().String(), Command: "printf " + marker,
				TimeoutSeconds: 2, Detach: detached,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !detached {
				if started.TimedOut || started.ExitStatus == nil || *started.ExitStatus != 0 ||
					!strings.Contains(strings.Join(started.Output, "\n"), marker) {
					t.Fatalf("waited run_command = %+v", started)
				}
				return
			}
			_, collected, err := instance.tools.getJob(ctx, request, getJobInput{
				JobID: started.JobID, TimeoutSeconds: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !collected.Finished || collected.ExitStatus == nil || *collected.ExitStatus != 0 ||
				!strings.Contains(strings.Join(collected.Output, "\n"), marker) {
				t.Fatalf("detached get_job = %+v", collected)
			}
		})
	}
}

func assertPaneCannotResolveTmux(
	ctx context.Context,
	t *testing.T,
	pane tmux.Pane,
) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "tmux-resolution")
	command := "command -v tmux > " + shellQuote(probe) +
		" 2>&1 || printf MISSING > " + shellQuote(probe)
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command, Literal: true,
	}); err != nil {
		t.Fatal(err)
	}
	for {
		resolved, err := os.ReadFile(probe)
		if err == nil && len(resolved) > 0 {
			if got := strings.TrimSpace(string(resolved)); got != "MISSING" {
				t.Fatalf("pane resolved tmux outside its test PATH as %q", got)
			}
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane PATH probe did not finish: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

//libtmux:real-tmux
func TestConcurrentGetJobFollowersJoinBeforeTheTmuxWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
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

func connectJobSession(
	ctx context.Context,
	t *testing.T,
	target tmux.Server,
) (*Instance, *ServerSession, *sdk.CallToolRequest) {
	t.Helper()
	instance := mustInternalMCPServer(t, target)
	_, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	return instance, serverSession, &sdk.CallToolRequest{Session: serverSession.sdk}
}

func waitForJobCollection(t *testing.T, ctx context.Context, owned *jobs, id string) {
	t.Helper()
	for {
		entry, found := owned.find(id)
		if !found {
			t.Fatalf("job %q disappeared before collection", id)
		}
		if entry.collecting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("job %q was not collected: %v", id, ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitForPath(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s was not published: %v", filepath.Base(path), ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
