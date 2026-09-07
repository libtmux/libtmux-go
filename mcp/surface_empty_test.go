package mcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every collection these tools publish is required and typed as an array, so a
// nil slice reaching the encoder answers null and breaks the tool's own schema.
// The sweep in surface_schema_test.go calls each tool once with something to
// report, which is the path where the slice is never nil. These are the same
// tools asked a question whose honest answer is nothing.
//
//libtmux:real-tmux
func TestEmptyCollectionsAnswerTheirSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "empty"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	advertised := map[string]*sdk.Tool{}
	for _, tool := range listed.Tools {
		advertised[tool.Name] = tool
	}

	panes, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_panes"})
	if err != nil || panes.IsError {
		t.Fatalf("list_panes = (%v, %v)", surfaceResultText(panes), err)
	}
	paneID := firstListedID(t, panes, "panes")

	for _, empty := range []struct {
		tool      string
		arguments map[string]any
	}{
		// A pattern no pane holds, so panes comes back with no rows.
		{"search_panes", map[string]any{"pattern": "no-pane-holds-this-text"}},
		// A hook name nothing is bound to.
		{"show_hooks", map[string]any{"name": "client-detached"}},
		// A variable name tmux does not define.
		{"get_tmux_variables", map[string]any{"names": []any{"not_a_tmux_variable"}}},
		// A wait that ends without matching, so it reports no lines.
		{"wait_for_text", map[string]any{
			"pane_id": paneID, "patterns": []any{"never-written-to-this-pane"},
			"timeout": 1,
		}},
		// A command writing nothing at all.
		{"run_shell_command", map[string]any{
			"pane_id": paneID, "command": "true", "timeout": 10,
		}},
	} {
		t.Run(empty.tool, func(t *testing.T) {
			tool, offered := advertised[empty.tool]
			if !offered {
				t.Fatalf("%s is not advertised", empty.tool)
			}
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: empty.tool, Arguments: empty.arguments,
			})
			if err != nil {
				t.Fatalf("%s: %v", empty.tool, err)
			}
			if result.IsError {
				t.Fatalf("%s: %s", empty.tool, surfaceResultText(result))
			}
			if err := answersItsSchema(tool, result); err != nil {
				t.Errorf("%s with nothing to report: %v", empty.tool, err)
			}
		})
	}
}

func firstListedID(t *testing.T, result *sdk.CallToolResult, collection string) string {
	t.Helper()
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s reply is not an object", collection)
	}
	rows, ok := structured[collection].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("%s reported no rows", collection)
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("%s row is not an object", collection)
	}
	id, _ := row["id"].(string)
	if id == "" {
		t.Fatalf("%s row has no id", collection)
	}
	return id
}
