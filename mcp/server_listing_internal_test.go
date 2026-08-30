package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestListServersUsesTargetsFrozenExecutionBinding(t *testing.T) {
	ambientRoot := t.TempDir()
	frozenRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", ambientRoot)
	directory := filepath.Join(frozenRoot, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(directory, "sibling")
	leaveInternalDeadUnixSocket(t, sibling)
	if err := os.WriteFile(filepath.Join(directory, "tmux.conf"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ambientDirectory := filepath.Join(ambientRoot, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(ambientDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ambient := filepath.Join(ambientDirectory, "ambient")
	leaveInternalDeadUnixSocket(t, ambient)

	frozenEnvironment := []string{"PATH=", "TMUX_TMPDIR=" + frozenRoot}
	options := executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
		SocketPath:         filepath.Join(t.TempDir(), "explicit-target.sock"),
		ProcessEnvironment: frozenEnvironment,
	})
	target := mustInternalTmuxServer(t, options)
	targetSelection, err := target.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	directory = targetSelection.NamedDirectory
	sibling = filepath.Join(directory, filepath.Base(sibling))
	ambient, err = filepath.EvalSymlinks(ambient)
	if err != nil {
		t.Fatal(err)
	}
	instance := mustInternalMCPServer(t, target)
	instance.runtime.deps.probeSessions = func(
		context.Context,
		tmux.Server,
	) ([]tmux.Session, error) {
		return nil, tmux.ErrNoServer
	}
	probed := make(chan tmux.Server, 1)
	instance.runtime.deps.probeSibling = func(
		_ context.Context,
		server tmux.Server,
	) (bool, int) {
		probed <- server
		return false, 0
	}

	_, output, err := instance.tools.listServers(
		t.Context(), nil, listServersInput{IncludeDead: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if output.SearchedIn != directory || output.Total != 2 {
		t.Fatalf("discovery = (%q, total %d), want %q and 2", output.SearchedIn, output.Total, directory)
	}
	for _, server := range output.Servers {
		if server.SocketPath == ambient {
			t.Fatalf("list_servers scanned ambient socket %q", ambient)
		}
	}
	probe := <-probed
	selection, err := probe.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	if selection.Path != sibling || probe.Executable() != options.Binary ||
		!slices.Equal(probe.ProcessEnvironment(), options.ProcessEnvironment) {
		t.Fatalf(
			"sibling probe = (%q, %q, %#v), want frozen (%q, %q, %#v)",
			selection.Path,
			probe.Executable(),
			probe.ProcessEnvironment(),
			sibling,
			options.Binary,
			options.ProcessEnvironment,
		)
	}
}

func TestListServersProbesSiblingSocketsConcurrently(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	const siblingCount = 8
	for index := range siblingCount {
		leaveInternalDeadUnixSocket(t, filepath.Join(directory, fmt.Sprintf("sibling-%d", index)))
	}
	target := mustInternalTmuxServer(t, executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
		SocketPath:         filepath.Join(t.TempDir(), "target"),
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
	}))
	instance := mustInternalMCPServer(t, target)
	instance.runtime.deps.probeSessions = func(
		context.Context,
		tmux.Server,
	) ([]tmux.Session, error) {
		return nil, tmux.ErrNoServer
	}
	started := make(chan struct{}, siblingCount)
	release := make(chan struct{})
	instance.runtime.deps.probeSibling = func(
		ctx context.Context,
		_ tmux.Server,
	) (bool, int) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return false, 0
	}
	type callResult struct {
		output listServersOutput
		err    error
	}
	returned := make(chan callResult, 1)
	go func() {
		_, output, err := instance.tools.listServers(
			t.Context(), nil, listServersInput{IncludeDead: true},
		)
		returned <- callResult{output: output, err: err}
	}()
	for range 2 {
		select {
		case <-started:
		case got := <-returned:
			t.Fatalf("list_servers returned before concurrent probes: %+v, %v", got.output, got.err)
		case <-time.After(time.Second):
			t.Fatal("sibling socket probes ran serially")
		}
	}
	close(release)
	got := <-returned
	if got.err != nil || got.output.Total != siblingCount+1 {
		t.Fatalf("list_servers = (total %d, %v), want total %d", got.output.Total, got.err, siblingCount+1)
	}
}

func TestListServersBoundsSiblingProbesByMaxServers(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	const siblingCount = 8
	for index := range siblingCount {
		leaveInternalDeadUnixSocket(t, filepath.Join(directory, fmt.Sprintf("sibling-%d", index)))
	}
	targetPath := filepath.Join(t.TempDir(), "target")
	target := mustInternalTmuxServer(t, executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
		SocketPath:         targetPath,
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
	}))
	instance := mustInternalMCPServer(t, target)
	var probes atomic.Int64
	instance.runtime.deps.probeSibling = func(context.Context, tmux.Server) (bool, int) {
		probes.Add(1)
		return false, 0
	}

	_, output, err := instance.tools.listServers(
		t.Context(), nil, listServersInput{IncludeDead: true, MaxServers: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 1 {
		t.Fatalf("maxServers 2 launched %d sibling probes, want 1", probes.Load())
	}
	if len(output.Servers) != 2 || !output.Servers[0].IsTarget ||
		output.Servers[0].SocketPath != targetPath {
		t.Fatalf("servers = %#v, want target plus at most one sibling", output.Servers)
	}
	if !output.Truncated {
		t.Fatal("bounded socket directory scan was not reported as truncated")
	}
}

func TestListServersReportsSocketDirectoryReadFailure(t *testing.T) {
	notDirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDirectory, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target := mustInternalTmuxServer(t, executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
		SocketName:         "target",
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + notDirectory},
	}))
	instance := mustInternalMCPServer(t, target)

	_, _, err := instance.tools.listServers(t.Context(), nil, listServersInput{})
	if err == nil {
		t.Fatal("list_servers swallowed a socket directory read failure")
	}
}

func TestProbeSiblingServerTreatsTimeoutAsDead(t *testing.T) {
	target := mustInternalTmuxServer(t, executableFixtureOptions(t, fixtureHang, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "sibling"),
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	alive, sessions := probeSiblingServer(ctx, target)
	if alive || sessions != 0 || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("timed-out sibling = (%t, %d, %v), want dead with local deadline", alive, sessions, ctx.Err())
	}
}

func TestListServersPropagatesOuterCancellationAfterSiblingProbe(t *testing.T) {
	//nolint:usetesting // t.TempDir can exceed the Unix socket path limit.
	root, err := os.MkdirTemp("", "mcp-list-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	directory := filepath.Join(root, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	leaveInternalDeadUnixSocket(t, filepath.Join(directory, "sibling"))
	target := mustInternalTmuxServer(t, executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
		SocketPath:         filepath.Join(t.TempDir(), "target"),
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
	}))
	instance := mustInternalMCPServer(t, target)
	instance.runtime.deps.probeSessions = func(
		context.Context,
		tmux.Server,
	) ([]tmux.Session, error) {
		return nil, tmux.ErrNoServer
	}
	started := make(chan struct{})
	instance.runtime.deps.probeSibling = func(ctx context.Context, _ tmux.Server) (bool, int) {
		close(started)
		<-ctx.Done()
		return false, 0
	}
	ctx, cancel := context.WithCancel(t.Context())
	returned := make(chan error, 1)
	go func() {
		_, _, err := instance.tools.listServers(ctx, nil, listServersInput{IncludeDead: true})
		returned <- err
	}()
	<-started
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("list_servers cancellation error = %v, want context canceled", err)
	}
}

func leaveInternalDeadUnixSocket(t testing.TB, path string) {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}
