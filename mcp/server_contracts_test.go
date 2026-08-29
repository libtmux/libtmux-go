package mcp_test

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerAdvertisesItsTools(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	advertised := map[string]bool{}
	for _, tool := range tools.Tools {
		advertised[tool.Name] = true
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
	}
	for _, want := range []string{
		"list_panes", "capture_pane", "send_keys", "build_workspace", "kill_session",
	} {
		if !advertised[want] {
			t.Errorf("tool %q is not advertised", want)
		}
	}
}

//libtmux:real-tmux
func TestToolFailuresReachTheClientAsContent(t *testing.T) {
	// kill_session is offered only at the destructive level.
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	for name, arguments := range map[string]map[string]any{
		"capture_pane":    {"paneId": "%99999"},
		"send_keys":       {"paneId": "%99999", "command": "true"},
		"kill_session":    {"sessionName": "no-such-session"},
		"build_workspace": {"document": "session_nme: typo\n"},
	} {
		result := call(ctx, t, session, name, arguments, nil)
		if !result.IsError {
			t.Errorf("%s: expected a tool error", name)
			continue
		}
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok || strings.TrimSpace(text.Text) == "" {
			t.Errorf("%s: error content = %#v, want nonempty text", name, result.Content)
			continue
		}
		// A failure must not also ship a result. The SDK serializes a handler's
		// output whether or not the call failed, so a zero value would reach a
		// client as a successful-looking empty answer next to the error.
		if result.StructuredContent != nil {
			t.Errorf("%s: failure carried structured content %v",
				name, result.StructuredContent)
		}
		t.Logf("%s -> %s", name, text.Text)
	}
}

//libtmux:real-tmux
func TestEveryToolCarriesAnnotations(t *testing.T) {
	session, _, ctx := connect(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("the server advertises no tools")
	}

	readers := map[string]bool{
		"list_panes": true, "list_windows": true, "list_sessions": true,
		"list_servers": true, "snapshot_pane": true, "search_panes": true,
		"capture_pane": true, "capture_since": true,
		"find_pane_by_position": true, "wait_for_text": true, "call_readonly_tools_batch": true,
		"get_pane_info": true, "get_window_info": true, "get_session_info": true,
		"get_server_info": true, "show_buffer": true,
		"show_option": true, "show_environment": true, "show_hooks": true,
		"get_job": true,
	}
	enders := map[string]bool{
		"kill_session": true, "kill_window": true, "kill_pane": true,
		"kill_server": true, "call_destructive_tools_batch": true,
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil {
			t.Errorf("%s carries no annotations", tool.Name)
			continue
		}
		if tool.Annotations.Title == "" {
			t.Errorf("%s carries no title", tool.Name)
		}
		if capability, ok := tool.Meta[tmuxmcp.CapabilityMetaKey].(string); !ok || capability == "" {
			t.Errorf("%s carries no capability metadata", tool.Name)
		}
		if got := tool.Annotations.ReadOnlyHint; got != readers[tool.Name] {
			t.Errorf("%s readOnlyHint = %v, want %v", tool.Name, got, readers[tool.Name])
		}
		destructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		if want := enders[tool.Name]; destructive != want {
			t.Errorf("%s destructiveHint = %v, want %v", tool.Name, destructive, want)
		}
	}
}

//libtmux:real-tmux
func TestNoNotificationsBeforeTheClientIsInitialized(t *testing.T) {
	target := mustTmuxServer(t, tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = target.Kill(killCtx)
	})

	client, server := sdk.NewInMemoryTransports()
	ordered := handshakeOrderingFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	serverSession, err := mustMCPServer(t, target).Connect(
		ctx, assumeResponseCommit(ordered), nil,
	)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	var early int
	clientImpl := sdk.NewClient(&sdk.Implementation{Name: "ordering"}, &sdk.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdk.ToolListChangedRequest) {
			early++
		},
	})
	clientSession, err := clientImpl.Connect(ctx, client, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	// A connect that completes at all is the point: a client enforcing the
	// ordering rule would not have got here.
	if _, err := clientSession.ListTools(ctx, nil); err != nil {
		t.Fatalf("list tools: %v", err)
	}
}

// handshakeOrderingFor exposes the ordering wrapper to this test.
func handshakeOrderingFor(inner sdk.Transport) sdk.Transport {
	return tmuxmcp.HandshakeOrdered(inner)
}

//libtmux:real-tmux
func TestToolDescriptionsCarryNoSchemaSyntax(t *testing.T) {
	session, _, ctx := connect(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range listed.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		for _, leak := range []string{"minLength=", "minItems=", "maxLength="} {
			if strings.Contains(string(schema), leak) {
				t.Errorf("%s ships %q as description text", tool.Name, leak)
			}
		}
	}
}

