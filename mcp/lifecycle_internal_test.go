package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type gatedCloser struct {
	started chan struct{}
	release chan struct{}
}

type responseGateTransport struct {
	inner     mcp.Transport
	armed     <-chan struct{}
	started   chan struct{}
	release   <-chan struct{}
	closed    chan struct{}
	writeErr  error
	once      sync.Once
	closeOnce sync.Once
	closes    atomic.Int64
}

func (t *responseGateTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &responseGateConnection{inner: connection, gate: t}, nil
}

type responseGateConnection struct {
	inner mcp.Connection
	gate  *responseGateTransport
}

func (c *responseGateConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	return c.inner.Read(ctx)
}

func (c *responseGateConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if _, response := message.(*jsonrpc.Response); !response {
		return c.inner.Write(ctx, message)
	}
	select {
	case <-c.gate.armed:
	default:
		return c.inner.Write(ctx, message)
	}
	c.gate.once.Do(func() { close(c.gate.started) })
	if c.gate.writeErr != nil {
		return c.gate.writeErr
	}
	select {
	case <-c.gate.release:
		return c.inner.Write(ctx, message)
	case <-c.gate.closed:
		return errResponseGateClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

var errResponseGateClosed = errors.New("response gate closed")

func (c *responseGateConnection) Close() error {
	c.gate.closes.Add(1)
	c.gate.closeOnce.Do(func() {
		if c.gate.closed != nil {
			close(c.gate.closed)
		}
	})
	return c.inner.Close()
}

func (c *responseGateConnection) SessionID() string { return c.inner.SessionID() }

func (c *gatedCloser) Close() error {
	close(c.started)
	<-c.release
	return nil
}

func TestInstanceCloseContextStartsShutdownWithCanceledContext(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "close-context-unused",
	}))
	gate := &gatedCloser{started: make(chan struct{}), release: make(chan struct{})}
	instance.audit = gate

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := instance.CloseContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext() error = %v, want context canceled", err)
	}
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("CloseContext() returned without starting shutdown")
	}
	close(gate.release)
	if err := instance.CloseContext(context.Background()); err != nil {
		t.Fatalf("CloseContext() after shutdown error = %v", err)
	}
}

func TestInstanceRejectsConnectAfterShutdownStarts(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "closed-connect-unused",
	}))
	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, serverTransport := mcp.NewInMemoryTransports()
	if session, err := instance.Connect(
		t.Context(), AssumeResponseCommit(serverTransport), nil,
	); err == nil {
		_ = session.Close()
		t.Fatal("Connect() after Close() succeeded")
	}
}

func terminalFailureInstance(t testing.TB, socketName string) *Instance {
	t.Helper()
	target := mustInternalTmuxServer(t, tmux.ServerOptions{SocketName: socketName})
	instance := mustInternalMCPServer(t, target)
	instance.runtime.deps.probeSessions = func(
		context.Context,
		tmux.Server,
	) ([]tmux.Session, error) {
		return nil, tmux.ErrDaemonReplaced
	}
	return instance
}

