package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testToolsetsEnvironment     = "LIBTMUX_TOOLSETS"
	testToolsEnvironment        = "LIBTMUX_TOOLS"
	testExcludeEnvironment      = "LIBTMUX_EXCLUDE_TOOLS"
	testRecipeEnvironment       = "LIBTMUX_MCP_PROMPTS_AS_TOOLS"
	testCapabilitiesEnvironment = "LIBTMUX_MCP_CAPABILITIES"
	testCapabilitiesURI         = "tmux://capabilities"
	testCapabilityMetaKey       = "com.git-pull.libtmux-mcp/capability"
)

var expectedToolsBySet = map[string][]string{
	"inspect": {
		"list_sessions", "list_windows", "list_panes", "get_server_info",
		"get_session_info", "get_window_info", "get_pane_info", "capture_pane",
		"capture_since", "snapshot_pane", "search_panes", "find_pane_by_position",
		"wait_for_text", "get_tmux_variables", "show_option", "show_environment",
		"show_hooks", "call_read_tools_batch",
	},
	"manage": {
		"rename_session", "rename_window", "select_window", "select_pane",
		"select_layout", "resize_window", "resize_pane", "move_window", "swap_pane",
		"set_pane_title", "wait_for_channel", "signal_channel", "set_mouse_enabled",
		"set_history_limit",
	},
	"execute": {
		"create_session", "create_window", "split_window", "respawn_pane",
		"run_shell_command", "send_keys", "send_keys_batch", "paste_text",
		"set_synchronize_panes",
	},
	"teardown": {
		"clear_pane_scrollback", "kill_pane", "kill_window", "kill_session",
	},
}

func TestCapabilityManifestDefinesTheExactPublicSurface(t *testing.T) {
	setCapabilityEnvironment(t, "inspect,manage,execute,teardown", "", "")
	tools, err := AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := toolsForSets("inspect", "manage", "execute", "teardown")
	got := toolNames(tools)
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want exact 45-tool inventory %v", got, want)
	}

	validReach := setOf("none", "configured-process", "pane-input", "pane-command")
	validEffects := setOf("observe", "change", "delete")
	validOutputs := setOf(
		"tmux-metadata", "terminal-content", "process-environment", "configured-command",
	)
	wantCapabilityFields := []string{
		"amplifiesFutureInput", "annotations", "description", "inputLiteralization",
		"inputSchema", "mayExposeSecrets", "mayReturnUntrustedContent", "name",
		"nestedAuthority", "outputClasses", "outputSchema", "processReach", "title",
		"tmuxEffects", "toolset",
	}

	for _, tool := range tools {
		if CapabilityMetaKey != testCapabilityMetaKey {
			t.Fatalf("CapabilityMetaKey = %q, want shared key %q", CapabilityMetaKey, testCapabilityMetaKey)
		}
		capability, ok := tool.Meta[testCapabilityMetaKey].(map[string]any)
		if !ok {
			t.Errorf("%s capability metadata = %#v, want an object", tool.Name, tool.Meta[testCapabilityMetaKey])
			continue
		}
		if got := sortedKeys(capability); !slices.Equal(got, wantCapabilityFields) {
			t.Errorf("%s capability fields = %v, want %v", tool.Name, got, wantCapabilityFields)
		}
		for field, want := range map[string]any{
			"name": tool.Name, "title": tool.Title, "description": tool.Description,
		} {
			if got := capability[field]; got != want {
				t.Errorf("%s %s = %#v, want %#v", tool.Name, field, got, want)
			}
		}
		toolset, _ := capability["toolset"].(string)
		if !slices.Contains(expectedToolsBySet[toolset], tool.Name) {
			t.Errorf("%s toolset = %q, want its exact inventory group", tool.Name, toolset)
		}
		reach, _ := capability["processReach"].(string)
		if !validReach[reach] {
			t.Errorf("%s processReach = %q", tool.Name, reach)
		}
		assertStringSet(t, tool.Name+" effects", capability["tmuxEffects"], validEffects, true)
		assertStringSet(t, tool.Name+" outputClasses", capability["outputClasses"], validOutputs, true)
		for _, field := range []string{"mayExposeSecrets", "mayReturnUntrustedContent"} {
			if _, ok := capability[field].(bool); !ok {
				t.Errorf("%s %s = %#v, want bool", tool.Name, field, capability[field])
			}
		}
		amplifies, ok := capability["amplifiesFutureInput"].(bool)
		if !ok || amplifies != (tool.Name == "set_synchronize_panes") {
			t.Errorf("%s amplifiesFutureInput = %#v", tool.Name, capability["amplifiesFutureInput"])
		}
		for _, field := range []string{"annotations", "inputSchema", "outputSchema"} {
			if capability[field] == nil {
				t.Errorf("%s capability metadata omits %s", tool.Name, field)
			}
		}
		if tool.Name == "set_synchronize_panes" &&
			(!strings.Contains(tool.Description, "window synchronization default") ||
				!strings.Contains(tool.Description, "pane-level overrides")) {
			t.Errorf("set_synchronize_panes description omits configured membership semantics: %q", tool.Description)
		}

		literalized, ok := capability["inputLiteralization"].(map[string]any)
		if !ok {
			t.Errorf("%s inputLiteralization = %#v, want an object", tool.Name, capability["inputLiteralization"])
		}
		if _, published := capability["tmuxFormatControls"]; published {
			t.Errorf("%s publishes retired tmuxFormatControls metadata", tool.Name)
		}
		if _, published := capability["inputSinks"]; published {
			t.Errorf("%s publishes internal inputSinks metadata", tool.Name)
		}
		for input, control := range literalized {
			if control != "double-hash-once" && control != "validated-variable-name" {
				t.Errorf("%s %s input literalization = %#v", tool.Name, input, control)
			}
		}

		if tool.Title == "" || tool.Annotations == nil || tool.Annotations.Title != tool.Title {
			t.Errorf("%s title/annotations = (%q, %#v)", tool.Name, tool.Title, tool.Annotations)
			continue
		}
		destructive := tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
		openWorld := tool.Annotations.OpenWorldHint != nil && *tool.Annotations.OpenWorldHint
		if tool.Annotations.ReadOnlyHint || !destructive || tool.Annotations.IdempotentHint || !openWorld {
			t.Errorf("%s annotations are not the conservative whole-call defaults: %#v", tool.Name, tool.Annotations)
		}
		if !strings.HasPrefix(tool.Description, controlledOpener(toolset, reach, capability)) {
			t.Errorf("%s description does not start with its controlled capability statement: %q", tool.Name, tool.Description)
		}
	}
}

