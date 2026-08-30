package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func mustTmuxServer(t testing.TB, options tmux.ServerOptions) tmux.Server {
	t.Helper()
	server, err := tmux.NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func mustMCPServer(t testing.TB, target tmux.Server) *tmuxmcp.Instance {
	t.Helper()
	server, err := tmuxmcp.NewServer(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func assumeResponseCommit(transport sdk.Transport) sdk.Transport {
	return tmuxmcp.AssumeResponseCommit(transport)
}

func TestMain(m *testing.M) {
	runExecutableFixture()
	if err := os.Setenv(tmuxmcp.CapabilitiesEnvironmentVariable, "all"); err != nil {
		panic(err)
	}
	os.Exit(tmuxtest.Main(m))
}

// connect starts the MCP server against a tmux server unique to the test and
// returns a connected client session. Both are torn down with the test.
func connect(t *testing.T) (*sdk.ClientSession, tmux.Server, context.Context) {
	t.Helper()
	return connectWith(t, tmuxtest.ServerOptions{})
}

// connectWith is connect against a server the test configures.
//
// FixedShell is the one worth knowing about: it gives every pane /bin/sh and a
// one-character prompt, so a test about where the cursor sits measures the code
// rather than whoever's shell configuration the suite inherited.
func connectWith(
	t *testing.T,
	options tmuxtest.ServerOptions,
) (*sdk.ClientSession, tmux.Server, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	target := tmuxtest.NewServerWithOptions(ctx, t, options)

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	instance := mustMCPServer(t, target)
	t.Cleanup(func() { _ = instance.Close() })
	serverSession, err := instance.Connect(ctx, assumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, target, ctx
}

// resultText is every text part of a reply joined, which is what a failure
// message needs. Printing the content slice prints pointers.
func resultText(result *sdk.CallToolResult) string {
	said := ""
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			said += text.Text
		}
	}
	return said
}

// call invokes one tool and decodes its structured result into value.
func call(
	ctx context.Context,
	t *testing.T,
	session *sdk.ClientSession,
	name string,
	arguments any,
	value any,
) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("%s: CallTool error = %v", name, err)
	}
	if result.IsError {
		return result
	}
	if value != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatalf("%s: marshal structured content: %v", name, err)
		}
		if err := json.Unmarshal(encoded, value); err != nil {
			t.Fatalf("%s: decode structured content: %v", name, err)
		}
	}
	return result
}

// paneIDs lists the panes the server reports, in tmux's order.
func paneIDs(ctx context.Context, t *testing.T, session *sdk.ClientSession) []string {
	t.Helper()
	var listed struct {
		Panes []struct {
			ID string `json:"id"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	ids := make([]string, 0, len(listed.Panes))
	for _, pane := range listed.Panes {
		ids = append(ids, pane.ID)
	}
	return ids
}

// workspace builds a session whose panes outlive the assertions made about
// them. A pane running a command that exits takes its window, its session, and
// then the tmux server with it, and a test asserting what survived would race
// that teardown.
func workspace(ctx context.Context, t *testing.T, session *sdk.ClientSession, document string) {
	t.Helper()
	result := call(ctx, t, session, "build_workspace", map[string]any{"document": document}, nil)
	if result.IsError {
		t.Fatalf("build_workspace: %#v", result.Content)
	}
}

// firstPane reports the pane a freshly built workspace put a shell in.
func firstPane(ctx context.Context, t *testing.T, session *sdk.ClientSession) string {
	t.Helper()
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}
	return panes[0]
}

// run runs a command in a pane and waits for it, so what follows can assume it
// finished rather than sleeping and hoping.
func run(ctx context.Context, t *testing.T, session *sdk.ClientSession, pane, command string) {
	t.Helper()
	result := call(ctx, t, session, "run_command", map[string]any{
		"paneId": pane, "command": command, "timeoutSeconds": 30,
	}, nil)
	if result.IsError {
		t.Fatalf("run %q: %#v", command, result.Content)
	}
}

// send types a command without waiting for it, for the cases that are about
// what happens while it runs.
func send(ctx context.Context, t *testing.T, session *sdk.ClientSession, pane, command string) {
	t.Helper()
	result := call(ctx, t, session, "send_keys", map[string]any{
		"paneId": pane, "command": command,
	}, nil)
	if result.IsError {
		t.Fatalf("send %q: %s", command, resultText(result))
	}
}