func TestTerminalToolFailureReachesCallerBeforeRunStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	instance := terminalFailureInstance(t, "terminal-run-unused")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	armed := make(chan struct{})
	release := make(chan struct{})
	gate := &responseGateTransport{
		inner: serverTransport, armed: armed,
		started: make(chan struct{}), release: release,
	}
	runResult := make(chan error, 1)
	go func() { runResult <- instance.Run(ctx, AssumeResponseCommit(gate)) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "terminal-run"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientSession.Close() }()
	close(armed)

	type callResult struct {
		result *mcp.CallToolResult
		err    error
	}
	called := make(chan callResult, 1)
	go func() {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "list_sessions", Arguments: map[string]any{},
		})
		called <- callResult{result: result, err: err}
	}()
	select {
	case <-gate.started:
	case <-ctx.Done():
		t.Fatal("terminal response write did not start")
	}
	select {
	case err := <-runResult:
		t.Fatalf("Run() returned before the terminal response write: %v", err)
	default:
	}
	select {
	case result := <-called:
		t.Fatalf("CallTool() returned before its response write: %#v", result)
	default:
	}
	close(release)
	var call callResult
	select {
	case call = <-called:
	case <-ctx.Done():
		t.Fatal("terminal response was not delivered")
	}
	result, callErr := call.result, call.err
	if callErr != nil {
		t.Fatalf("CallTool() error = %v, want a classified tool response", callErr)
	}
	responseText := ""
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			responseText += text.Text
		}
	}
	if !result.IsError || !strings.Contains(responseText, tmux.ErrDaemonReplaced.Error()) {
		t.Fatalf("CallTool() = (%t, %q), want ErrDaemonReplaced response", result.IsError, responseText)
	}
	select {
	case runErr := <-runResult:
		if !errors.Is(runErr, tmux.ErrDaemonReplaced) {
			t.Fatalf("Run() error = %v, want ErrDaemonReplaced", runErr)
		}
	case <-ctx.Done():
		t.Fatal("Run did not stop after terminal tool failure")
	}
}

func TestTerminalResponseWriteFailureClosesTheSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writeErr := errors.New("response write failed")
	instance := terminalFailureInstance(t, "terminal-write-unused")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	armed := make(chan struct{})
	gate := &responseGateTransport{
		inner: serverTransport, armed: armed,
		started: make(chan struct{}), writeErr: writeErr,
	}
	runResult := make(chan error, 1)
	go func() { runResult <- instance.Run(ctx, AssumeResponseCommit(gate)) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "terminal-write"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientSession.Close() }()
	close(armed)

	callDone := make(chan error, 1)
	go func() {
		_, callErr := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "list_sessions", Arguments: map[string]any{},
		})
		callDone <- callErr
	}()
	select {
	case <-gate.started:
	case <-ctx.Done():
		t.Fatal("failing terminal response write did not start")
	}
	var runErr error
	select {
	case runErr = <-runResult:
	case <-ctx.Done():
		t.Fatal("Run did not stop after response write failure")
	}
	if !errors.Is(runErr, tmux.ErrDaemonReplaced) || !errors.Is(runErr, writeErr) {
		t.Fatalf("Run() error = %v, want terminal and response-write failures", runErr)
	}
	var callErr error
	select {
	case callErr = <-callDone:
	case <-ctx.Done():
		t.Fatal("CallTool did not stop after response write failure")
	}
	if callErr == nil {
		t.Fatal("CallTool() succeeded after its response write failed")
	}
}

func TestTerminalResponseDrainTimeoutClosesAStuckWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	instance := terminalFailureInstance(t, "terminal-stuck-write-unused")
	instance.drainWait = 10 * time.Millisecond
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	armed := make(chan struct{})
	release := make(chan struct{})
	gate := &responseGateTransport{
		inner: serverTransport, armed: armed, started: make(chan struct{}),
		release: release, closed: make(chan struct{}),
	}
	runResult := make(chan error, 1)
	go func() { runResult <- instance.Run(ctx, AssumeResponseCommit(gate)) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "terminal-stuck-write"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientSession.Close() }()
	defer close(release)
	close(armed)

	callDone := make(chan error, 1)
	go func() {
		_, callErr := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "list_sessions", Arguments: map[string]any{},
		})
		callDone <- callErr
	}()
	select {
	case <-gate.started:
	case <-ctx.Done():
		t.Fatal("stuck terminal response write did not start")
	}
	select {
	case runErr := <-runResult:
		if !errors.Is(runErr, tmux.ErrDaemonReplaced) ||
			!errors.Is(runErr, errResponseGateClosed) {
			t.Fatalf("Run() error = %v, want terminal and forced-close failures", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("response drain timeout did not close the stuck write")
	}
	if closes := gate.closes.Load(); closes != 1 {
		t.Fatalf("transport Close() calls = %d, want 1", closes)
	}
	select {
	case callErr := <-callDone:
		if callErr == nil {
			t.Fatal("CallTool() succeeded after its response write was force-closed")
		}
	case <-time.After(time.Second):
		t.Fatal("CallTool() remained blocked after the response write was force-closed")
	}
}

func TestTerminalShutdownWaitsForEveryReadCallResponse(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "terminal-replies-unused",
	}))
	scope := newSessionScope(instance.ctx)
	firstID, err := jsonrpc.MakeID("first")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := jsonrpc.MakeID("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.requestRead(scope, &jsonrpc.Request{ID: firstID}); err != nil {
		t.Fatal(err)
	}
	if err := instance.requestRead(scope, &jsonrpc.Request{ID: secondID}); err != nil {
		t.Fatal(err)
	}
	instance.terminal(tmux.ErrDaemonReplaced)

	instance.responseSettled(scope, &jsonrpc.Response{ID: secondID})
	select {
	case <-instance.closeDone:
		t.Fatal("an unrelated response released the terminal response gate")
	default:
	}
	instance.responseSettled(scope, &jsonrpc.Response{ID: firstID})
	select {
	case <-instance.closeDone:
	case <-time.After(time.Second):
		t.Fatal("the final response did not release terminal shutdown")
	}
}

