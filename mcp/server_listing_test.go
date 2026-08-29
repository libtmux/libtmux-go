package mcp_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListServersMarksAnAbsentImplicitTarget(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		socketName string
		wantName   string
	}{
		{name: "named", socketName: "application", wantName: "application"},
		{name: "default", wantName: "default"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(
				root,
				"tmux-"+strconv.Itoa(os.Getuid()),
			)
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			targetPath := filepath.Join(directory, testCase.wantName)
			leaveDeadUnixSocket(t, targetPath)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			target := mustTmuxServer(t, tmux.ServerOptions{
				Binary:             executable,
				SocketName:         testCase.socketName,
				ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
				Runner: tmux.CommandRunnerFunc(func(
					_ context.Context,
					request tmux.CommandRequest,
				) (tmux.CommandResult, error) {
					if slices.Equal(request.Arguments, []string{"-V"}) {
						return tmux.CommandResult{Stdout: []string{"tmux 3.6"}}, nil
					}
					return tmux.CommandResult{
						Command:  request.Arguments,
						Stderr:   []string{"no server running on " + targetPath},
						ExitCode: 1,
					}, nil
				}),
			})
			session, ctx := connectTarget(t, target)

			var listed struct {
				Servers []struct {
					SocketPath string `json:"socketPath"`
					IsTarget   bool   `json:"isTarget"`
				} `json:"servers"`
				SearchedIn string `json:"searchedIn"`
			}
			result := call(
				ctx,
				t,
				session,
				"list_servers",
				map[string]any{"includeDead": true},
				&listed,
			)
			if result.IsError {
				t.Fatalf("list_servers failed: %s", resultText(result))
			}
			if listed.SearchedIn != directory || len(listed.Servers) != 1 ||
				listed.Servers[0].SocketPath != targetPath ||
				!listed.Servers[0].IsTarget {
				t.Fatalf(
					"list_servers = %#v in %q, want dead target %q in %q",
					listed.Servers,
					listed.SearchedIn,
					targetPath,
					directory,
				)
			}
		})
	}
}

