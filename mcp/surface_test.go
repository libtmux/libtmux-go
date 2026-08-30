package mcp_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fixture is what the surface sweep addresses, filled in as it goes.
type fixture struct {
	session, otherSession string
	window, otherWindow   string
	pane, otherPane       string
	buffer, job           string
}

// step is one tool called with arguments the fixture supplies.
type step struct {
	tool string
	// arguments are built rather than fixed, because most of them name an id
	// that only exists once the sweep has run.
	arguments func(*fixture) map[string]any
	// keep reads something out of the reply for a later step.
	keep func(*fixture, map[string]any)
	// mayFail marks a call whose failure is a legitimate answer here, and
	// whose reply is therefore not held to the output schema.
	mayFail bool
}

//libtmux:real-tmux
func TestEveryToolAnswersTheSchemaItPublishes(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	t.Setenv(tmuxmcp.RecipeToolEnvironmentVariable, "1")
	session, _, ctx := connectWith(t, tmuxtest.ServerOptions{FixedShell: true})

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	advertised := map[string]*sdk.Tool{}
	for _, tool := range listed.Tools {
		advertised[tool.Name] = tool
	}

	held := &fixture{}
	called := map[string]bool{}
	for _, one := range surfaceSweep() {
		tool, offered := advertised[one.tool]
		if !offered {
			t.Errorf("the sweep calls %q, which is not advertised", one.tool)
			continue
		}
		called[one.tool] = true
		t.Run(one.tool, func(t *testing.T) {
			result, err := session.CallTool(ctx, &sdk.CallToolParams{
				Name: one.tool, Arguments: one.arguments(held),
			})
			if err != nil {
				if one.mayFail {
					return
				}
				t.Fatalf("%s: %v", one.tool, err)
			}
			if result.IsError {
				if one.mayFail {
					return
				}
				t.Fatalf("%s: %s", one.tool, resultText(result))
			}
			if one.keep != nil {
				var decoded map[string]any
				encoded, _ := json.Marshal(result.StructuredContent)
				_ = json.Unmarshal(encoded, &decoded)
				one.keep(held, decoded)
			}
			if err := answersItsSchema(tool, result); err != nil {
				t.Errorf("%s: %v", one.tool, err)
			}
		})
	}
	for name := range advertised {
		if !called[name] {
			t.Errorf("%s is advertised and the sweep never calls it", name)
		}
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
	// satisfies the schema in memory can still marshal to one that does not.
	body, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	if value == nil {
		return errors.New("the tool declares an output schema and returned no structured content")
	}
	return resolved.Validate(value)
}

// none is the argument set of a tool that needs nothing.
func none(*fixture) map[string]any { return map[string]any{} }

// args builds a fixed argument set.
func args(pairs map[string]any) func(*fixture) map[string]any {
	return func(*fixture) map[string]any { return pairs }
}

// text is one string field out of a reply.
func text(reply map[string]any, field string) string {
	value, _ := reply[field].(string)
	return value
}

// surfaceSweep is every tool, in an order that builds what it addresses.
//
// The list is exhaustive by construction: the test fails on a tool it does not
// name, so a tool added later arrives here rather than going unchecked.
func surfaceSweep() []step {
	return []step{
		// What is there, before anything changes it.
		{tool: "get_server_info", arguments: args(map[string]any{"includeMessages": true})},
		{tool: "list_servers", arguments: args(map[string]any{"includeDead": true})},
		{tool: "list_sessions", arguments: none, keep: func(f *fixture, reply map[string]any) {
			if sessions, ok := reply["sessions"].([]any); ok && len(sessions) > 0 {
				first, _ := sessions[0].(map[string]any)
				f.session = text(first, "name")
			}
		}},

		// A workspace of its own, so the sweep owns what it takes apart.
		{tool: "build_workspace", arguments: args(map[string]any{
			"document": "session_name: swept\nwindows:\n  - window_name: one\n" +
				"    panes:\n      - {}\n      - {}\n",
		})},
		{
			tool: "list_windows", arguments: args(map[string]any{"sessionName": "swept"}),
			keep: func(f *fixture, reply map[string]any) {
				if windows, ok := reply["windows"].([]any); ok && len(windows) > 0 {
					first, _ := windows[0].(map[string]any)
					f.window = text(first, "id")
				}
			},
		},
		{tool: "list_panes", arguments: args(map[string]any{
			"sessionName": "swept", "detail": "full",
		}), keep: func(f *fixture, reply map[string]any) {
			panes, _ := reply["panes"].([]any)
			for index, entry := range panes {
				pane, _ := entry.(map[string]any)
				switch index {
				case 0:
					f.pane = text(pane, "id")
				case 1:
					f.otherPane = text(pane, "id")
				}
			}
		}},

		// Reading one thing at a time.
		{tool: "get_session_info", arguments: args(map[string]any{"sessionName": "swept"})},
		{tool: "get_window_info", arguments: func(f *fixture) map[string]any {
			return map[string]any{"windowId": f.window}
		}},
		{tool: "get_pane_info", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane}
		}},
		{tool: "find_pane_by_position", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "direction": "below"}
		}},
		{tool: "capture_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "maxLines": 20, "styles": true}
		}},
		{tool: "snapshot_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "includeHistory": true}
		}},
		{tool: "capture_since", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane}
		}},
		{tool: "search_panes", arguments: args(map[string]any{
			"text": "swept", "sessionName": "swept", "maxPanes": 5,
		})},
		{tool: "display_message", arguments: args(map[string]any{
			"format": "#{session_name}", "sessionName": "swept",
		})},
		{tool: "get_recipe", arguments: args(map[string]any{"name": "diagnose_pane"})},

		// Settings.
		{tool: "show_option", arguments: args(map[string]any{
			"name": "history-limit", "scope": "server",
		})},
		{tool: "set_option", arguments: args(map[string]any{
			"name": "history-limit", "value": "5000", "scope": "session",
			"sessionName": "swept",
		})},
		{tool: "show_hooks", arguments: args(map[string]any{"scope": "server"})},
		{tool: "set_environment", arguments: args(map[string]any{
			"name": "SWEPT", "value": "1", "sessionName": "swept",
		})},
		{tool: "show_environment", arguments: args(map[string]any{
			"name": "SWEPT", "sessionName": "swept",
		})},

		// Buffers.
		{
			tool: "load_buffer", arguments: args(map[string]any{"text": "swept-buffer"}),
			keep: func(f *fixture, reply map[string]any) { f.buffer = text(reply, "name") },
		},
		{tool: "show_buffer", arguments: func(f *fixture) map[string]any {
			return map[string]any{"name": f.buffer}
		}},
		{tool: "paste_buffer", arguments: func(f *fixture) map[string]any {
			return map[string]any{"name": f.buffer, "paneId": f.otherPane}
		}},
		{tool: "delete_buffer", arguments: func(f *fixture) map[string]any {
			return map[string]any{"name": f.buffer}
		}},

		// Typing, and running.
		{tool: "send_keys", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "command": "C-c"}
		}},
		{tool: "send_keys_batch", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "keys": []string{"C-c"}}
		}},
		{tool: "paste_text", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "text": "# swept", "enter": true}
		}},
		{tool: "run_command", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "command": "echo swept", "timeoutSeconds": 20}
		}},
		{tool: "run_command", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "command": "echo detached", "detach": true}
		}, keep: func(f *fixture, reply map[string]any) { f.job = text(reply, "jobId") }},
		{tool: "get_job", arguments: func(f *fixture) map[string]any {
			return map[string]any{"jobId": f.job, "timeoutSeconds": 20}
		}},

		// Waiting.
		{tool: "signal_channel", arguments: args(map[string]any{"channel": "swept-channel"})},
		{tool: "wait_for_channel", arguments: args(map[string]any{
			"channel": "swept-channel", "timeoutSeconds": 2,
		})},
		{tool: "wait_for_text", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "idleSeconds": 1, "timeoutSeconds": 10}
		}},

		// Modes and plumbing.
		{tool: "enter_copy_mode", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "scrollUp": true}
		}},
		{tool: "exit_copy_mode", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane}
		}},
		{tool: "pipe_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "command": "cat > /dev/null"}
		}},
		{tool: "pipe_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane}
		}},
		{tool: "clear_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "history": true}
		}},

		// Shape.
		{tool: "split_window", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "direction": "right"}
		}},
		{tool: "resize_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "height": 10}
		}},
		{tool: "resize_window", arguments: func(f *fixture) map[string]any {
			return map[string]any{"windowId": f.window, "width": 100, "height": 30}
		}},
		{tool: "select_layout", arguments: func(f *fixture) map[string]any {
			return map[string]any{"windowId": f.window, "layout": "even-horizontal"}
		}},
		{tool: "swap_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "withPaneId": f.otherPane}
		}},
		{tool: "select_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.otherPane}
		}},
		{tool: "set_pane_title", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "title": "swept"}
		}},
		{tool: "create_window", arguments: args(map[string]any{
			"sessionName": "swept", "name": "two", "command": "sleep 300",
		}), keep: func(f *fixture, reply map[string]any) { f.otherWindow = text(reply, "windowId") }},
		{tool: "move_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.otherPane, "toWindowId": f.otherWindow}
		}},
		{tool: "move_window", arguments: func(f *fixture) map[string]any {
			return map[string]any{"windowId": f.otherWindow, "index": 9}
		}},
		{tool: "select_window", arguments: func(f *fixture) map[string]any {
			return map[string]any{"windowId": f.window}
		}},
		{tool: "rename_window", arguments: func(f *fixture) map[string]any {
			return map[string]any{"windowId": f.window, "name": "renamed"}
		}},
		{tool: "respawn_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.pane, "kill": true, "command": "sleep 300"}
		}},

		// Batches, which dispatch the same tools one layer down.
		{tool: "call_readonly_tools_batch", arguments: args(map[string]any{
			"calls": []map[string]any{{"tool": "list_panes", "arguments": map[string]any{}}},
		})},
		{tool: "call_mutating_tools_batch", arguments: func(f *fixture) map[string]any {
			return map[string]any{"calls": []map[string]any{
				{"tool": "set_pane_title", "arguments": map[string]any{
					"paneId": f.pane, "title": "batched",
				}},
			}, "onError": "continue"}
		}},
		{tool: "call_destructive_tools_batch", arguments: args(map[string]any{
			"calls": []map[string]any{{"tool": "list_panes", "arguments": map[string]any{}}},
		})},

		// Made to be ended, then ended, largest last.
		{tool: "create_session", arguments: args(map[string]any{
			"name": "doomed", "command": "sleep 300",
		}), keep: func(f *fixture, reply map[string]any) {
			f.otherSession = text(reply, "sessionName")
		}},
		{tool: "rename_session", arguments: args(map[string]any{
			"sessionName": "doomed", "name": "condemned",
		})},
		{tool: "kill_pane", arguments: func(f *fixture) map[string]any {
			return map[string]any{"paneId": f.otherPane}
		}},
		{tool: "kill_window", arguments: func(f *fixture) map[string]any {
			return map[string]any{"windowId": f.otherWindow}
		}},
		{tool: "kill_session", arguments: args(map[string]any{"sessionName": "condemned"})},
		{tool: "kill_server", arguments: args(map[string]any{"confirm": true})},
	}
}
