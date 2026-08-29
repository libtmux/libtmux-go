package mcp_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
			target := mustTmuxServer(t, executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
				SocketName:         testCase.socketName,
				ProcessEnvironment: []string{"TMUX_TMPDIR=" + root},
			}))
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

func TestListServersUsesTargetsFrozenSocketDirectory(t *testing.T) {
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

	options := executableFixtureOptions(t, fixtureNoServer, tmux.ServerOptions{
		SocketPath:         targetPath,
		ProcessEnvironment: frozenEnvironment,
	})
	if options.Binary != executable {
		t.Fatalf("fixture executable = %q, want %q", options.Binary, executable)
	}
	target := mustTmuxServer(t, options)

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
}

//libtmux:real-tmux
func TestListServersUsesTheBoundLaneForItsTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	base := tmuxtest.NewServer(ctx, t)
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.Symlink(base.Executable(), proxy); err != nil {
		t.Fatal(err)
	}
	target := mustTmuxServer(t, tmux.ServerOptions{
		Binary:             proxy,
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: base.ProcessEnvironment(),
	})
	session, sessionCtx := connectTarget(t, target)
	var serverInfo struct{}
	call(sessionCtx, t, session, "get_server_info", map[string]any{}, &serverInfo)
	if err := os.Remove(proxy); err != nil {
		t.Fatal(err)
	}

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