func TestSessionJobsAreIsolatedAndReleasedOnDisconnect(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "session-jobs-unused",
	}))

	connect := func() *ServerSession {
		_, transport := mcp.NewInMemoryTransports()
		session, err := instance.Connect(
			t.Context(), AssumeResponseCommit(transport), nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	first, second := connect(), connect()
	t.Cleanup(func() { _ = second.Close() })

	directory := filepath.Join(t.TempDir(), "first-job")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := first.scope.jobs.keep(&job{
		id: "opaque-handle", directory: directory, finished: true,
		started: time.Now(), ended: time.Now(),
	}); err != nil {
		t.Fatalf("first session keep job: %v", err)
	}

	firstRequest := &mcp.CallToolRequest{Session: first.sdk}
	if _, _, err := instance.tools.getJob(t.Context(), firstRequest, getJobInput{
		JobID: "opaque-handle",
	}); err != nil {
		t.Fatalf("own getJob() error = %v", err)
	}
	secondRequest := &mcp.CallToolRequest{Session: second.sdk}
	if _, _, err := instance.tools.getJob(t.Context(), secondRequest, getJobInput{
		JobID: "opaque-handle",
	}); err == nil {
		t.Fatal("another MCP session collected the first session's job")
	}

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job directory survived session disconnect: %v", err)
	}
}