func TestCapabilityManifestPublishesTypedPrunedReadBatchAuthority(t *testing.T) {
	setCapabilityEnvironment(t, "inspect", "", "")
	tools, err := AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch := namedTool(t, tools, "call_read_tools_batch")
	capability := batch.Meta[testCapabilityMetaKey].(map[string]any)
	nested := stringValues(capability["nestedAuthority"])
	if len(nested) != 16 || !slices.Contains(nested, "show_environment") ||
		slices.Contains(nested, "wait_for_text") || slices.Contains(nested, batch.Name) {
		t.Fatalf("nested authority = %v, want exact a21 sixteen-tool set", nested)
	}
	if !strings.HasPrefix(batch.Description, "Read pane output;") {
		t.Fatalf("mixed aggregate opener = %q, want terminal-content precedence", batch.Description)
	}
	if got := aggregateBranches(t, batch); !slices.Equal(got, slices.Sorted(slices.Values(nested))) {
		t.Fatalf("typed aggregate branches = %v, want %v", got, nested)
	}

	setCapabilityEnvironment(t, "inspect", "", "show_environment")
	tools, err = AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch = namedTool(t, tools, "call_read_tools_batch")
	capability = batch.Meta[testCapabilityMetaKey].(map[string]any)
	nested = stringValues(capability["nestedAuthority"])
	if len(nested) != 15 || slices.Contains(nested, "show_environment") {
		t.Fatalf("excluded aggregate authority = %v", nested)
	}
	if got := aggregateBranches(t, batch); len(got) != 15 || slices.Contains(got, "show_environment") {
		t.Fatalf("excluded typed branches = %v", got)
	}
	if got := aggregateMaxItems(t, batch); got != 16 {
		t.Fatalf("excluded aggregate maxItems = %d, want fixed 16", got)
	}

	setCapabilityEnvironment(t, "", "call_read_tools_batch", "")
	tools, err = AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := toolNames(tools); !slices.Equal(got, []string{"call_read_tools_batch"}) {
		t.Fatalf("aggregate-only tools = %v", got)
	}
	batch = namedTool(t, tools, "call_read_tools_batch")
	capability = batch.Meta[testCapabilityMetaKey].(map[string]any)
	nested = stringValues(capability["nestedAuthority"])
	if len(nested) != 16 || aggregateMaxItems(t, batch) != 16 {
		t.Fatalf("aggregate-only authority = %v, maxItems=%d", nested, aggregateMaxItems(t, batch))
	}

	setCapabilityEnvironment(t, "", "call_read_tools_batch", strings.Join(nested, ","))
	tools, err = AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch = namedTool(t, tools, "call_read_tools_batch")
	capability = batch.Meta[testCapabilityMetaKey].(map[string]any)
	if got := stringValues(capability["nestedAuthority"]); len(got) != 0 {
		t.Fatalf("zero-authority nested tools = %v, want empty", got)
	}
	if got := stringValues(capability["tmuxEffects"]); !slices.Equal(got, []string{"observe"}) {
		t.Fatalf("zero-authority effects = %v, want CM2 observe fallback", got)
	}
	if got := stringValues(capability["outputClasses"]); len(got) != 0 {
		t.Fatalf("zero-authority outputs = %v, want empty union", got)
	}
	if !aggregateItemsUnsatisfiable(t, batch) {
		encoded, err := json.Marshal(batch.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("zero-authority batch schema still accepts an operation: %s", encoded)
	}

	if got := literalizeTmuxFormat("#one##two"); got != "##one####two" {
		t.Fatalf("literalizeTmuxFormat() = %q, want exactly one doubling", got)
	}
}

func TestCapabilityManifestPublishesExactEffectsAndPrunedBatchUnions(t *testing.T) {
	setCapabilityEnvironment(t, "inspect,manage,execute,teardown", "", "")
	tools, err := AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantEffects := map[string][]string{
		"capture_since":         {"observe"},
		"create_session":        {"observe", "change"},
		"kill_pane":             {"observe", "delete"},
		"respawn_pane":          {"observe", "change", "delete"},
		"run_shell_command":     {"observe", "change"},
		"set_history_limit":     {"change"},
		"set_mouse_enabled":     {"change"},
		"set_synchronize_panes": {"change"},
		"wait_for_channel":      {"change"},
	}
	for name, want := range wantEffects {
		capability := namedTool(t, tools, name).Meta[testCapabilityMetaKey].(map[string]any)
		if got := stringValues(capability["tmuxEffects"]); !slices.Equal(got, want) {
			t.Errorf("%s effects = %v, want %v", name, got, want)
		}
	}
	showHooks := namedTool(t, tools, "show_hooks").Meta[testCapabilityMetaKey].(map[string]any)
	if got := stringValues(showHooks["outputClasses"]); !slices.Equal(got, []string{"configured-command"}) {
		t.Errorf("show_hooks output classes = %v, want configured-command only", got)
	}
	variables := namedTool(t, tools, "get_tmux_variables").Meta[testCapabilityMetaKey].(map[string]any)
	literalized := variables["inputLiteralization"].(map[string]any)
	if literalized["names"] != "validated-variable-name" {
		t.Errorf("get_tmux_variables names control = %#v", literalized["names"])
	}

	setCapabilityEnvironment(t, "", "call_read_tools_batch", "capture_since,show_environment,show_hooks,show_option,get_tmux_variables")
	tools, err = AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch := namedTool(t, tools, "call_read_tools_batch")
	capability := batch.Meta[testCapabilityMetaKey].(map[string]any)
	if got := stringValues(capability["tmuxEffects"]); !slices.Equal(got, []string{"observe"}) {
		t.Errorf("pruned batch effects = %v, want observe", got)
	}
	if got := stringValues(capability["outputClasses"]); !slices.Equal(got, []string{"tmux-metadata", "terminal-content"}) {
		t.Errorf("pruned batch outputs = %v, want metadata plus terminal content", got)
	}
}

func TestReadBatchPreservesTheFullNestedMCPEnvelope(t *testing.T) {
	inputSchema, err := jsonschema.For[emptyToolInput](nil)
	if err != nil {
		t.Fatal(err)
	}
	inputResolved, err := inputSchema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	outputSchema, err := jsonschema.For[map[string]string](nil)
	if err != nil {
		t.Fatal(err)
	}
	outputResolved, err := outputSchema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}

	envelope, err := batched(
		t.Context(), nil,
		func(context.Context, *sdk.CallToolRequest, emptyToolInput) (*sdk.CallToolResult, map[string]string, error) {
			return &sdk.CallToolResult{
				Meta:    sdk.Meta{"nested": "kept"},
				Content: []sdk.Content{&sdk.TextContent{Text: "visible nested content"}},
			}, map[string]string{"value": "structured"}, nil
		},
		nil,
		inputResolved,
		outputResolved,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	meta, _ := decoded["_meta"].(map[string]any)
	structured, _ := decoded["structuredContent"].(map[string]any)
	content, _ := decoded["content"].([]any)
	if meta["nested"] != "kept" || structured["value"] != "structured" || len(content) != 1 {
		t.Fatalf("nested envelope = %#v, want metadata, content, and structured content", decoded)
	}

	failed, err := batched(
		t.Context(), nil,
		func(context.Context, *sdk.CallToolRequest, emptyToolInput) (*sdk.CallToolResult, map[string]string, error) {
			return nil, nil, errors.New("nested refusal")
		},
		nil,
		inputResolved,
		outputResolved,
	)
	if err != nil || !failed.IsError || !strings.Contains(batchErrorText(failed), "nested refusal") {
		t.Fatalf("nested failure envelope = (%#v, %v), want visible MCP error result", failed, err)
	}
}

func TestCapabilityManifestReadBatchUsesNestedSchemaValidation(t *testing.T) {
	setCapabilityEnvironment(t, "", "call_read_tools_batch", "")
	surface, err := resolveToolSurface(socketProfile{})
	if err != nil {
		t.Fatal(err)
	}
	registry := newToolRegistry(surface)
	server := sdk.NewServer(&sdk.Implementation{Name: "nested-validation", Version: "1"}, nil)
	if err := registerToolManifest(server, registry); err != nil {
		t.Fatal(err)
	}
	if len(registry.dispatchers) != 16 {
		t.Fatalf("aggregate-only dispatchers = %d, want exact nested authority", len(registry.dispatchers))
	}
	for name, arguments := range map[string]map[string]any{
		"missing required name": {},
		"wrong name type":       {"name": 7},
		"unknown key":           {"name": "history-limit", "surprise": true},
		"invalid enum":          {"name": "history-limit", "scope": "somewhere"},
	} {
		t.Run(name, func(t *testing.T) {
			_, output, err := registry.runReadBatch(t.Context(), nil, batchInput{Calls: []batchCall{{
				Tool: "show_option", Arguments: arguments,
			}}})
			if err != nil || output.Failed != 1 || output.Succeeded != 0 || len(output.Results) != 1 ||
				output.Results[0].Success || output.Results[0].Error == nil ||
				output.Results[0].Index != 0 || output.StoppedAt == nil || *output.StoppedAt != 0 ||
				!strings.Contains(nestedEnvelopeError(output.Results[0].Result), "arguments are not what this tool takes") {
				t.Fatalf("runReadBatch() = (%#v, %v), want canonical nested-schema refusal row", output, err)
			}
		})
	}
	_, continued, err := registry.runReadBatch(t.Context(), nil, batchInput{
		OnError: onErrorContinue,
		Calls: []batchCall{
			{Tool: "show_option", Arguments: map[string]any{}},
			{Tool: "show_option", Arguments: map[string]any{}},
		},
	})
	if err != nil || continued.OnError != onErrorContinue || continued.Failed != 2 ||
		continued.StoppedAt != nil || len(continued.Results) != 2 ||
		continued.Results[0].Index != 0 || continued.Results[1].Index != 1 {
		t.Fatalf("continuing batch = (%#v, %v), want two indexed failure envelopes", continued, err)
	}

	registry.dispatchers["get_server_info"] = dispatcher{call: func(
		context.Context, *sdk.CallToolRequest, json.RawMessage,
	) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Meta:              sdk.Meta{"nested": "kept"},
			Content:           []sdk.Content{&sdk.TextContent{Text: "visible nested content"}},
			StructuredContent: map[string]any{"value": "structured"},
		}, nil
	}}
	_, retained, err := registry.runReadBatch(t.Context(), nil, batchInput{Calls: []batchCall{{
		Tool: "get_server_info",
	}}})
	if err != nil || len(retained.Results) != 1 {
		t.Fatalf("retained nested envelope = (%#v, %v)", retained, err)
	}
	retainedEnvelope, _ := retained.Results[0].Result.(map[string]any)
	retainedMeta, _ := retainedEnvelope["_meta"].(map[string]any)
	retainedStructured, _ := retainedEnvelope["structuredContent"].(map[string]any)
	if retained.Results[0].ResultTruncated ||
		retainedMeta["nested"] != "kept" || retainedStructured["value"] != "structured" {
		t.Fatalf("retained nested envelope = (%#v, %v)", retained, err)
	}

	registry.dispatchers["get_server_info"] = dispatcher{call: func(
		context.Context, *sdk.CallToolRequest, json.RawMessage,
	) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", 1_048_576)}},
			StructuredContent: map[string]any{"value": strings.Repeat("x", 1_048_576)},
		}, nil
	}}
	result, output, err := registry.runReadBatch(t.Context(), nil, batchInput{Calls: []batchCall{{
		Tool: "get_server_info",
	}}})
	if err != nil || !output.Truncated || output.Succeeded != 1 || output.Failed != 0 ||
		len(output.Results) != 1 || !output.Results[0].Success ||
		!output.Results[0].ResultTruncated || output.Results[0].Result != nil ||
		output.TruncatedBytes <= 0 || output.OnError != onErrorStop {
		t.Fatalf("oversized batch = (%#v, %v), want retained compact success row", output, err)
	}
	overhead, err := defaultReadBatchResponseOverhead()
	if err != nil {
		t.Fatal(err)
	}
	wireBytes, err := readBatchWireBytes(overhead, output)
	if err != nil || wireBytes > readBatchWireMaxBytes {
		t.Fatalf("complete JSON-RPC response = %d bytes, cap %d, error %v",
			wireBytes, readBatchWireMaxBytes, err)
	}
	if got := result.Content[0].(*sdk.TextContent).Text; got != readBatchSummary(output) {
		t.Fatalf("batch summary = %q, want %q", got, readBatchSummary(output))
	}
}

