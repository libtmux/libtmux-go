//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// countingRunner is the library's own subprocess transport with a counter in
// front of it, so a measurement counts tmux processes rather than estimating
// them.
type countingRunner struct {
	mu    sync.Mutex
	count int
}

func (r *countingRunner) Run(
	ctx context.Context,
	request tmux.CommandRequest,
) (tmux.CommandResult, error) {
	r.mu.Lock()
	r.count++
	r.mu.Unlock()
	var stdio *tmuxcmd.Stdio
	if request.Stdio != nil {
		stdio = &tmuxcmd.Stdio{
			Stdin:  request.Stdio.Stdin,
			Stdout: request.Stdio.Stdout,
			Stderr: request.Stdio.Stderr,
		}
	}
	result, err := tmuxcmd.Runner{}.Run(ctx, tmuxcmd.Request{
		Binary:      request.Binary,
		Arguments:   request.Arguments,
		Environment: request.Environment,
		Directory:   request.Directory,
		Stdio:       stdio,
	})
	return tmux.CommandResult{
		Command:   result.Command,
		Stdout:    result.Stdout,
		RawStdout: result.RawStdout,
		Stderr:    result.Stderr,
		ExitCode:  result.ExitCode,
	}, err
}

func (r *countingRunner) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count = 0
}

func (r *countingRunner) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// countingEngine counts the commands one engine carried.
type countingEngine struct {
	inner tmux.Engine
	mu    sync.Mutex
	count int
}

func (e *countingEngine) Supports(kind tmux.CommandKind) bool { return e.inner.Supports(kind) }

func (e *countingEngine) Run(
	ctx context.Context,
	kind tmux.CommandKind,
	request tmux.CommandRequest,
) (tmux.CommandResult, error) {
	e.mu.Lock()
	e.count++
	e.mu.Unlock()
	return e.inner.Run(ctx, kind, request)
}

func (e *countingEngine) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count = 0
}

func (e *countingEngine) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

// measuredServer returns a handle that talks to the harness-owned tmux server
// through a counted subprocess transport.
func measuredServer(t *testing.T, harness tmux.Server) (tmux.Server, *countingRunner) {
	t.Helper()
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("resolve tmux: %v", err)
	}
	runner := &countingRunner{}
	return tmux.NewServer(tmux.ServerOptions{
		Binary:             binary,
		SocketPath:         harness.SocketPath(),
		ConfigFile:         harness.ConfigFile(),
		ProcessEnvironment: harness.ProcessEnvironment(),
		Runner:             runner,
	}), runner
}

//libtmux:real-tmux
func TestControlEngineServesTheObjectAPIWithoutStartingProcesses(t *testing.T) {
	harness := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, runner := measuredServer(t, harness)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	engine := &countingEngine{inner: client.Engine()}
	connected := server.WithEngine(engine)
	runner.reset()

	window, err := connected.Session(ctx, sessions[0].ID())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	created, err := window.NewWindow(ctx, tmux.NewWindowRequest{Name: tmux.Ptr("engine")})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	renamed, err := created.Rename(ctx, "engine-renamed")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if name, ok := renamed.Name(); !ok || name != "engine-renamed" {
		t.Fatalf("Rename() name = (%q, %v)", name, ok)
	}
	snapshot, err := connected.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Windows()) < 2 {
		t.Fatalf("Snapshot() windows = %d, want at least two", len(snapshot.Windows()))
	}
	if runner.total() != 0 {
		t.Fatalf("started %d tmux processes, want none through the control engine", runner.total())
	}
	if engine.total() == 0 {
		t.Fatal("control engine carried no commands")
	}
}

//libtmux:real-tmux
func TestControlEnginePreservesCompletedCommandFailureClassification(t *testing.T) {
	harness := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, _ := measuredServer(t, harness)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	connected := server.WithEngine(client.Engine())

	session, err := connected.Session(ctx, sessions[0].ID())
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: tmux.Ptr("doomed")})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	if err := window.Kill(ctx); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	err = window.Kill(ctx)
	var commandError *tmux.CommandError
	if !errors.As(err, &commandError) || !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("second Kill() error = %v, want a CommandError", err)
	}
	if commandError.Result.ExitCode != 1 {
		t.Fatalf("CommandError exit code = %d, want 1", commandError.Result.ExitCode)
	}
	if !strings.Contains(strings.Join(commandError.Result.Stderr, "\n"), "can't find window") {
		t.Fatalf("CommandError stderr = %#v, want tmux's own message", commandError.Result.Stderr)
	}

	// A missing-target failure still reaches the absence classification that
	// reads tmux's message off stderr, which control mode reports in a %error
	// frame instead.
	if _, err := connected.Window(ctx, tmux.WindowID("@4242")); !errors.Is(
		err,
		tmux.ErrSnapshotNotFound,
	) {
		t.Fatalf("Window(@4242) error = %v, want ErrSnapshotNotFound", err)
	}
}

