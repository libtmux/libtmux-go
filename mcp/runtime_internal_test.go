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

	runtime := newRuntime(ctx, target, func(error) { cancel() })
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

func TestAwaitCommandRechecksCompletionAtItsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	created, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "deadline-result"})
	if err != nil {
		t.Fatal(err)
	}
	pane, ok, err := created.ResolveActivePane(ctx)
	if err != nil || !ok {
		t.Fatalf("ResolveActivePane() = (%v, %t, %v)", pane, ok, err)
	}
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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

	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "runtime-discovery-lease.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "runtime-probe-unused",
		Runner: tmux.CommandRunnerFunc(func(
			context.Context,
			tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			return tmux.CommandResult{ExitCode: -1}, tmux.ErrDaemonReplaced
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target, func(error) { cancel() })

	if _, err := runtime.command(context.Background()); !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("command error = %v, want ErrDaemonReplaced", err)
	}
	if ctx.Err() == nil {
		t.Fatal("terminal probe failure did not cancel its owner")
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
	runtime := newRuntime(ctx, target, func(error) { cancel() })

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
	if ctx.Err() == nil {
		t.Fatal("bound absence did not cancel its owner")
	}
}

func TestLateBindingCannotReviveTerminalRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-late-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	acted, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: "acted-creation"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target, func(error) { cancel() })
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
	if ctx.Err() == nil {
		t.Fatal("failed acted creation did not cancel its owner")
	}
}

func TestRuntimeTerminalErrorsNeverFallBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-terminal-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target, func(error) { cancel() })

	runtime.observe(errors.Join(tmux.ErrOutcomeUnknown, errors.New("reply lost")))
	if ctx.Err() == nil {
		t.Fatal("indeterminate runtime did not cancel its owner")
	}
	if _, err := runtime.command(context.Background()); !errors.Is(err, tmux.ErrOutcomeUnknown) {
		t.Fatalf("command after indeterminate outcome error = %v, want ErrOutcomeUnknown", err)
	}
	runtime.mutex.Lock()
	state := runtime.state
	runtime.mutex.Unlock()
	if state != runtimeTerminal {
		t.Fatalf("state = %v, want runtimeTerminal", state)
	}
}

func TestContextualUnknownOutcomeDoesNotPoisonUnboundRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "runtime-context-unused"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(ctx, target, func(error) { cancel() })

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