func TestReadBatchBoundsTheCompleteJSONRPCResponse(t *testing.T) {
	setCapabilityEnvironment(t, "", "call_read_tools_batch", "")
	surface, err := resolveToolSurface(socketProfile{})
	if err != nil {
		t.Fatal(err)
	}
	registry := newToolRegistry(surface)
	server := sdk.NewServer(&sdk.Implementation{Name: "wire-bound", Version: "1"}, nil)
	if err := registerToolManifest(server, registry); err != nil {
		t.Fatal(err)
	}

	payload := strings.Repeat("x", 400_000)
	registry.dispatchers["get_server_info"] = dispatcher{call: func(
		context.Context, *sdk.CallToolRequest, json.RawMessage,
	) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: payload}},
			StructuredContent: map[string]any{"value": payload},
		}, nil
	}}
	requestID := strings.Repeat("request-", 30_000)
	id, err := jsonrpc.MakeID(requestID)
	if err != nil {
		t.Fatal(err)
	}
	wireRequest := &jsonrpc.Request{ID: id, Method: "tools/call"}
	if err := attachResponseOverhead(wireRequest); err != nil {
		t.Fatal(err)
	}
	request := &sdk.CallToolRequest{Extra: wireRequest.Extra.(*sdk.RequestExtra)}
	result, output, err := registry.runReadBatch(t.Context(), request, batchInput{Calls: []batchCall{
		{Tool: "get_server_info"},
		{Tool: "get_server_info"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encodedOutput, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	result.StructuredContent = json.RawMessage(encodedOutput)
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: id, Result: encodedResult})
	if err != nil {
		t.Fatal(err)
	}
	if len(wire)+1 > readBatchWireMaxBytes {
		t.Fatalf("complete JSON-RPC response = %d bytes, cap %d", len(wire)+1, readBatchWireMaxBytes)
	}
	overhead, err := readBatchResponseOverhead(request)
	if err != nil {
		t.Fatal(err)
	}
	measured, err := readBatchWireBytes(overhead, output)
	if err != nil || measured != len(wire)+1 {
		t.Fatalf("measured response = %d bytes, actual %d, error %v", measured, len(wire)+1, err)
	}
	if len(output.Results) != 2 || output.Succeeded != 2 {
		t.Fatalf("bounded rows = %#v, want every executed row retained", output.Results)
	}
	for index, row := range output.Results {
		if !row.Success || !row.ResultTruncated || row.Result != nil {
			t.Fatalf("bounded row %d = %#v, want retained truncated success", index, row)
		}
	}
}

func nestedEnvelopeError(raw any) string {
	envelope, _ := raw.(map[string]any)
	content, _ := envelope["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if text, ok := block["text"].(string); ok {
			return text
		}
	}
	return ""
}