//libtmux:real-tmux
func TestOperationsTheControlEngineCannotServeKeepWorking(t *testing.T) {
	harness := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, runner := measuredServer(t, harness)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	direct, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	engine := &countingEngine{inner: client.Engine()}
	connected := server.WithEngine(engine)
	if engine.Supports(tmux.CommandProcess) {
		t.Fatal("control engine claims it can carry process commands")
	}

	runner.reset()
	engine.reset()
	connectedVersion, err := connected.RefreshVersion(ctx)
	if err != nil {
		t.Fatalf("RefreshVersion() through the control engine error = %v", err)
	}
	if connectedVersion.String() != direct.String() {
		t.Fatalf("RefreshVersion() = %q, want %q", connectedVersion, direct)
	}
	if runner.total() == 0 {
		t.Fatal("tmux -V did not start a tmux process")
	}
	if engine.total() != 0 {
		t.Fatalf("control engine carried %d version commands, want none", engine.total())
	}

	panes, err := connected.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	pane := panes.Panes()[0]
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: tmux.Ptr("printf 'engine-marker\\n'"),
	}); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	if err := tmuxtest.WaitFor(ctx, 20*time.Millisecond, func(ctx context.Context) (bool, error) {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err != nil {
			return false, err
		}
		for _, line := range lines {
			if strings.Contains(line, "engine-marker") && !strings.Contains(line, "printf") {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatalf("wait for pane output: %v", err)
	}

	runner.reset()
	engine.reset()
	captured, err := pane.CaptureBytes(ctx, tmux.CapturePaneRequest{})
	if err != nil {
		t.Fatalf("CaptureBytes() error = %v", err)
	}
	if !strings.Contains(string(captured), "engine-marker") {
		t.Fatalf("CaptureBytes() = %q, want the pane marker", captured)
	}
	if runner.total() != 1 {
		t.Fatalf("CaptureBytes() started %d tmux processes, want one", runner.total())
	}
	if engine.total() != 0 {
		t.Fatalf("control engine carried %d byte-exact reads, want none", engine.total())
	}
}

// connectedHarness returns a counted handle on a fresh harness server, the same
// handle with a control-mode engine selected, and the counters for each
// transport. The version cache is warmed first so a tmux -V probe, which no
// engine can carry, cannot land inside a measurement.
func connectedHarness(
	ctx context.Context,
	t *testing.T,
) (tmux.Server, tmux.Server, *countingRunner, *countingEngine) {
	t.Helper()

	harness := tmuxtest.NewServer(context.Background(), t)
	server, runner := measuredServer(t, harness)
	if _, err := server.Version(ctx); err != nil {
		t.Fatalf("warm the version cache: %v", err)
	}
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	engine := &countingEngine{inner: client.Engine()}
	return server, server.WithEngine(engine), runner, engine
}

// TestRebindMovesAHeldRecordOntoAnEngineWithoutALookup measures the trap
// WithServer closes. A record made before the engine existed keeps forking and
// says nothing about it, so the failure is a silent performance one; the point
// of the guard is that moving the record costs no tmux command at all.
//
//libtmux:real-tmux
func TestRebindMovesAHeldRecordOntoAnEngineWithoutALookup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server, connected, runner, engine := connectedHarness(ctx, t)
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	held := snapshot.Sessions()[0]

	runner.reset()
	engine.reset()
	if _, err := held.Refresh(ctx); err != nil {
		t.Fatalf("Refresh() through the record's own handle error = %v", err)
	}
	stale := runner.total()
	if stale == 0 {
		t.Fatal("a record made before the engine existed started no tmux process")
	}

	runner.reset()
	engine.reset()
	bound := held.WithServer(connected)
	if runner.total() != 0 || engine.total() != 0 {
		t.Fatalf(
			"WithServer() ran %d processes and %d control commands, want none",
			runner.total(),
			engine.total(),
		)
	}
	if bound.ID() != held.ID() {
		t.Fatalf("WithServer() ID = %q, want %q", bound.ID(), held.ID())
	}

	if _, err := bound.Refresh(ctx); err != nil {
		t.Fatalf("Refresh() through the rebound handle error = %v", err)
	}
	if runner.total() != 0 {
		t.Fatalf("the rebound record started %d tmux processes, want none", runner.total())
	}
	if engine.total() == 0 {
		t.Fatal("the rebound record carried no commands over the engine")
	}

	// A snapshot's relations are read out of state the original handle built, so
	// a rebind that stopped at the receiver would hand back forking children.
	runner.reset()
	for _, window := range bound.Windows() {
		if _, err := window.Refresh(ctx); err != nil {
			t.Fatalf("Refresh() through a rebound relation error = %v", err)
		}
		for _, pane := range window.Panes() {
			if _, err := pane.Refresh(ctx); err != nil {
				t.Fatalf("Refresh() through a rebound pane error = %v", err)
			}
		}
	}
	if runner.total() != 0 {
		t.Fatalf("rebound relations started %d tmux processes, want none", runner.total())
	}
}

// TestFileCaptureWatchLoopStartsNoProcesses is the measurement the fast path
// claims: a loop that reads a pane every round over a control-mode engine
// starts no tmux process at all, where the same loop written with Pane.Capture
// starts one per round.
//
//libtmux:real-tmux
func TestFileCaptureWatchLoopStartsNoProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server, connected, runner, engine := connectedHarness(ctx, t)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	pane, ok, err := sessions[0].ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("ResolveActivePane() = (%t, %v), want a pane", ok, err)
	}
	bound := pane.WithServer(connected)
	path := filepath.Join(t.TempDir(), "pane.txt")

	const rounds = 30
	runner.reset()
	engine.reset()
	for range rounds {
		panes, err := connected.SearchPanes(ctx, nil)
		if err != nil || len(panes) == 0 {
			t.Fatalf("SearchPanes() = (%d panes, %v)", len(panes), err)
		}
		if _, err := bound.CaptureToFile(ctx, path, tmux.CapturePaneRequest{}); err != nil {
			t.Fatalf("CaptureToFile() error = %v", err)
		}
	}
	t.Logf(
		"%d rounds of SearchPanes plus CaptureToFile: %d tmux processes, %d control commands",
		rounds,
		runner.total(),
		engine.total(),
	)
	if runner.total() != 0 {
		t.Fatalf("the watch loop started %d tmux processes, want none", runner.total())
	}

	// The same loop written with a printed capture is what this replaces.
	runner.reset()
	if _, err := bound.Capture(ctx, tmux.CapturePaneRequest{}); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	t.Logf("one round of the same loop written with Pane.Capture: %d tmux process", runner.total())
	if runner.total() != 1 {
		t.Fatalf("Capture() started %d tmux processes, want one", runner.total())
	}
}

