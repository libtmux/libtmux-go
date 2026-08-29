package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type gatedCloser struct {
	started chan struct{}
	release chan struct{}
}

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
	if session, err := instance.Connect(t.Context(), serverTransport, nil); err == nil {
		_ = session.Close()
		t.Fatal("Connect() after Close() succeeded")
	}
}

func TestSessionJobsAreIsolatedAndReleasedOnDisconnect(t *testing.T) {
	instance := mustInternalMCPServer(t, mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "session-jobs-unused",
	}))

	connect := func() *ServerSession {
		_, transport := mcp.NewInMemoryTransports()
		session, err := instance.Connect(t.Context(), transport, nil)
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
	if !first.scope.jobs.keep(&job{
		id: "opaque-handle", directory: directory, finished: true,
		started: time.Now(), ended: time.Now(),
	}) {
		t.Fatal("first session refused a job")
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
		session, err := instance.Connect(t.Context(), transport, nil)
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
		session, err := instance.Connect(t.Context(), transport, nil)
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := instance.tools.subscribe(ctx, request(first)); err != nil {
		t.Fatal(err)
	}
	if got := instance.tools.watchers.subscriptionCount(uri); got != 1 {
		t.Fatalf("watcher subscriptions = %d after first subscribe, want 1", got)
	}
	if err := instance.tools.unsubscribe(ctx, &mcp.UnsubscribeRequest{
		Session: second.sdk,
		Params:  &mcp.UnsubscribeParams{URI: uri},
	}); err != nil {
		t.Fatal(err)
	}
	if got := instance.tools.watchers.subscriptionCount(uri); got != 1 {
		t.Fatalf("foreign unsubscribe changed count to %d, want 1", got)
	}
	if err := instance.tools.subscribe(ctx, request(first)); err != nil {
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
	session, err := instance.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !session.scope.jobs.keep(&job{id: "job", directory: jobDirectory}) {
		t.Fatal("session refused a job before shutdown")
	}

	auditFile, ok := instance.audit.(*os.File)
	if !ok {
		t.Fatalf("audit owner = %T, want *os.File", instance.audit)
	}

	const uri = "tmux://panes/%9/content"
	instance.tools.watchers.subscribed[uri] = 1
	instance.tools.watchers.spelled[uri] = map[string]int{uri: 1}
	instance.tools.watchers.notify(t.Context(), uri)
	instance.tools.watchers.notify(t.Context(), uri)
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
