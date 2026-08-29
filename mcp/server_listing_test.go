package mcp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
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
			if err := os.WriteFile(targetPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
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
					return tmux.CommandResult{
						Command:  request.Arguments,
						Stderr:   []string{"no server running"},
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
				t.Fatalf("list_servers failed: %#v", result.Content)
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
	if err := os.WriteFile(sibling, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ambientDirectory := filepath.Join(ambientRoot, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(ambientDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	ambient := filepath.Join(ambientDirectory, "ambient")
	if err := os.WriteFile(ambient, nil, 0o600); err != nil {
		t.Fatal(err)
	}
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
	serverSession, err := instance.Connect(ctx, serverTransport, nil)
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

func TestListServersRetiresAClosedTargetEngine(t *testing.T) {
	for _, closeOnCall := range []int64{1, 2} {
		t.Run(fmt.Sprintf("call-%d", closeOnCall), func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			var subprocessCalls atomic.Int64
			engine := &closingEngine{closeOnCall: closeOnCall}
			target := mustTmuxServer(t, tmux.ServerOptions{
				Binary:             executable,
				SocketName:         "closed",
				ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
				Runner: tmux.CommandRunnerFunc(func(
					_ context.Context,
					request tmux.CommandRequest,
				) (tmux.CommandResult, error) {
					subprocessCalls.Add(1)
					return tmux.CommandResult{
						Command:  request.Arguments,
						Stderr:   []string{"no server running"},
						ExitCode: 1,
					}, nil
				}),
			}).WithEngine(engine)
			session, ctx := connectTarget(t, target)

			first, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name:      "list_servers",
				Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatalf("first list_servers: %v", err)
			}
			if !first.IsError {
				t.Fatalf("first list_servers succeeded through a closed engine: %#v", first.Content)
			}
			if subprocessCalls.Load() != 0 {
				t.Fatalf("closed-engine call used subprocess %d times", subprocessCalls.Load())
			}

			second, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name:      "list_servers",
				Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatalf("second list_servers: %v", err)
			}
			if second.IsError {
				t.Fatalf("second list_servers failed after recovery: %#v", second.Content)
			}
			if subprocessCalls.Load() == 0 {
				t.Fatal("second list_servers did not use the recovered subprocess engine")
			}
		})
	}
}

type closingEngine struct {
	closeOnCall int64
	calls       atomic.Int64
}

func (*closingEngine) Supports(kind tmux.CommandKind) bool {
	return kind == tmux.CommandServer
}

func (engine *closingEngine) Run(
	context.Context,
	tmux.CommandKind,
	tmux.CommandRequest,
) (tmux.CommandResult, error) {
	if engine.calls.Add(1) >= engine.closeOnCall {
		return tmux.CommandResult{ExitCode: -1}, tmux.ErrControlClosed
	}
	return tmux.CommandResult{ExitCode: 0}, nil
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
	serverSession, err := instance.Connect(ctx, serverTransport, nil)
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

var _ tmux.Engine = (*closingEngine)(nil)