// TestFileCaptureReportsTheSameContentAsAPrintedCapture keeps the fast path
// honest: it is worth reaching for only while its result is the result the
// documented capture would have produced.
//
//libtmux:real-tmux
func TestFileCaptureReportsTheSameContentAsAPrintedCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server, connected, _, _ := connectedHarness(ctx, t)
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	pane, ok, err := sessions[0].ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("ResolveActivePane() = (%t, %v), want a pane", ok, err)
	}
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: tmux.Ptr("printf 'capture-marker\\n'"),
	}); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	if err := tmuxtest.WaitFor(ctx, 20*time.Millisecond, func(ctx context.Context) (bool, error) {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err != nil {
			return false, err
		}
		return slices.Contains(lines, "capture-marker"), nil
	}); err != nil {
		t.Fatalf("wait for pane output: %v", err)
	}

	// Each capture is its own tmux command, so a pane that changes between two
	// of them, as a shell redrawing its prompt does, fails a comparison that
	// has nothing to do with the transports. The set is taken again when they
	// disagree, and only a pane that never holds still fails.
	bound := pane.WithServer(connected)
	path := filepath.Join(t.TempDir(), "pane.txt")
	request := tmux.CapturePaneRequest{Start: tmux.CaptureBoundary, End: tmux.CaptureBoundary}

	var lines, printed []string
	var contents, exact []byte
	for attempt := range 10 {
		var err error
		if lines, err = bound.CaptureToFile(ctx, path, request); err != nil {
			t.Fatalf("CaptureToFile() error = %v", err)
		}
		if contents, err = os.ReadFile(path); err != nil {
			t.Fatalf("read the captured file: %v", err)
		}
		if printed, err = bound.Capture(ctx, request); err != nil {
			t.Fatalf("Capture() error = %v", err)
		}
		if exact, err = bound.CaptureBytes(ctx, request); err != nil {
			t.Fatalf("CaptureBytes() error = %v", err)
		}
		if slices.Equal(lines, printed) && bytes.Equal(contents, exact) {
			break
		}
		if attempt == 9 {
			t.Fatalf("the pane never held still: CaptureToFile() = %#v, Capture() = %#v, "+
				"file holds %d bytes, CaptureBytes() reports %d",
				lines, printed, len(contents), len(exact))
		}
	}

	// The scratch buffer does not outlive the call, so an interactive paste
	// still reaches whatever the user last copied.
	buffers, err := connected.ListBuffers(ctx, tmux.ListBuffersRequest{})
	if err != nil {
		t.Fatalf("ListBuffers() error = %v", err)
	}
	for _, buffer := range buffers {
		if strings.Contains(buffer, "libtmux-go-capture-") {
			t.Fatalf("CaptureToFile() left the buffer %q behind", buffer)
		}
	}
}