func TestEveryUnorderedToolsetCombinationResolvesDeterministically(t *testing.T) {
	sets := []string{"inspect", "manage", "execute", "teardown"}
	for mask := range 16 {
		selected := make([]string, 0, len(sets))
		for index, name := range sets {
			if mask&(1<<index) != 0 {
				selected = append(selected, name)
			}
		}
		t.Run(strings.Join(selected, "+"), func(t *testing.T) {
			setCapabilityEnvironment(t, strings.Join(selected, ","), "", "")
			tools, err := AdvertisedTools(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got, want := toolNames(tools), toolsForSets(selected...); !slices.Equal(got, want) {
				t.Fatalf("tools = %v, want %v", got, want)
			}
		})
	}
}

func TestNamedInclusionsAndExclusionsFreezeOneSurface(t *testing.T) {
	setCapabilityEnvironment(t, "", "capture_pane,kill_pane", "kill_pane")
	tools, err := AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := toolNames(tools), []string{"capture_pane"}; !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want exclusion to win: %v", got, want)
	}

	for _, test := range []struct {
		name, toolsets, included, excluded, want string
	}{
		{"unknown toolset", "inspect,unknown", "", "", testToolsetsEnvironment},
		{"empty toolset token", "inspect,,manage", "", "", testToolsetsEnvironment},
		{"unknown inclusion", "", "not_a_tool", "", testToolsEnvironment},
		{"unknown exclusion", "", "", "not_a_tool", testExcludeEnvironment},
	} {
		t.Run(test.name, func(t *testing.T) {
			setCapabilityEnvironment(t, test.toolsets, test.included, test.excluded)
			_, err := AdvertisedTools(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AdvertisedTools() error = %v, want %s startup failure", err, test.want)
			}
		})
	}
}