func TestSessionConsentIsReleasedOnDisconnect(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "session-consent-unused",
	}))
	connect := func() *ServerSession {
		_, transport := mcp.NewInMemoryTransports()
		session, err := instance.Connect(
			t.Context(), AssumeResponseCommit(transport), nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	first, second := connect(), connect()
	t.Cleanup(func() { _ = second.Close() })
	firstRequest := &mcp.CallToolRequest{Session: first.sdk}
	secondRequest := &mcp.CallToolRequest{Session: second.sdk}

	instance.tools.remember(firstRequest, "%9")
	if !instance.tools.allowed(firstRequest, "%9") {
		t.Fatal("the granting MCP session did not retain consent")
	}
	if instance.tools.allowed(secondRequest, "%9") {
		t.Fatal("one MCP session inherited another session's consent")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if instance.tools.allowed(firstRequest, "%9") {
		t.Fatal("consent survived its MCP session")
	}
}

func TestSessionSubscriptionsAreIdempotentAndOwned(t *testing.T) {
	t.Setenv(CapabilitiesEnvironmentVariable, "all")
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "session-subscriptions-unused",
	}))
	connect := func() *ServerSession {
		_, transport := mcp.NewInMemoryTransports()
		session, err := instance.Connect(
			t.Context(), AssumeResponseCommit(transport), nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	first, second := connect(), connect()
	t.Cleanup(func() { _ = second.Close() })
	const uri = "tmux://sessions"
	request := func(session *ServerSession) *mcp.SubscribeRequest {
		return &mcp.SubscribeRequest{
			Session: session.sdk,
			Params:  &mcp.SubscribeParams{URI: uri},
		}
	}
	subscribe := func(request *mcp.SubscribeRequest) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		return instance.tools.subscribe(ctx, request)
	}
	if err := subscribe(request(first)); err != nil {
		t.Fatal(err)
	}
	if got := instance.tools.watchers.subscriptionCount(uri); got != 1 {
		t.Fatalf("watcher subscriptions = %d after first subscribe, want 1", got)
	}
	if err := instance.tools.unsubscribe(context.Background(), &mcp.UnsubscribeRequest{
		Session: second.sdk,
		Params:  &mcp.UnsubscribeParams{URI: uri},
	}); err != nil {
		t.Fatal(err)
	}
	if got := instance.tools.watchers.subscriptionCount(uri); got != 1 {
		t.Fatalf("foreign unsubscribe changed count to %d, want 1", got)
	}
	if err := subscribe(request(first)); err != nil {
		t.Fatal(err)
	}
	if got := instance.tools.watchers.subscriptionCount(uri); got != 1 {
		t.Fatalf("repeated subscribe changed count to %d, want 1", got)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := instance.tools.watchers.subscriptionCount(uri); got != 0 {
		t.Fatalf("disconnect left %d watcher subscriptions", got)
	}
}

func (w *watchers) subscriptionCount(uri string) int {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.subscribed[uri]
}

func mustInternalTmuxServer(t testing.TB, options tmux.ServerOptions) tmux.Server {
	t.Helper()
	server, err := tmux.NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func mustInternalMCPServer(t testing.TB, target tmux.Server) *Instance {
	t.Helper()
	server, err := NewServer(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func TestNewServerRejectsInvalidTargetBeforeAllocating(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(AuditEnvironmentVariable, auditPath)

	instance, err := NewServer(tmux.Server{})
	if !errors.Is(err, tmux.ErrInvalidServer) {
		t.Fatalf("NewServer() error = %v, want ErrInvalidServer", err)
	}
	if instance != nil {
		t.Fatalf("NewServer() instance = %v, want nil", instance)
	}
	if _, err := os.Stat(auditPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("audit file exists after rejected construction: %v", err)
	}
}

func TestInstanceCloseReleasesOwnedResources(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(AuditEnvironmentVariable, auditPath)
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "lifecycle-unused",
	}))

	jobDirectory := filepath.Join(t.TempDir(), "job")
	if err := os.Mkdir(jobDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	_, transport := mcp.NewInMemoryTransports()
	session, err := instance.Connect(t.Context(), AssumeResponseCommit(transport), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.scope.jobs.keep(&job{id: "job", directory: jobDirectory}); err != nil {
		t.Fatalf("session keep job before shutdown: %v", err)
	}

	auditFile, ok := instance.audit.(*os.File)
	if !ok {
		t.Fatalf("audit owner = %T, want *os.File", instance.audit)
	}

	const uri = "tmux://panes/%9/content"
	instance.tools.watchers.subscribed[uri] = 1
	instance.tools.watchers.spelled[uri] = map[string]int{uri: 1}
	instance.tools.watchers.notify(uri)
	instance.tools.watchers.notify(uri)
	if !instance.tools.watchers.owes(uri) {
		t.Fatal("watcher has no deferred notification to release")
	}

	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := os.Stat(jobDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job directory survived Close(): %v", err)
	}
	if _, err := auditFile.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("audit write after Close() error = %v, want closed", err)
	}
	if instance.tools.watchers.owes(uri) {
		t.Fatal("deferred notification survived Close()")
	}
	time.Sleep(watchNotifyInterval + 50*time.Millisecond)
	if !instance.tools.watchers.at(uri).IsZero() {
		t.Fatal("a deferred notification ran after Close()")
	}
}