// TestControlPoolServesConcurrentCallersWithoutProcesses covers the reason a
// pool holds more than one connection: a single one serializes commands, so
// concurrent callers queue behind each other.
//
//libtmux:real-tmux
func TestControlPoolServesConcurrentCallersWithoutProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mutex sync.Mutex
	var processes int
	counting := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		mutex.Lock()
		processes++
		mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})

	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		Runner:     counting,
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "pool"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	connected, live, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{
		Connections: 4,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if got := pool.Connections(); got != 4 {
		t.Fatalf("pool holds %d connections, want 4", got)
	}
	_ = connected

	mutex.Lock()
	processes = 0
	mutex.Unlock()

	var group sync.WaitGroup
	failures := make(chan error, 4)
	for worker := range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 10 {
				if _, err := live.SearchWindows(ctx, nil); err != nil {
					failures <- fmt.Errorf("worker %d: %w", worker, err)
					return
				}
			}
		}()
	}
	group.Wait()
	close(failures)
	if err := <-failures; err != nil {
		t.Fatalf("concurrent read: %v", err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if processes != 0 {
		t.Fatalf("40 concurrent reads over a pool started %d tmux processes, want 0", processes)
	}
	if got := pool.Connections(); got != 4 {
		t.Fatalf("pool ended with %d connections, want 4", got)
	}
}

// TestARecordOutlivesThePoolThatCarriedIt covers the reason a closed pool
// stops claiming commands rather than failing them. A function that builds
// something over a pool and returns what it built hands back records on the
// pooled handle; closing the pool on the way out must leave the caller with
// records that are slower again, not records that are broken.
//
//libtmux:real-tmux
func TestARecordOutlivesThePoolThatCarriedIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mutex sync.Mutex
	var processes int
	var warnings []tmux.Warning
	counting := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		mutex.Lock()
		processes++
		mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})

	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		Runner:     counting,
		WarningHandler: func(warning tmux.Warning) {
			mutex.Lock()
			warnings = append(warnings, warning)
			mutex.Unlock()
		},
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "outlive"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, live, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	mutex.Lock()
	processes = 0
	mutex.Unlock()
	if _, err := live.SearchWindows(ctx, nil); err != nil {
		t.Fatalf("read over the pool: %v", err)
	}
	mutex.Lock()
	overPool := processes
	mutex.Unlock()
	if overPool != 0 {
		t.Fatalf("a read over the pool started %d processes, want 0", overPool)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}

	// The same record, after the pool it was made through is gone.
	windows, err := live.SearchWindows(ctx, nil)
	if err != nil {
		t.Fatalf("read after the pool closed: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("read after close returned %d windows, want 1", len(windows))
	}

	mutex.Lock()
	defer mutex.Unlock()
	if processes == 0 {
		t.Fatal("a read after the pool closed started no process, so it did not fall back")
	}
	var told bool
	for _, warning := range warnings {
		if warning.Kind == tmux.WarningControlPoolClosed {
			told = true
		}
	}
	if !told {
		t.Fatalf("falling back to a process was not reported: %+v", warnings)
	}
}

