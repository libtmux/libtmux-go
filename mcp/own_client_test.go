package mcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP server reads tmux through a control client of its own, and tmux
// counts it as attached; a detached session read as watched by someone.
//
//libtmux:real-tmux
func TestListingsLeaveOutTheServersOwnClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "detached"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	counts := func() (attached, sessionAttached, clients int) {
		t.Helper()
		var listed struct {
			Sessions []struct {
				ID       string `json:"id"`
				Attached int    `json:"attached"`
			} `json:"sessions"`
		}
		call(ctx, t, session, "list_sessions", nil, &listed)
		if len(listed.Sessions) != 1 {
			t.Fatalf("list_sessions returned %d sessions, want 1", len(listed.Sessions))
		}
		var info struct {
			Session struct {
				Attached int `json:"attached"`
			} `json:"session"`
		}
		call(ctx, t, session, "get_session_info", map[string]any{"session_id": listed.Sessions[0].ID}, &info)
		var server struct {
			Clients int `json:"clients"`
		}
		call(ctx, t, session, "get_server_info", nil, &server)
		return listed.Sessions[0].Attached, info.Session.Attached, server.Clients
	}

	if attached, sessionAttached, clients := counts(); attached != 0 || sessionAttached != 0 || clients != 0 {
		t.Fatalf("detached session: attached=%d session_info=%d clients=%d, want 0 0 0",
			attached, sessionAttached, clients)
	}

	sessions, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := sessions.Sessions()[0].OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()
	if attached, sessionAttached, clients := counts(); attached != 1 || sessionAttached != 1 || clients != 1 {
		t.Fatalf("with another client: attached=%d session_info=%d clients=%d, want 1 1 1",
			attached, sessionAttached, clients)
	}
}

// GO2-10: get_server_info must report the configured socket path even before
// any tmux daemon has ever started there - list_sessions's own note points
// agents at get_server_info to find it, so it must be true with no server
// alive as well as with one.
//
//libtmux:real-tmux
func TestGetServerInfoReportsSocketPathBeforeAnyServerStarts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	var info struct {
		Alive      bool   `json:"alive"`
		SocketPath string `json:"socketPath"`
	}
	call(ctx, t, session, "get_server_info", nil, &info)
	if info.Alive {
		t.Fatal("target was already alive; test setup did not reproduce a never-started server")
	}
	if info.SocketPath == "" {
		t.Fatal("get_server_info socketPath = \"\" with no live server, want the configured socket path")
	}
}

func call(ctx context.Context, t *testing.T, session *sdk.ClientSession, name string, arguments map[string]any, into any) {
	t.Helper()
	if arguments == nil {
		arguments = map[string]any{}
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := decodeStructured(result, into); err != nil {
		t.Fatalf("%s: %v (%s)", name, err, surfaceResultText(result))
	}
}