//libtmux:real-tmux
func TestEveryChangingToolSaysWhetherRepeatingItCompounds(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Changing tmux to a state: doing it twice leaves the same state.
	settles := map[string]bool{
		"clear_pane": true, "delete_buffer": true, "exit_copy_mode": true,
		"move_window": true, "rename_session": true, "rename_window": true,
		"select_layout": true, "select_pane": true, "select_window": true,
		"set_environment": true, "set_option": true, "set_pane_title": true,
	}
	// Changing tmux by a step, or by one that reverses: splitting twice makes
	// two panes, swapping twice puts them back, zooming twice unzooms.
	compounds := map[string]bool{
		"build_workspace": true, "call_destructive_tools_batch": true,
		"call_mutating_tools_batch": true, "create_session": true,
		"create_window": true, "display_message": true, "enter_copy_mode": true,
		"kill_pane":   true,
		"kill_server": true, "kill_session": true, "kill_window": true,
		"load_buffer": true, "move_pane": true, "paste_buffer": true,
		"paste_text": true, "pipe_pane": true, "resize_pane": true,
		"resize_window": true, "respawn_pane": true, "run_command": true,
		"send_keys": true, "send_keys_batch": true, "signal_channel": true,
		"wait_for_channel": true, "split_window": true, "swap_pane": true,
	}

	for _, tool := range listed.Tools {
		annotations := tool.Annotations
		if annotations == nil {
			t.Errorf("%s carries no annotations at all", tool.Name)
			continue
		}
		if annotations.ReadOnlyHint {
			if !annotations.IdempotentHint {
				t.Errorf("%s only reads, so repeating it cannot compound", tool.Name)
			}
			continue
		}
		switch {
		case settles[tool.Name]:
			if !annotations.IdempotentHint {
				t.Errorf("%s sets a state, so it should say repeating it is safe",
					tool.Name)
			}
		case compounds[tool.Name]:
			if annotations.IdempotentHint {
				t.Errorf("%s compounds, so it must not say repeating it is safe",
					tool.Name)
			}
		default:
			t.Errorf("%s is a changing tool in neither list: decide whether "+
				"repeating it compounds and add it to one", tool.Name)
		}
	}
}

// advertisedSchema is a tool's input schema as a client receives it, decoded
// far enough to ask what each argument is called and what it says about itself.
type advertisedSchema struct {
	Properties map[string]struct {
		Description string `json:"description"`
		// Enum is the closed set of values, absent when any value goes.
		Enum []any `json:"enum"`
	} `json:"properties"`
}

// schemaOf decodes one tool's advertised input schema.
func schemaOf(t *testing.T, tool *sdk.Tool) advertisedSchema {
	t.Helper()
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("%s: marshal input schema: %v", tool.Name, err)
	}
	var schema advertisedSchema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("%s: decode input schema: %v", tool.Name, err)
	}
	return schema
}

func TestEveryArgumentSaysWhatItIs(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		for name, property := range schemaOf(t, tool).Properties {
			if strings.TrimSpace(property.Description) == "" {
				t.Errorf("%s: argument %q has no description", tool.Name, name)
			}
		}
	}
}

//libtmux:real-tmux
func TestAValueOutsideAClosedSetIsRefused(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)
	// Two panes, so a side to look toward has something on it, and shells
	// rather than commands, so nothing exits under the assertions.
	workspace(ctx, t, session,
		"session_name: closed-sets\nwindows:\n  - panes:\n      - {}\n      - {}\n")

	pane := firstPane(ctx, t, session)
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string]advertisedSchema{}
	for _, tool := range listed.Tools {
		schemas[tool.Name] = schemaOf(t, tool)
	}
	for _, closed := range []struct {
		tool     string
		argument string
		fixed    map[string]any
	}{
		// No paneId: the tools that take a scope refuse a target the scope
		// does not read, which is a constraint of its own.
		{"show_hooks", "scope", map[string]any{}},
		{"list_panes", "detail", map[string]any{}},
		{"find_pane_by_position", "direction", map[string]any{"paneId": pane}},
	} {
		t.Run(closed.tool+"/"+closed.argument, func(t *testing.T) {
			send := func(value string) *sdk.CallToolResult {
				arguments := map[string]any{closed.argument: value}
				maps.Copy(arguments, closed.fixed)
				result, err := session.CallTool(ctx, &sdk.CallToolParams{
					Name: closed.tool, Arguments: arguments,
				})
				if err != nil {
					return &sdk.CallToolResult{
						IsError: true,
						Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}},
					}
				}
				return result
			}
			if result := send("sideways"); !result.IsError {
				t.Errorf("%s took an unlisted %s", closed.tool, closed.argument)
			} else if said := resultText(result); !strings.Contains(said, "enum") {
				t.Errorf("refused for the wrong reason: %s", said)
			}
			property, carried := schemas[closed.tool].Properties[closed.argument]
			if !carried || len(property.Enum) == 0 {
				t.Fatalf("%s publishes no set for %s", closed.tool, closed.argument)
			}
			for _, value := range property.Enum {
				// Empty means the default and is reached by omitting the
				// argument, which is what a client actually sends.
				if value == nil || value == "" {
					continue
				}
				if result := send(value.(string)); result.IsError {
					t.Errorf("%s refused its own %s %q: %s",
						closed.tool, closed.argument, value, resultText(result))
				}
			}
		})
	}
}

