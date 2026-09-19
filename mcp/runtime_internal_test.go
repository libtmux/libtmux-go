package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewServerRejectsAConnectionBoundTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	session, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "runtime-admission"})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	instance, err := NewServer(connection.Server())
	if instance != nil || !errors.Is(err, ErrRuntimeTargetBound) {
		t.Fatalf("NewServer() = (%p, %v), want (nil, ErrRuntimeTargetBound)", instance, err)
	}
}

func TestRuntimeOwnsOneCommandConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "runtime-existing"}); err != nil {
		t.Fatal(err)
	}

	runtime := newRuntime(ctx, target)
	command, err := runtime.command(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	runtime.mutex.Lock()
	state := runtime.state
	original := runtime.original
	commandConnection := runtime.commandConnection
	runtime.mutex.Unlock()
	if state != runtimeBound {
		t.Fatalf("state = %v, want runtimeBound", state)
	}
	if original.ID() == "" {
		t.Fatal("bound runtime did not retain its original materialized session")
	}
	if commandConnection == nil {
		t.Fatal("runtime did not retain its command connection")
	}
	if commandConnection.Session().ID() != original.ID() {
		t.Fatalf("command connection session = %s, want original %s",
			commandConnection.Session().ID(), original.ID())
	}
	if !command.ConnectionBound() {
		t.Fatal("terminal command server is not bound to the runtime connection")
	}
	process, err := runtime.process(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if process.ConnectionBound() {
		t.Fatal("process acquisition inherited a terminal connection")
	}
}

// TestObserveNeverLeavesALostConnectionCurrent guards the recovery path
// itself: observe must clear commandConnection and original along with
// marking the runtime terminal, not just close the connection in the
// background. current() reads commandConnection first regardless of state,
// so leaving it set would hand a request the exact connection this call just
// decided was lost or untrusted while it closes underneath the caller.
func TestObserveNeverLeavesALostConnectionCurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "runtime-observe-nil"}); err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	t.Cleanup(func() { _ = runtime.Close() })
	if _, err := runtime.command(ctx); err != nil {
		t.Fatal(err)
	}
	runtime.mutex.Lock()
	bound := runtime.commandConnection
	runtime.mutex.Unlock()
	if bound == nil {
		t.Fatal("runtime did not bind a real command connection")
	}

	runtime.observe(tmux.ErrControlClosed)

	runtime.mutex.Lock()
	afterConnection := runtime.commandConnection
	afterOriginal := runtime.original
	runtime.mutex.Unlock()
	if afterConnection != nil {
		t.Fatal("observe() left a lost command connection stored as current")
	}
	if afterOriginal.ID() != "" {
		t.Fatal("observe() left the lost connection's session stored as current")
	}
	if current := runtime.current(); current.ConnectionBound() {
		t.Fatalf("current() = %#v, want an unbound handle once the daemon is lost", current)
	}
}