func TestRetiredSafetyEnvironmentFailsWithMigration(t *testing.T) {
	setCapabilityEnvironment(t, "inspect", "", "")
	t.Setenv(SafetyEnvironmentVariable, "")
	_, err := AdvertisedTools(t.Context())
	if err == nil || !strings.Contains(err.Error(), testToolsetsEnvironment) {
		t.Fatalf("AdvertisedTools() error = %v, want migration to %s", err, testToolsetsEnvironment)
	}
}

func TestRetiredPromptToolEnvironmentFailsWithMigration(t *testing.T) {
	for _, value := range []string{"", "1"} {
		t.Run(value, func(t *testing.T) {
			setCapabilityEnvironment(t, "inspect", "", "")
			t.Setenv(testRecipeEnvironment, value)
			_, err := AdvertisedTools(t.Context())
			if err == nil || !strings.Contains(err.Error(), testRecipeEnvironment) {
				t.Fatalf("AdvertisedTools() error = %v, want retired %s failure", err, testRecipeEnvironment)
			}
		})
	}
}

func TestRetiredCapabilityEnvironmentFailsWithMigration(t *testing.T) {
	for _, value := range []string{"", "operate"} {
		t.Run(value, func(t *testing.T) {
			setCapabilityEnvironment(t, "inspect", "", "")
			t.Setenv(testCapabilitiesEnvironment, value)
			_, err := AdvertisedTools(t.Context())
			if err == nil || !strings.Contains(err.Error(), testCapabilitiesEnvironment) ||
				!strings.Contains(err.Error(), testToolsetsEnvironment) {
				t.Fatalf(
					"AdvertisedTools() error = %v, want %s migration to %s",
					err,
					testCapabilitiesEnvironment,
					testToolsetsEnvironment,
				)
			}
		})
	}
}