func TestNoToolOffersAnArgumentItIgnores(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "destructive")
	session, _, ctx := connect(t)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ignored := map[string]string{
		"exit_copy_mode": "scrollUp",
	}
	for _, tool := range tools.Tools {
		unwanted, watched := ignored[tool.Name]
		if !watched {
			continue
		}
		if _, offered := schemaOf(t, tool).Properties[unwanted]; offered {
			t.Errorf("%s offers %q, which it does not read", tool.Name, unwanted)
		}
	}
}

//libtmux:real-tmux
func TestServerMessagesAreBoundedLikeEveryOtherReply(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: messages\nwindows:\n  - panes:\n      - {}\n")

	// A log long enough to be worth bounding, made the way a caller makes one:
	// by asking this server questions.
	call(ctx, t, session, "set_option", map[string]any{
		"scope": "server", "name": "message-limit", "value": "1000",
	}, nil)
	for range 40 {
		call(ctx, t, session, "list_panes", map[string]any{}, nil)
	}

	measure := func(arguments map[string]any) (int, int, bool) {
		t.Helper()
		var reply struct {
			Messages            []string `json:"messages"`
			MessagesUnavailable string   `json:"messagesUnavailable"`
			Truncated           bool     `json:"truncated"`
		}
		arguments["includeMessages"] = true
		result := call(ctx, t, session, "get_server_info", arguments, &reply)
		if result.IsError {
			said := ""
			for _, content := range result.Content {
				if text, ok := content.(*sdk.TextContent); ok {
					said += text.Text
				}
			}
			t.Fatalf("get_server_info %v: %s", arguments, said)
		}
		// tmux keeps the message log per client and refuses the command
		// outright before 3.5 when nothing is attached, which the in-memory
		// transport used here never is. The reply has to say so rather than
		// return an empty log, and there is nothing to bound when it does.
		if reply.MessagesUnavailable != "" {
			if len(reply.Messages) != 0 {
				t.Errorf("the log is reported unavailable and carries %d messages",
					len(reply.Messages))
			}
			t.Skipf("this tmux will not report its message log here: %s",
				reply.MessagesUnavailable)
		}
		size := 0
		for _, message := range reply.Messages {
			size += len(message) + 1
		}
		return len(reply.Messages), size, reply.Truncated
	}

	// The number is this test's own claim rather than a copy of the cap: what
	// matters is that a caller who asked for nothing cannot be handed a reply
	// measured in hundreds of kilobytes. Unbounded, this log runs past 180,000.
	const affordable = 32_000
	count, size, truncated := measure(map[string]any{})
	if size > affordable {
		t.Errorf("asking for the message log with no bounds returned %d bytes over "+
			"%d messages; a caller cannot spend that and did not ask to", size, count)
	}
	if truncated && count == 0 {
		t.Error("the reply reports truncation and carries nothing")
	}

	// A caller may ask for less, and is told that it cost something.
	fewer, _, fewerTruncated := measure(map[string]any{"maxLines": 5})
	if fewer > 5 {
		t.Errorf("maxLines 5 returned %d messages", fewer)
	}
	if count > fewer && !fewerTruncated {
		t.Error("a reply cut to five messages does not report the truncation")
	}

	// And may bound the bytes, which is the cap that was missing.
	_, tight, tightTruncated := measure(map[string]any{"maxLines": 1000, "maxBytes": 4000})
	if tight > 4000 {
		t.Errorf("maxBytes 4000 returned %d bytes", tight)
	}
	if size > tight && !tightTruncated {
		t.Error("a reply cut to four thousand bytes does not report the truncation")
	}
}

//libtmux:real-tmux
func TestAnEmptyCollectionIsStillAnArray(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: arrays\nwindows:\n  - panes:\n      - {}\n")

	raw := func(tool string, arguments map[string]any) map[string]any {
		t.Helper()
		result := call(ctx, t, session, tool, arguments, nil)
		if result.IsError {
			t.Fatalf("%s: %#v", tool, result.Content)
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	hooks := raw("show_hooks", map[string]any{"scope": "server"})
	found, present := hooks["hooks"]
	if !present {
		t.Errorf("show_hooks omits the hooks key: %v", hooks)
	} else if _, ok := found.([]any); !ok && found != nil {
		t.Errorf("hooks is %T rather than an array", found)
	}

	info := raw("get_server_info", map[string]any{})
	clients, present := info["attachedClients"]
	if !present {
		t.Errorf("get_server_info omits the attachedClients key: %v", info)
	} else if _, ok := clients.([]any); !ok && clients != nil {
		t.Errorf("attachedClients is %T rather than an array", clients)
	}
}