func TestAwaitCommandRechecksCompletionAtItsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "deadline-result"})
	if err != nil {
		t.Fatal(err)
	}
	pane, err := created.ResolveActivePane(ctx)
	if err != nil {
		t.Fatalf("ResolveActivePane() error = %v", err)
	}
	runtime := newRuntime(ctx, target)
	t.Cleanup(func() { _ = runtime.Close() })
	directory := t.TempDir()
	statusPath := filepath.Join(directory, "status")
	openedPath := filepath.Join(directory, "opened")
	closedPath := filepath.Join(directory, "closed")
	if err := os.WriteFile(statusPath, []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{openedPath, closedPath} {
		if err := os.WriteFile(path, []byte("0 0 0 80 24"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	registry := &tools{runtime: runtime}
	_, output, err := registry.awaitCommand(ctx, ctx, awaiting{
		pane: pane, statusPath: statusPath,
		openedPath: openedPath, closedPath: closedPath,
		limits: bounds{lines: 100, bytes: 10_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.TimedOut || output.ExitStatus == nil || *output.ExitStatus != 7 {
		t.Fatalf("awaitCommand() = %+v, want completed status 7", output)
	}
}

func TestAbsentRuntimeStaysUnboundUntilAtomicCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "runtime.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime := newRuntime(ctx, target)
	process, err := runtime.command(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if process.ConnectionBound() {
		t.Fatal("absent acquisition invented a connection")
	}
	runtime.mutex.Lock()
	state := runtime.state
	runtime.mutex.Unlock()
	if state != runtimeUnbound {
		t.Fatalf("state after absent probe = %v, want runtimeUnbound", state)
	}

	if _, err := runtime.createSession(ctx, tmux.NewSessionRequest{
		Command: "invalid\x00command",
	}); err == nil {
		t.Fatal("invalid first creation succeeded")
	}
	runtime.mutex.Lock()
	state = runtime.state
	runtime.mutex.Unlock()
	if state != runtimeUnbound {
		t.Fatalf("state after rejected first creation = %v, want runtimeUnbound", state)
	}

	created, err := runtime.createSession(ctx, tmux.NewSessionRequest{Name: "runtime-created"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = target.Kill(context.Background())
	})
	if created.ID() == "" || !created.Server().ConnectionBound() {
		t.Fatalf("created session = %#v, want a command-bound session", created)
	}
	runtime.mutex.Lock()
	state = runtime.state
	original := runtime.original
	runtime.mutex.Unlock()
	if state != runtimeBound || original.ID() != created.ID() {
		t.Fatalf("state/original = (%v, %s), want (runtimeBound, %s)",
			state, original.ID(), created.ID())
	}
}

func TestBootstrapWaitsForAnUnboundRequestToDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "runtime-lease.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	acquired, err := runtime.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	type creation struct {
		session tmux.Session
		err     error
	}
	created := make(chan creation, 1)
	go func() {
		session, createErr := runtime.createSession(
			ctx,
			tmux.NewSessionRequest{Name: "after-lease"},
		)
		created <- creation{session: session, err: createErr}
	}()

	deadline := time.Now().Add(time.Second)
	for {
		runtime.mutex.Lock()
		state := runtime.state
		runtime.mutex.Unlock()
		if state == runtimeBinding {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("creation never reserved the binding transition")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case result := <-created:
		t.Fatalf("creation crossed a live unbound lease: (%#v, %v)", result.session, result.err)
	default:
	}
	if alive, aliveErr := target.IsAlive(ctx); aliveErr != nil || alive {
		t.Fatalf("target before lease release = (%t, %v), want absent", alive, aliveErr)
	}

	acquired.release()
	result := <-created
	if result.err != nil {
		t.Fatal(result.err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = target.Kill(context.Background())
	})
}

func TestDiscoveryWaitsForAnUnboundRequestToDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	//nolint:usetesting // t.TempDir can exceed the Unix socket path limit.
	directory, err := os.MkdirTemp("", "mcp-lease-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(directory, "s"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	first, err := runtime.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	leasedCtx := withAcquiredServer(ctx, first)

	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "external"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = target.Kill(context.Background())
	})

	secondResult := make(chan error, 1)
	go func() {
		second, acquireErr := runtime.acquire(ctx)
		if second != nil {
			second.release()
		}
		secondResult <- acquireErr
	}()
	waitForRuntimeState(t, runtime, runtimeBinding)

	if process, processErr := runtime.process(leasedCtx); processErr != nil || process.ConnectionBound() {
		t.Fatalf("leased process during drain = (%#v, %v), want original process handle", process, processErr)
	}
	select {
	case err := <-secondResult:
		t.Fatalf("discovery crossed a live unbound lease: %v", err)
	default:
	}

	first.release()
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryReprobesWhenItsCandidateDisappears(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	runtime := newRuntime(ctx, target)
	lease, err := runtime.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	first, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "stale-candidate"})
	if err != nil {
		t.Fatal(err)
	}
	survivor, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "surviving-candidate"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
	})

	acquired := make(chan error, 1)
	go func() {
		selection, acquireErr := runtime.acquire(ctx)
		if selection != nil {
			selection.release()
		}
		acquired <- acquireErr
	}()
	waitForRuntimeState(t, runtime, runtimeBinding)
	if err := first.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	lease.release()

	if err := <-acquired; err != nil {
		t.Fatalf("acquire after stale candidate: %v", err)
	}
	runtime.mutex.Lock()
	state := runtime.state
	original := runtime.original
	runtime.mutex.Unlock()
	if state != runtimeBound || original.ID() != survivor.ID() {
		t.Fatalf("state/original = (%v, %s), want (runtimeBound, %s)",
			state, original.ID(), survivor.ID())
	}
}

