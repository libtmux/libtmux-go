package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The manifest tests hold the surface a client is offered: names, toolsets,
// annotations, and the schemas each tool publishes. None of them calls a tool
// and reads what comes back, so a handler could answer with a shape its own
// output schema rejects and every gate would stay green. This sweep closes
// that: it calls all 45 advertised tools against a real tmux server and holds
// each reply to the schema its tool published.

// surfaceFixture is what the sweep addresses, filled in as it goes.
type surfaceFixture struct {
	session, otherSession string
	window, otherWindow   string
	pane, otherPane       string
}

// surfaceStep is one tool called with arguments the fixture supplies.
type surfaceStep struct {
	tool string
	// arguments are built rather than fixed, because most of them name an id
	// that only exists once the sweep has run.
	arguments func(*surfaceFixture) map[string]any
	// keep reads something out of the reply for a later step.
	keep func(*surfaceFixture, map[string]any)
	// mayFail marks a call whose failure is a legitimate answer here, and whose
	// reply is therefore not held to the output schema.
	mayFail bool
}

//libtmux:real-tmux
func TestEveryToolAnswersTheSchemaItPublishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "sweep"}
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

	held := &surfaceFixture{}
	called := map[string]bool{}
	for _, one := range surfaceSweep() {
		tool, offered := advertised[one.tool]
		if !offered {
			t.Errorf("the sweep calls %q, which is not advertised", one.tool)
			continue
		}
		called[one.tool] = true
		result, err := session.CallTool(ctx, &sdk.CallToolParams{
			Name: one.tool, Arguments: one.arguments(held),
		})
		if err != nil {
			if one.mayFail {
				continue
			}
			t.Fatalf("%s: %v", one.tool, err)
		}
		if result.IsError {
			if one.mayFail {
				continue
			}
			t.Fatalf("%s: %s", one.tool, surfaceResultText(result))
		}
		if one.keep != nil {
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatalf("%s: encode reply: %v", one.tool, err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("%s: decode reply: %v", one.tool, err)
			}
			one.keep(held, decoded)
		}
		if err := answersItsSchema(tool, result); err != nil {
			t.Errorf("%s: %v", one.tool, err)
		}
	}
	for name := range advertised {
		if !called[name] {
			t.Errorf("%s is advertised and the sweep never calls it", name)
		}
	}
}

// A tool answering its own schema does not prove it acted: tmux accepts a
// server-scoped write of a session option without complaint and changes
// nothing, so a setter aimed at the wrong scope can report success and leave
// the option alone. libtmux-go refuses that write before tmux runs, which is
// what makes it loud here; this reads the value back through the surface so a
// setter that stops taking effect fails even if the refusal is ever relaxed.
//
//libtmux:real-tmux
func TestSettersChangeWhatTheyReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	request := tmux.NewSessionRequest{Name: "effect"}
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &request,
	})
	session, closeSession, err := connectExampleClient(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSession)

	sessions, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_sessions"})
	if err != nil || sessions.IsError {
		t.Fatalf("list_sessions = (%v, %v)", surfaceResultText(sessions), err)
	}
	sessionID := firstListedID(t, sessions, "sessions")
	windows, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_windows"})
	if err != nil || windows.IsError {
		t.Fatalf("list_windows = (%v, %v)", surfaceResultText(windows), err)
	}
	windowID := firstListedID(t, windows, "windows")

	for _, effect := range []struct {
		name      string
		set       string
		arguments map[string]any
		option    string
		scope     string
		target    string
		want      string
	}{
		{
			name: "mouse on", set: "set_mouse_enabled",
			arguments: map[string]any{"enabled": true},
			option:    "mouse", scope: "session", target: sessionID, want: "on",
		},
		{
			name: "mouse off", set: "set_mouse_enabled",
			arguments: map[string]any{"enabled": false},
			option:    "mouse", scope: "session", target: sessionID, want: "off",
		},
		{
			name: "history limit", set: "set_history_limit",
			arguments: map[string]any{"session_id": sessionID, "lines": 4321},
			option:    "history-limit", scope: "session", target: sessionID,
			want: "4321",
		},
		{
			name: "synchronize panes", set: "set_synchronize_panes",
			arguments: map[string]any{"window_id": windowID, "enabled": true},
			option:    "synchronize-panes", scope: "window", target: windowID,
			want: "on",
		},
	} {
		t.Run(effect.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: effect.set, Arguments: effect.arguments,
			})
			if err != nil {
				t.Fatalf("%s: %v", effect.set, err)
			}
			if result.IsError {
				t.Fatalf("%s: %s", effect.set, surfaceResultText(result))
			}
			read, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: "show_option",
				Arguments: map[string]any{
					// mouse is written as the global session option, which a
					// session reads by inheritance rather than as its own.
					"name": effect.option, "scope": effect.scope,
					"target": effect.target, "effective": true,
				},
			})
			if err != nil || read.IsError {
				t.Fatalf("show_option %s: (%v, %v)",
					effect.option, surfaceResultText(read), err)
			}
			structured, ok := read.StructuredContent.(map[string]any)
			if !ok {
				t.Fatalf("show_option %s answered no object", effect.option)
			}
			if value, _ := structured["value"].(string); value != effect.want {
				t.Errorf("%s reported success but %s reads %q, want %q",
					effect.set, effect.option, value, effect.want)
			}
		})
	}
}