func TestEmptyNamedToolEnvironmentFailsAtStartup(t *testing.T) {
	for _, variable := range []string{testToolsEnvironment, testExcludeEnvironment} {
		t.Run(variable, func(t *testing.T) {
			setCapabilityEnvironment(t, "inspect", "", "")
			t.Setenv(variable, "")
			_, err := AdvertisedTools(t.Context())
			if err == nil || !strings.Contains(err.Error(), variable) {
				t.Fatalf("AdvertisedTools() error = %v, want empty %s startup failure", err, variable)
			}
		})
	}
}

func TestOnlyCapabilitiesResourceIsAdvertised(t *testing.T) {
	setCapabilityEnvironment(t, "inspect,manage,execute,teardown", "", "")
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-mcp-capability-test",
		ConfigFile: "/dev/null",
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := NewServer(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(t.Context(), AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "capability-test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	resources, err := clientSession.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) != 1 || resources.Resources[0].URI != testCapabilitiesURI {
		t.Fatalf("resources = %#v, want only %s", resources.Resources, testCapabilitiesURI)
	}
	templates, err := clientSession.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates.ResourceTemplates) != 0 {
		t.Fatalf("resource templates = %#v, want none", templates.ResourceTemplates)
	}
	read, err := clientSession.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: testCapabilitiesURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 {
		t.Fatalf("capability contents = %#v", read.Contents)
	}
	var payload struct {
		SchemaVersion int  `json:"schemaVersion"`
		Frozen        bool `json:"frozen"`
		Socket        struct {
			Selector                string `json:"selector"`
			SelectionProvenance     string `json:"selectionProvenance"`
			ServerState             string `json:"serverState"`
			ConfigurationProvenance string `json:"configurationProvenance"`
			NamespaceBoundary       string `json:"namespaceBoundary"`
		} `json:"socket"`
		ToolCount             int              `json:"toolCount"`
		EffectiveTools        []string         `json:"effectiveTools"`
		Tools                 []map[string]any `json:"tools"`
		HostCommandTools      int              `json:"hostCommandTools"`
		ToolFilteringBoundary string           `json:"toolFilteringBoundary"`
		Boundary              map[string]any   `json:"boundary"`
		Connection            map[string]any   `json:"connection"`
	}
	if err := json.Unmarshal([]byte(read.Contents[0].Text), &payload); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if payload.Socket.Selector == "" || payload.Socket.NamespaceBoundary != "tmux-objects-only" {
		t.Errorf("socket disclosure = %#v", payload.Socket)
	}
	if payload.SchemaVersion != 1 || !payload.Frozen {
		t.Errorf("capability report version/frozen = (%d, %t)", payload.SchemaVersion, payload.Frozen)
	}
	if payload.ToolCount != 45 || len(payload.Tools) != 45 ||
		len(payload.EffectiveTools) != 45 || payload.HostCommandTools != 0 {
		t.Errorf("capability inventory = count %d, rows %d, host commands %d", payload.ToolCount, len(payload.Tools), payload.HostCommandTools)
	}
	if payload.ToolFilteringBoundary != "interface-shaping-not-authorization" {
		t.Errorf("tool filtering boundary = %q", payload.ToolFilteringBoundary)
	}
	wantBoundary := map[string]any{
		"oneSocketPerProcess":    true,
		"perCallSocketSelection": false,
		"hostCommandExecution":   false,
		"dynamicResources":       false,
	}
	attachCommand, _ := payload.Connection["attachCommand"].(string)
	if !reflect.DeepEqual(payload.Boundary, wantBoundary) ||
		payload.Connection["socketSelector"] != payload.Socket.Selector ||
		payload.Connection["socketProvenance"] != payload.Socket.SelectionProvenance ||
		payload.Connection["serverState"] != payload.Socket.ServerState ||
		payload.Connection["configurationProvenance"] != payload.Socket.ConfigurationProvenance ||
		payload.Connection["resolvedSocketPath"] == "" ||
		!strings.Contains(attachCommand, " -N -S '") {
		t.Errorf("common boundary/connection disclosure = (%#v, %#v)", payload.Boundary, payload.Connection)
	}
	listed, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(map[string]map[string]any, len(payload.Tools))
	for _, row := range payload.Tools {
		name, _ := row["name"].(string)
		rows[name] = row
	}
	for _, tool := range listed.Tools {
		advertised, _ := tool.Meta[testCapabilityMetaKey].(map[string]any)
		if !reflect.DeepEqual(advertised, rows[tool.Name]) {
			t.Errorf("%s metadata/resource row drift:\nmeta=%#v\nresource=%#v", tool.Name, advertised, rows[tool.Name])
		}
	}
}