func TestListServersUsesTargetsFrozenExecutionBinding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	ambientRoot := t.TempDir()
	frozenRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", ambientRoot)
	directory := filepath.Join(frozenRoot, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(directory, "sibling")
	leaveDeadUnixSocket(t, sibling)
	notASocket := filepath.Join(directory, "tmux.conf")
	if err := os.WriteFile(notASocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ambientDirectory := filepath.Join(ambientRoot, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(ambientDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ambient := filepath.Join(ambientDirectory, "ambient")
	leaveDeadUnixSocket(t, ambient)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	targetPath := filepath.Join(t.TempDir(), "explicit-target.sock")
	frozenEnvironment := []string{"PATH=", "TMUX_TMPDIR=" + frozenRoot}

	var mutex sync.Mutex
	var requests []tmux.CommandRequest
	target := mustTmuxServer(t, tmux.ServerOptions{
		Binary:             executable,
		SocketPath:         targetPath,
		ProcessEnvironment: frozenEnvironment,
		Runner: tmux.CommandRunnerFunc(func(
			_ context.Context,
			request tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			mutex.Lock()
			requests = append(requests, request)
			mutex.Unlock()
			if slices.Equal(request.Arguments, []string{"-V"}) {
				return tmux.CommandResult{Stdout: []string{"tmux 3.6"}}, nil
			}
			if slices.Contains(request.Arguments, "#{socket_path}") {
				return tmux.CommandResult{
					Command:  request.Arguments,
					Stdout:   []string{targetPath},
					ExitCode: 0,
				}, nil
			}
			return tmux.CommandResult{
				Command:  request.Arguments,
				Stderr:   []string{"no server running on sibling"},
				ExitCode: 1,
			}, nil
		}),
	})

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance := mustMCPServer(t, target)
	serverSession, err := instance.Connect(ctx, assumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "frozen-binding"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	var listed struct {
		Servers []struct {
			SocketPath string `json:"socketPath"`
			IsTarget   bool   `json:"isTarget"`
		} `json:"servers"`
		Total      int    `json:"total"`
		SearchedIn string `json:"searchedIn"`
	}
	result := call(
		ctx,
		t,
		session,
		"list_servers",
		map[string]any{"includeDead": true},
		&listed,
	)
	if result.IsError {
		t.Fatalf("list_servers failed: %#v", result.Content)
	}
	if listed.SearchedIn != directory || listed.Total != 2 {
		t.Fatalf(
			"discovery = (%q, total %d), want frozen directory %q with sibling and explicit target",
			listed.SearchedIn,
			listed.Total,
			directory,
		)
	}
	foundTarget := false
	for _, server := range listed.Servers {
		if server.SocketPath == ambient {
			t.Fatalf("list_servers scanned ambient socket %q", ambient)
		}
		if server.SocketPath == targetPath && server.IsTarget {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatalf("servers = %#v, want explicit target outside scan directory", listed.Servers)
	}

	mutex.Lock()
	defer mutex.Unlock()
	wantSelector := "-S" + sibling
	foundSibling := false
	for _, request := range requests {
		if slices.Contains(request.Arguments, "-S"+ambient) {
			t.Fatalf("requests probed ambient socket: %#v", requests)
		}
		if slices.Contains(request.Arguments, "-S"+notASocket) {
			t.Fatalf("requests probed a non-socket file: %#v", requests)
		}
		if slices.Contains(request.Arguments, wantSelector) {
			if request.Binary != executable ||
				!slices.Equal(request.Environment, frozenEnvironment) {
				t.Fatalf("sibling probe binding = %#v, want frozen target binding", request)
			}
			foundSibling = true
		}
	}
	if !foundSibling {
		t.Fatalf("requests = %#v, want sibling probe through frozen binding", requests)
	}
}

//libtmux:real-tmux
func TestListServersUsesTheBoundLaneForItsTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	base := tmuxtest.NewServer(ctx, t)
	delegate := tmux.SubprocessRunner()
	var blockProcess atomic.Bool
	target := mustTmuxServer(t, tmux.ServerOptions{
		Binary:             base.Executable(),
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: base.ProcessEnvironment(),
		Runner: tmux.CommandRunnerFunc(func(
			ctx context.Context,
			request tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			if blockProcess.Load() && !slices.Equal(request.Arguments, []string{"-V"}) {
				return tmux.CommandResult{}, fmt.Errorf(
					"%w: blocked process lane: %v", tmux.ErrControlClosed, request.Arguments,
				)
			}
			return delegate.Run(ctx, request)
		}),
	})
	session, sessionCtx := connectTarget(t, target)
	var serverInfo struct{}
	call(sessionCtx, t, session, "get_server_info", map[string]any{}, &serverInfo)
	blockProcess.Store(true)

	callCtx, endCall := context.WithTimeout(sessionCtx, time.Second)
	defer endCall()
	result, err := session.CallTool(callCtx, &sdk.CallToolParams{
		Name: "list_servers", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("list_servers depended on the blocked process lane: %v", err)
	}
	if result.IsError {
		t.Fatalf("list_servers failed: %s", resultText(result))
	}
}

func TestListServersProbesSiblingSocketsConcurrently(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	siblings := make([]string, 8)
	for index := range siblings {
		siblings[index] = filepath.Join(directory, fmt.Sprintf("sibling-%d", index))
		leaveDeadUnixSocket(t, siblings[index])
	}
	targetPath := filepath.Join(t.TempDir(), "target")
	started := make(chan struct{}, len(siblings))
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseProbes := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProbes()
	target := mustTmuxServer(t, tmux.ServerOptions{
		Binary:             mustExecutable(t),
		SocketPath:         targetPath,
		ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
		Runner: tmux.CommandRunnerFunc(func(
			ctx context.Context,
			request tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			if slices.Equal(request.Arguments, []string{"-V"}) {
				return tmux.CommandResult{Stdout: []string{"tmux 3.6"}}, nil
			}
			for _, sibling := range siblings {
				if !slices.Contains(request.Arguments, "-S"+sibling) {
					continue
				}
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return tmux.CommandResult{}, ctx.Err()
				}
				break
			}
			return tmux.CommandResult{
				Command:  request.Arguments,
				Stderr:   []string{"no server running on " + targetPath},
				ExitCode: 1,
			}, nil
		}),
	})
	selection, err := target.SocketSelection()
	if err != nil {
		t.Fatal(err)
	}
	if selection.NamedDirectory != directory {
		t.Fatalf("named socket directory = %q, want %q", selection.NamedDirectory, directory)
	}
	if info, err := os.Stat(siblings[0]); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("sibling is not a socket: (%v, %v)", info, err)
	}
	session, sessionCtx := connectTarget(t, target)
	type callResult struct {
		result *sdk.CallToolResult
		err    error
	}
	returned := make(chan callResult, 1)
	go func() {
		result, err := session.CallTool(sessionCtx, &sdk.CallToolParams{
			Name: "list_servers", Arguments: map[string]any{"includeDead": true},
		})
		returned <- callResult{result: result, err: err}
	}()

	for count := 0; count < 2; count++ {
		select {
		case <-started:
		case got := <-returned:
			if got.err != nil {
				t.Fatalf("list_servers returned before probing siblings: %v", got.err)
			}
			t.Fatalf("list_servers returned before probing siblings: %s", resultText(got.result))
		case <-time.After(time.Second):
			t.Fatal("sibling socket probes ran serially")
		}
	}
	releaseProbes()
	got := <-returned
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.result.IsError {
		t.Fatalf("list_servers failed: %s", resultText(got.result))
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return executable
}

func leaveDeadUnixSocket(t *testing.T, path string) {
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

func connectTarget(
	t *testing.T,
	target tmux.Server,
) (*sdk.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance := mustMCPServer(t, target)
	t.Cleanup(func() { _ = instance.Close() })
	serverSession, err := instance.Connect(ctx, assumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "list-servers-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, ctx
}