// TestEngineReportsTheChosenTransport covers the reader a library uses to tell
// whether a handle's owner already chose how commands reach tmux.
//
//libtmux:real-tmux
func TestEngineReportsTheChosenTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(context.Background(), t)

	if server.Engine() != nil {
		t.Fatal("a fresh handle reports an engine, want nil for process execution")
	}
	if chosen := server.WithEngine(server.SubprocessEngine()).Engine(); chosen == nil {
		t.Fatal("a handle told to stay on processes reports no engine")
	}

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "engine"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	connected, _, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if connected.Engine() == nil {
		t.Fatal("a pooled handle reports no engine")
	}
	if server.Engine() != nil {
		t.Fatal("opening a pool changed the handle it was opened from")
	}
}

// TestThePoolHandsBackAConnectedSession pins the seam a source-blind reader
// fell through. The session a caller passes in keeps starting a tmux process
// per command, so the pool must not hand that same value back: the one it
// returns, and the one its own accessor reports, both carry the connections.
//
//libtmux:real-tmux
func TestThePoolHandsBackAConnectedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mutex sync.Mutex
	var processes int
	counting := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		mutex.Lock()
		processes++
		mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})

	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		Runner:     counting,
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})

	passed, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "seam"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, returned, pool, err := server.OpenControlPool(ctx, passed, tmux.ControlPoolRequest{})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	for _, source := range []struct {
		name    string
		session tmux.Session
	}{
		{"the session the pool returned", returned},
		{"the session its accessor reports", pool.Session()},
	} {
		mutex.Lock()
		processes = 0
		mutex.Unlock()
		if _, err := source.session.SearchWindows(ctx, nil); err != nil {
			t.Fatalf("%s: %v", source.name, err)
		}
		mutex.Lock()
		started := processes
		mutex.Unlock()
		if started != 0 {
			t.Errorf("a read through %s started %d processes, want 0", source.name, started)
		}
	}
}

// TestABlockingCommandHoldsItsConnection covers the hazard the pool's
// documentation warns about: a command that blocks inside tmux occupies a
// connection until it is answered, so a pool with one connection carries
// nothing else meanwhile.
//
//libtmux:real-tmux
func TestABlockingCommandHoldsItsConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server := tmuxtest.NewServer(context.Background(), t)
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "blocking"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	connected, live, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	waiting := make(chan error, 1)
	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer waitCancel()
		waiting <- connected.WaitFor(waitCtx, tmux.WaitForRequest{Channel: "held"})
	}()

	// Once that command holds the only connection, another cannot be carried.
	// The wait is issued from a goroutine, so reads are retried until one is
	// refused rather than assuming the goroutine got there first.
	blocked := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		occupied, occupiedCancel := context.WithTimeout(ctx, 300*time.Millisecond)
		_, err = live.SearchWindows(occupied, nil)
		occupiedCancel()
		if errors.Is(err, context.DeadlineExceeded) {
			blocked = true
			break
		}
		if err != nil {
			t.Fatalf("read behind a blocking command: %v", err)
		}
	}
	if !blocked {
		t.Fatal("a read was carried while the only connection was held by a blocking command")
	}

	// Answering it frees the connection, and the pool carries commands again.
	if err := server.WaitFor(ctx, tmux.WaitForRequest{
		Channel: "held",
		Mode:    tmux.WaitForModeSignal,
	}); err != nil {
		t.Fatalf("signal: %v", err)
	}
	if err := <-waiting; err != nil {
		t.Fatalf("blocked command: %v", err)
	}
	if _, err := live.SearchWindows(ctx, nil); err != nil {
		t.Fatalf("read after the connection was freed: %v", err)
	}
}