func TestCapabilityManifestDefaultSocketProvenanceControlsTeardownDefault(t *testing.T) {
	for _, name := range []string{ToolsetsEnvironmentVariable, ToolsEnvironmentVariable, ExcludeToolsEnvironmentVariable, SafetyEnvironmentVariable, RecipeToolEnvironmentVariable} {
		clearEnvironment(t, name)
	}
	configPath, cleanupConfig, err := MaterializeMinimalConfig()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanupConfig() })
	if configPath == os.DevNull {
		t.Fatal("the shipped minimal configuration resolved to the null device")
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "libtmux-mcp") {
		t.Fatalf("minimal configuration = %q, want the shipped package configuration", contents)
	}
	explicitCopy := filepath.Join(t.TempDir(), "operator.conf")
	if err := os.WriteFile(explicitCopy, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	explicitTarget, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-mcp",
		ConfigFile: explicitCopy,
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitProfile, err := profileForTarget(explicitTarget, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if explicitProfile.ConfigurationProvenance != "user-configured" || explicitProfile.defaultTeardown {
		t.Fatalf("explicit copied config profile = %#v, want user-configured provenance", explicitProfile)
	}

	shortSocketRoot, err := os.MkdirTemp("/tmp", "ltgm-") //nolint:usetesting // tmux socket paths must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortSocketRoot) })
	target, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(shortSocketRoot, "mcp"),
		ConfigFile: configPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = target.Kill(ctx)
	})
	absent, err := profileForTarget(target, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if absent.ConfigurationProvenance != "user-configured" || absent.defaultTeardown {
		t.Fatalf("unverified default profile = %#v, want no inferred ownership", absent)
	}
	surface, err := resolveToolSurface(absent)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.tools) != 41 {
		t.Fatalf("unverified minimal target defaults to %d tools, want teardown withheld", len(surface.tools))
	}

	existing, err := profileForTarget(target, "existing")
	if err != nil {
		t.Fatal(err)
	}
	if existing.ConfigurationProvenance != "unknown" || existing.defaultTeardown {
		t.Fatalf("existing default profile = %#v, want conservative provenance", existing)
	}
	surface, err = resolveToolSurface(existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.tools) != 41 {
		t.Fatalf("existing daemon defaults to %d tools, want teardown withheld", len(surface.tools))
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	profiles := make([]socketProfile, 2)
	errorsByLauncher := make([]error, 2)
	nonces := []string{"11111111111111111111111111111111", "22222222222222222222222222222222"}
	var launchers sync.WaitGroup
	for index := range nonces {
		launchers.Add(1)
		go func() {
			defer launchers.Done()
			profiles[index], errorsByLauncher[index] = pinDefaultMinimal(ctx, target, nonces[index])
		}()
	}
	launchers.Wait()
	owners := 0
	for index, profile := range profiles {
		if errorsByLauncher[index] != nil {
			t.Fatalf("launcher %d: %v", index, errorsByLauncher[index])
		}
		if profile.defaultTeardown {
			owners++
			if profile.ConfigurationProvenance != "minimal" || profile.ServerState != "created" {
				t.Errorf("owner profile = %#v", profile)
			}
		}
	}
	if owners != 1 {
		t.Fatalf("authenticated launch owners = %d, want exactly one; profiles=%#v", owners, profiles)
	}
	marker, present, err := target.GlobalSessionScope().RawOption(ctx, minimalOwnerOption)
	if err != nil || !present || !slices.Contains(nonces, marker) {
		t.Fatalf("retained owner marker = (%q, %t, %v)", marker, present, err)
	}
	if value, present, err := target.GetEnvironment(ctx, minimalOwnerEnvironment); err != nil || present {
		t.Fatalf("owner nonce remained in tmux environment = (%#v, %t, %v)", value, present, err)
	}
}

func TestRunShellCommandHasNoRetiredJobHandles(t *testing.T) {
	setCapabilityEnvironment(t, "execute", "", "")
	tools, err := AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var run *sdk.Tool
	for _, tool := range tools {
		if tool.Name == "run_shell_command" {
			run = tool
			break
		}
	}
	if run == nil {
		t.Fatal("run_shell_command is not advertised")
	}
	input := schemaProperties(t, run.InputSchema)
	for _, retired := range []string{"detach", "job_id", "jobId"} {
		if _, ok := input[retired]; ok {
			t.Errorf("run_shell_command input still exposes %q", retired)
		}
	}
	output := schemaProperties(t, run.OutputSchema)
	for _, retired := range []string{"job_id", "jobId", "detached"} {
		if _, ok := output[retired]; ok {
			t.Errorf("run_shell_command output still exposes %q", retired)
		}
	}
}