func TestToolSurfaceHoldsItsUnboundLeaseThroughTheHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "runtime-surface-lease.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	registry := &tools{runtime: runtime}
	entered := make(chan struct{})
	finish := make(chan struct{})
	handler := withRequestRuntime(registry, func(
		_ context.Context,
		_ *sdk.CallToolRequest,
		_ struct{},
	) (*sdk.CallToolResult, struct{}, error) {
		close(entered)
		<-finish
		return nil, struct{}{}, nil
	})
	handled := make(chan error, 1)
	go func() {
		_, _, handlerErr := handler(ctx, nil, struct{}{})
		handled <- handlerErr
	}()
	<-entered

	type createdResult struct {
		session tmux.Session
		err     error
	}
	createCtx, cancelCreate := context.WithCancel(ctx)
	created := make(chan createdResult, 1)
	go func() {
		createdSession, createErr := runtime.createSession(
			createCtx,
			tmux.NewSessionRequest{Name: "after-surface"},
		)
		created <- createdResult{session: createdSession, err: createErr}
	}()
	waitForRuntimeState(t, runtime, runtimeBinding)
	if alive, aliveErr := target.IsAlive(ctx); aliveErr != nil || alive {
		t.Fatalf("target during handler = (%t, %v), want absent", alive, aliveErr)
	}

	cancelCreate()
	result := <-created
	if !errors.Is(result.err, context.Canceled) || result.session.ID() != "" {
		t.Fatalf("canceled create = (%s, %v), want empty session and context canceled", result.session.ID(), result.err)
	}
	close(finish)
	if err := <-handled; err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForRuntimeState(t *testing.T, runtime *tmuxRuntime, want runtimeState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		runtime.mutex.Lock()
		state := runtime.state
		runtime.mutex.Unlock()
		if state == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime state = %v, want %v", state, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTerminalProbeFailurePoisonsRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-probe-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	runtime.deps.probeSessions = func(context.Context, tmux.Server) ([]tmux.Session, error) {
		return nil, tmux.ErrDaemonReplaced
	}

	if _, err := runtime.command(context.Background()); !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("command error = %v, want ErrDaemonReplaced", err)
	}
	if ctx.Err() != nil {
		t.Fatal("a terminal probe failure must not cancel the MCP process; it stays recoverable")
	}
	runtime.mutex.Lock()
	state := runtime.state
	runtime.mutex.Unlock()
	if state != runtimeTerminal {
		t.Fatalf("state = %v, want runtimeTerminal", state)
	}
}

func TestNoServerBecomesTerminalOnlyAfterBinding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-no-server-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)

	runtime.observe(tmux.ErrNoServer)
	runtime.mutex.Lock()
	state := runtime.state
	runtime.state = runtimeBound
	runtime.mutex.Unlock()
	if state != runtimeUnbound || ctx.Err() != nil {
		t.Fatalf("unbound absence = (%v, %v), want (runtimeUnbound, nil)", state, ctx.Err())
	}

	runtime.observe(tmux.ErrNoServer)
	runtime.mutex.Lock()
	state = runtime.state
	cause := runtime.cause
	runtime.mutex.Unlock()
	if state != runtimeTerminal || !errors.Is(cause, tmux.ErrNoServer) {
		t.Fatalf("bound absence = (%v, %v), want terminal ErrNoServer", state, cause)
	}
	if ctx.Err() != nil {
		t.Fatal("bound absence must not cancel the MCP process; the loss is reported, not fatal")
	}
}

func TestLateBindingCannotReviveTerminalRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-late-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	runtime.mutex.Lock()
	runtime.state = runtimeBinding
	runtime.binding = make(chan struct{})
	runtime.mutex.Unlock()
	runtime.observe(tmux.ErrControlClosed)

	err = runtime.finishBinding(tmux.Session{}, nil)
	if !errors.Is(err, tmux.ErrControlClosed) {
		t.Fatalf("late finishBinding error = %v, want ErrControlClosed", err)
	}
	runtime.mutex.Lock()
	state := runtime.state
	runtime.mutex.Unlock()
	if state != runtimeTerminal {
		t.Fatalf("state = %v, want runtimeTerminal", state)
	}
}

func TestPreConnectionCancellationReturnsToUnbound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-cancel-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	runtime.mutex.Lock()
	runtime.state = runtimeBinding
	runtime.binding = make(chan struct{})
	runtime.mutex.Unlock()

	runtime.failBinding(tmux.Session{}, nil, context.Canceled, false)
	runtime.mutex.Lock()
	state := runtime.state
	runtime.mutex.Unlock()
	if state != runtimeUnbound {
		t.Fatalf("state = %v, want runtimeUnbound", state)
	}
	if ctx.Err() != nil {
		t.Fatalf("pre-connection cancellation canceled runtime: %v", ctx.Err())
	}
}

func TestFailedCreationWithAnIDIsTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	acted, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "acted-creation"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)
	runtime.mutex.Lock()
	runtime.state = runtimeBinding
	runtime.binding = make(chan struct{})
	runtime.mutex.Unlock()
	want := errors.New("bootstrap reply lost")

	if retryable := runtime.failBinding(acted, nil, want, true); retryable {
		t.Fatal("failBinding() marked an acted creation retryable")
	}
	runtime.mutex.Lock()
	state := runtime.state
	cause := runtime.cause
	original := runtime.original
	runtime.mutex.Unlock()
	if state != runtimeTerminal || !errors.Is(cause, want) || original.ID() != acted.ID() {
		t.Fatalf("failed acted creation = (%v, %v, %s), want (terminal, %v, %s)",
			state, cause, original.ID(), want, acted.ID())
	}
	if ctx.Err() != nil {
		t.Fatal("a failed but acted create_session must not cancel the MCP process")
	}
}

// TestRuntimeTerminalErrorsHealOnTheNextAcquisition pins that a runtime
// a terminal error poisoned does not stay poisoned forever. The call that
// hit the error reports it (asserted directly on observe below); the next
// acquisition heals the runtime back to unbound instead of returning the
// stale cause on every future call.
func TestRuntimeTerminalErrorsHealOnTheNextAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-terminal-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)

	runtime.observe(errors.Join(tmux.ErrOutcomeUnknown, errors.New("reply lost")))
	if ctx.Err() != nil {
		t.Fatal("an indeterminate outcome must not cancel the MCP process")
	}
	runtime.mutex.Lock()
	state := runtime.state
	runtime.mutex.Unlock()
	if state != runtimeTerminal {
		t.Fatalf("state right after observe = %v, want runtimeTerminal", state)
	}

	// The next acquisition - standing in for the next tool call - heals the
	// runtime rather than reliving the stale cause forever. This target has
	// no daemon, so a healed, unbound read reports absence, not an error.
	if _, err := runtime.command(context.Background()); err != nil {
		t.Fatalf("command() after healing = %v, want nil (absent target, not an error)", err)
	}
	runtime.mutex.Lock()
	state = runtime.state
	runtime.mutex.Unlock()
	if state != runtimeUnbound {
		t.Fatalf("state after healing = %v, want runtimeUnbound", state)
	}
}

func TestContextualUnknownOutcomeDoesNotPoisonUnboundRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-context-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target)

	runtime.observe(errors.Join(tmux.ErrOutcomeUnknown, context.Canceled))
	runtime.mutex.Lock()
	state := runtime.state
	runtime.mutex.Unlock()
	if state != runtimeUnbound {
		t.Fatalf("state = %v, want runtimeUnbound", state)
	}
	if ctx.Err() != nil {
		t.Fatalf("contextual uncertainty canceled runtime: %v", ctx.Err())
	}
}