// TestAWarningPrintsItself covers logging a warning without reaching for a
// field, which is what a handler written in one line does.
func TestAWarningPrintsItself(t *testing.T) {
	t.Parallel()
	warning := tmux.Warning{Message: "control pool is closed: server started a tmux process instead"}
	if got := warning.String(); got != warning.Message {
		t.Fatalf("String() = %q, want the message", got)
	}
	if got := fmt.Sprintf("%v", warning); got != warning.Message {
		t.Fatalf("formatted as %q, want the message", got)
	}
}

// TestAConnectionProvesItsOwnServerIdentity covers the second identity probe a
// snapshot read otherwise pays. Its only job is to prove that the listing came
// from one tmux server instance, which a connection already guarantees: tmux
// ends a client with the server it attached to, so a client that answers at
// all answers from the instance it was opened against.
//
//libtmux:real-tmux
func TestAConnectionProvesItsOwnServerIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mutex sync.Mutex
	carried := map[string]int{}
	// A process carries the client-global selectors ahead of the subcommand,
	// so the subcommand is found rather than assumed to be first.
	count := func(request tmux.CommandRequest) {
		mutex.Lock()
		defer mutex.Unlock()
		for _, argument := range request.Arguments {
			if !strings.HasPrefix(argument, "-") {
				carried[argument]++
				return
			}
		}
	}
	counting := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		count(request)
		return tmux.SubprocessRunner().Run(ctx, request)
	})

	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		Runner:     counting,
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "identity"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Forking: the guard costs a probe on each side of the listing.
	mutex.Lock()
	carried = map[string]int{}
	mutex.Unlock()
	if _, err := session.SearchWindows(ctx, nil); err != nil {
		t.Fatalf("read over processes: %v", err)
	}
	mutex.Lock()
	forkingProbes := carried["display-message"]
	mutex.Unlock()
	if forkingProbes != 2 {
		t.Fatalf("a forking read issued %d identity probes, want 2", forkingProbes)
	}

	// Connected: the transport proves it, so only the opening probe remains.
	_, live, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	recorder := &subcommandEngine{inner: pool.Engine(), seen: map[string]int{}}
	instrumented := live.WithServer(server.WithEngine(recorder))
	if _, err := instrumented.SearchWindows(ctx, nil); err != nil {
		t.Fatalf("read over the connection: %v", err)
	}
	if got := recorder.total("display-message"); got != 1 {
		t.Fatalf("a connected read issued %d identity probes, want 1", got)
	}
}

// subcommandEngine records which tmux subcommands an engine carried.
type subcommandEngine struct {
	inner tmux.Engine
	mu    sync.Mutex
	seen  map[string]int
}

func (e *subcommandEngine) Supports(kind tmux.CommandKind) bool { return e.inner.Supports(kind) }

func (e *subcommandEngine) Run(
	ctx context.Context,
	kind tmux.CommandKind,
	request tmux.CommandRequest,
) (tmux.CommandResult, error) {
	if len(request.Arguments) != 0 {
		e.mu.Lock()
		e.seen[request.Arguments[0]]++
		e.mu.Unlock()
	}
	return e.inner.Run(ctx, kind, request)
}

// instanceBound forwards the wrapped engine's optional behavior. A wrapper
// that does not forward silently drops it, which is what this test would
// otherwise measure instead of the library.
func (e *subcommandEngine) InstanceBound() bool {
	bound, ok := e.inner.(tmux.InstanceBoundEngine)
	return ok && bound.InstanceBound()
}

func (e *subcommandEngine) total(subcommand string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.seen[subcommand]
}

// TestATmuxFilterNarrowsTheListing covers the expression form the TmuxFilter
// documentation shows, since a raw tmux format is the one thing in this
// package a caller cannot assemble from Go values.
//
//libtmux:real-tmux
func TestATmuxFilterNarrowsTheListing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(context.Background(), t)
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "filter"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for range 2 {
		if _, err := session.NewWindow(ctx, tmux.NewWindowRequest{}); err != nil {
			t.Fatalf("create window: %v", err)
		}
	}

	all, err := session.SearchWindows(ctx, nil)
	if err != nil {
		t.Fatalf("search windows: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("session holds %d windows, want 3", len(all))
	}

	active := tmux.TmuxFilter("#{==:#{window_active},1}")
	filtered, err := session.SearchWindows(ctx, &active)
	if err != nil {
		t.Fatalf("search with a tmux filter: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("the documented filter returned %d windows, want 1", len(filtered))
	}
}
