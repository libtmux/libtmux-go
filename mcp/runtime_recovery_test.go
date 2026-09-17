package mcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPSurvivesItsTmuxServerExiting pins GO2-9/D4: an MCP process must stay
// up when its tmux server exits - the last session closing, kill-server, or a
// first create_session that cannot reach a server. The call that hits the
// loss reports it; the process itself keeps serving, and the next
// create_session starts a new server rather than every future tool call
// reliving the same stale failure forever.
//
//libtmux:real-tmux
func TestMCPSurvivesItsTmuxServerExiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	var created struct {
		SessionID string `json:"sessionId"`
	}
	call(ctx, t, session, "create_session", map[string]any{"session_name": "first"}, &created)
	if created.SessionID == "" {
		t.Fatal("create_session did not return a session id")
	}

	// A raw kill-server on the isolated socket, outside the runtime's own
	// control connection - simulates the daemon dying under the MCP process.
	if err := target.Kill(ctx); err != nil {
		t.Fatalf("raw kill-server: %v", err)
	}
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		alive, aliveErr := target.IsAlive(ctx)
		return !alive, aliveErr
	}); err != nil {
		t.Fatalf("wait for the killed server to go: %v", err)
	}

	// The next tool call must report the loss, not hang and not take the
	// whole process down with it.
	listed, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "list_sessions", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("list_sessions after kill-server: transport error %v, want a tool isError result", err)
	}
	if !listed.IsError {
		t.Fatalf("list_sessions after kill-server: IsError = false, want true (the daemon is gone)")
	}

	// The MCP process is still up: create_session starts a new server.
	var recreated struct {
		SessionID string `json:"sessionId"`
	}
	call(ctx, t, session, "create_session", map[string]any{"session_name": "second"}, &recreated)
	if recreated.SessionID == "" {
		t.Fatal("create_session after the daemon loss did not return a session id")
	}

	var relisted struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	call(ctx, t, session, "list_sessions", nil, &relisted)
	if len(relisted.Sessions) != 1 {
		t.Fatalf("list_sessions after recovery = %d sessions, want 1", len(relisted.Sessions))
	}
}