// answersItsSchema holds one reply to the schema its tool published.
func answersItsSchema(tool *sdk.Tool, result *sdk.CallToolResult) error {
	if tool.OutputSchema == nil {
		return nil
	}
	encoded, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		return err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return err
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return err
	}
	// Through JSON, because that is how a client receives it: a Go value that
	// marshals to something the schema rejects is the failure being looked for.
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(structured, &value); err != nil {
		return err
	}
	if err := resolved.Validate(value); err != nil {
		return fmt.Errorf("reply does not answer its own output schema: %w", err)
	}
	return nil
}

// singleQuoted wraps a value for a shell word. The values here are a temporary
// path and a fixed prompt, neither of which carries a quote.
func singleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func surfaceResultText(result *sdk.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			return text.Text
		}
	}
	return ""
}

// surfaceSweep orders the calls so that every id one step names was created by
// an earlier one, and so that the teardown toolset runs last against objects
// the sweep made rather than the ones it is still using.
func surfaceSweep() []surfaceStep {
	none := func(*surfaceFixture) map[string]any { return map[string]any{} }
	firstID := func(rows any) string {
		list, ok := rows.([]any)
		if !ok || len(list) == 0 {
			return ""
		}
		row, ok := list[0].(map[string]any)
		if !ok {
			return ""
		}
		id, _ := row["id"].(string)
		return id
	}
	return []surfaceStep{
		{tool: "list_sessions", arguments: none, keep: func(f *surfaceFixture, r map[string]any) {
			f.session = firstID(r["sessions"])
		}},
		{tool: "list_windows", arguments: none, keep: func(f *surfaceFixture, r map[string]any) {
			f.window = firstID(r["windows"])
		}},
		{tool: "list_panes", arguments: none, keep: func(f *surfaceFixture, r map[string]any) {
			f.pane = firstID(r["panes"])
		}},
		{tool: "get_server_info", arguments: none},
		{tool: "get_session_info", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"session_id": f.session}
		}},
		{tool: "get_window_info", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.window}
		}},
		{tool: "get_pane_info", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane}
		}},
		{tool: "capture_pane", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane}
		}},
		{tool: "capture_since", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane}
		}},
		{tool: "snapshot_pane", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane}
		}},
		{tool: "search_panes", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"pattern": "tmuxtest"}
		}},
		{tool: "find_pane_by_position", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.window, "position": "top-left"}
		}},
		{tool: "wait_for_text", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{
				"pane_id": f.pane, "patterns": []any{"tmuxtest"}, "timeout": 5,
			}
		}},
		{tool: "get_tmux_variables", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"names": []any{"pid"}}
		}},
		{tool: "show_option", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"name": "history-limit"}
		}},
		{tool: "show_environment", arguments: none},
		{tool: "show_hooks", arguments: none},
		{tool: "call_read_tools_batch", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"operations": []any{
				map[string]any{"tool": "list_panes", "arguments": map[string]any{}},
			}}
		}},

		{tool: "create_session", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"session_name": "swept"}
		}, keep: func(f *surfaceFixture, r map[string]any) {
			f.otherSession, _ = r["sessionId"].(string)
		}},
		{tool: "create_window", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"session_id": f.otherSession, "window_name": "made"}
		}, keep: func(f *surfaceFixture, r map[string]any) {
			f.otherWindow, _ = r["windowId"].(string)
		}},
		{tool: "split_window", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane, "direction": "below"}
		}, keep: func(f *surfaceFixture, r map[string]any) {
			f.otherPane, _ = r["paneId"].(string)
		}},
		{tool: "respawn_pane", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.otherPane}
		}},
		{tool: "run_shell_command", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane, "command": "true", "timeout": 10}
		}},
		{tool: "send_keys", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane, "keys": []any{"Escape"}}
		}},
		{tool: "send_keys_batch", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"operations": []any{
				map[string]any{"pane_id": f.pane, "keys": []any{"Escape"}},
			}}
		}},
		{tool: "paste_text", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane, "text": "swept"}
		}},
		{tool: "set_synchronize_panes", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.window, "enabled": false}
		}},

		{tool: "rename_session", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"session_id": f.session, "new_name": "swept-session"}
		}},
		{tool: "rename_window", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.window, "new_name": "swept-window"}
		}},
		{tool: "select_window", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.window}
		}},
		{tool: "select_pane", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane}
		}},
		{tool: "select_layout", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.window, "layout": "even-vertical"}
		}},
		{tool: "resize_window", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.window, "width": 100, "height": 30}
		}},
		{tool: "resize_pane", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane, "height": 12}
		}},
		{tool: "move_window", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{
				"window_id": f.otherWindow, "session_id": f.otherSession, "index": 9,
			}
		}},
		{tool: "swap_pane", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane, "other_pane_id": f.otherPane}
		}},
		{tool: "set_pane_title", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.pane, "title": "swept"}
		}},
		{tool: "signal_channel", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"channel": "sweep"}
		}},
		{tool: "wait_for_channel", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"channel": "sweep", "timeout": 5}
		}},
		{tool: "set_mouse_enabled", arguments: func(*surfaceFixture) map[string]any {
			return map[string]any{"enabled": false}
		}},
		{tool: "set_history_limit", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"session_id": f.session, "lines": 5000}
		}},

		// Teardown last, and only against what the sweep made itself.
		{tool: "clear_pane_scrollback", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.otherPane}
		}},
		{tool: "kill_pane", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"pane_id": f.otherPane}
		}},
		{tool: "kill_window", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"window_id": f.otherWindow}
		}},
		{tool: "kill_session", arguments: func(f *surfaceFixture) map[string]any {
			return map[string]any{"session_id": f.otherSession}
		}},
	}
}