func TestCapabilityManifestDisclosesConfiguredInputMembership(t *testing.T) {
	setCapabilityEnvironment(t, "execute", "", "")
	tools, err := AdvertisedTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	send := namedTool(t, tools, "send_keys")
	if _, ok := schemaProperties(t, send.OutputSchema)["resolved_pane_ids"]; !ok {
		t.Fatal("send_keys output does not disclose resolved synchronized pane targets")
	}
	batch := namedTool(t, tools, "send_keys_batch")
	encoded, err := json.Marshal(batch.OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"resolved_pane_ids"`) {
		t.Fatal("send_keys_batch rows do not disclose configured pane membership")
	}
	run := namedTool(t, tools, "run_shell_command")
	if _, ok := schemaProperties(t, run.OutputSchema)["resolved_pane_ids"]; !ok {
		t.Fatal("run_shell_command output does not disclose its configured singleton")
	}
	paste := namedTool(t, tools, "paste_text")
	if _, ok := schemaProperties(t, paste.OutputSchema)["enter_pane_ids"]; !ok {
		t.Fatal("paste_text output does not disclose configured Enter membership")
	}
}

func setCapabilityEnvironment(t *testing.T, toolsets, included, excluded string) {
	t.Helper()
	clearEnvironment(t, SafetyEnvironmentVariable)
	clearEnvironment(t, RecipeToolEnvironmentVariable)
	clearEnvironment(t, testCapabilitiesEnvironment)
	t.Setenv(testToolsetsEnvironment, toolsets)
	clearEnvironment(t, testToolsEnvironment)
	clearEnvironment(t, testExcludeEnvironment)
	if included != "" {
		t.Setenv(testToolsEnvironment, included)
	}
	if excluded != "" {
		t.Setenv(testExcludeEnvironment, excluded)
	}
}

func clearEnvironment(t *testing.T, name string) {
	t.Helper()
	value := os.Getenv(name)
	t.Setenv(name, value)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func toolsForSets(names ...string) []string {
	var tools []string
	for _, name := range names {
		tools = append(tools, expectedToolsBySet[name]...)
	}
	slices.Sort(tools)
	return tools
}

func toolNames(tools []*sdk.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func namedTool(t *testing.T, tools []*sdk.Tool, name string) *sdk.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not advertised", name)
	return nil
}

func stringValues(value any) []string {
	switch values := value.(type) {
	case []string:
		return slices.Clone(values)
	case []any:
		converted := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				converted = append(converted, text)
			}
		}
		return converted
	default:
		return nil
	}
}

func aggregateBranches(t *testing.T, tool *sdk.Tool) []string {
	t.Helper()
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	operations, _ := properties["operations"].(map[string]any)
	items, _ := operations["items"].(map[string]any)
	oneOf, _ := items["oneOf"].([]any)
	names := make([]string, 0, len(oneOf))
	for _, raw := range oneOf {
		branch, _ := raw.(map[string]any)
		branchProperties, _ := branch["properties"].(map[string]any)
		toolProperty, _ := branchProperties["tool"].(map[string]any)
		if name, ok := toolProperty["const"].(string); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func aggregateMaxItems(t *testing.T, tool *sdk.Tool) int {
	t.Helper()
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	operations, _ := properties["operations"].(map[string]any)
	maximum, _ := operations["maxItems"].(float64)
	return int(maximum)
}

func aggregateItemsUnsatisfiable(t *testing.T, tool *sdk.Tool) bool {
	t.Helper()
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	properties, _ := schema["properties"].(map[string]any)
	operations, _ := properties["operations"].(map[string]any)
	if allowed, booleanSchema := operations["items"].(bool); booleanSchema {
		return !allowed
	}
	items, _ := operations["items"].(map[string]any)
	_, hasNot := items["not"]
	return hasNot
}

func schemaProperties(t *testing.T, schema any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Properties == nil {
		decoded.Properties = map[string]any{}
	}
	return decoded.Properties
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func setOf(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func assertStringSet(t *testing.T, label string, raw any, allowed map[string]bool, nonempty bool) {
	t.Helper()
	values, ok := raw.([]any)
	if !ok {
		t.Errorf("%s = %#v, want array", label, raw)
		return
	}
	if nonempty && len(values) == 0 {
		t.Errorf("%s is empty", label)
	}
	for _, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok || !allowed[value] {
			t.Errorf("%s contains %#v", label, rawValue)
		}
	}
}

func controlledOpener(toolset, reach string, capability map[string]any) string {
	switch toolset {
	case "inspect":
		outputs, _ := capability["outputClasses"].([]any)
		if slices.Contains(outputs, any("terminal-content")) {
			return "Read pane output; accepts no client-supplied executable input. Returned content may be sensitive or untrusted."
		}
		if slices.Contains(outputs, any("process-environment")) {
			return "Read the tmux environment; accepts no client-supplied executable input. Returned values may contain secrets."
		}
		if slices.Contains(outputs, any("configured-command")) {
			return "Read configured tmux commands; accepts no client-supplied executable input. Returned values may contain executable configuration."
		}
		return "Inspect tmux metadata; accepts no client-supplied executable input."
	case "manage":
		return "Change tmux state; no client-supplied executable input."
	case "teardown":
		return "Delete tmux state; accepts no command payload."
	case "execute":
		switch reach {
		case "configured-process":
			return "Start a pane's configured process; accepts no command payload."
		case "pane-input":
			return "Send input to a pane's program; a shell that receives it runs it with your user's permissions."
		case "pane-command":
			return "Run a shell command in a pane with your user's permissions."
		case "none":
			return "Change tmux state; no client-supplied executable input."
		}
	}
	return ""
}
