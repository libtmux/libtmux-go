package mcp

import (
	"fmt"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
)

var authoritativeToolManifest = mustBuildToolManifest()

func toolManifest() ([]toolDefinition, error) {
	definitions := make([]toolDefinition, 0, len(authoritativeToolManifest))
	for _, definition := range authoritativeToolManifest {
		definitions = append(definitions, definition.clone())
	}
	return definitions, nil
}

func mustBuildToolManifest() []toolDefinition {
	definitions := buildToolManifest()
	bindAggregateCapabilities(definitions)
	bindAggregateSchemas(definitions)
	if err := validateToolManifest(definitions); err != nil {
		panic(fmt.Sprintf("invalid MCP tool manifest: %v", err))
	}
	return definitions
}

func bindAggregateCapabilities(definitions []toolDefinition) {
	byName := make(map[string]toolDefinition, len(definitions))
	for _, definition := range definitions {
		byName[definition.name] = definition
	}
	for index := range definitions {
		definition := &definitions[index]
		if definition.aggregate {
			deriveAggregateCapabilities(definition, byName)
		}
	}
}

func deriveAggregateCapabilities(definition *toolDefinition, byName map[string]toolDefinition) {
	effectSet := map[tmuxEffect]bool{effectObserve: true}
	outputSet := map[outputClass]bool{}
	for _, name := range definition.nestedAuthority {
		nested := byName[name]
		for _, effect := range nested.effects {
			effectSet[effect] = true
		}
		for _, output := range nested.outputClasses {
			outputSet[output] = true
		}
	}
	definition.effects = orderedEffects(effectSet)
	definition.outputClasses = orderedOutputs(outputSet)
}

func orderedEffects(values map[tmuxEffect]bool) []tmuxEffect {
	ordered := []tmuxEffect{effectObserve, effectChange, effectDelete}
	return slices.DeleteFunc(ordered, func(value tmuxEffect) bool { return !values[value] })
}

func orderedOutputs(values map[outputClass]bool) []outputClass {
	ordered := []outputClass{
		outputTmuxMetadata,
		outputTerminalContent,
		outputProcessEnvironment,
		outputConfiguredCommand,
	}
	return slices.DeleteFunc(ordered, func(value outputClass) bool { return !values[value] })
}

func bindAggregateSchemas(definitions []toolDefinition) {
	byName := make(map[string]toolDefinition, len(definitions))
	for _, definition := range definitions {
		byName[definition.name] = definition
	}
	for index := range definitions {
		definition := &definitions[index]
		if !definition.aggregate {
			continue
		}
		bindAggregateSchema(definition, byName)
	}
}

func bindAggregateSchema(definition *toolDefinition, byName map[string]toolDefinition) {
	operations := definition.inputSchema.Properties["operations"]
	oneOf := make([]*jsonschema.Schema, 0, len(definition.nestedAuthority))
	for _, nestedName := range definition.nestedAuthority {
		nested := byName[nestedName]
		name := any(nestedName)
		oneOf = append(oneOf, &jsonschema.Schema{
			Type:     "object",
			Required: []string{"tool"},
			Properties: map[string]*jsonschema.Schema{
				"tool":      {Type: "string", Const: &name},
				"arguments": nested.inputSchema.CloneSchemas(),
			},
			AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		})
	}
	minimum, maximum := 1, 16
	if len(oneOf) == 0 {
		operations.Items = &jsonschema.Schema{Not: &jsonschema.Schema{}}
	} else {
		operations.Items = &jsonschema.Schema{OneOf: oneOf}
	}
	operations.MinItems = &minimum
	operations.MaxItems = &maximum
}

func buildToolManifest() []toolDefinition {
	definitions := make([]toolDefinition, 0, 45)
	definitions = appendInspectDefinitions(definitions)
	definitions = appendManageDefinitions(definitions)
	definitions = appendExecuteDefinitions(definitions)
	definitions = appendTeardownDefinitions(definitions)
	return definitions
}

func appendInspectDefinitions(definitions []toolDefinition) []toolDefinition {
	metadata := outputs(outputTmuxMetadata)
	definitions = append(definitions,
		defineTool(toolDefinition{
			name: "list_sessions", title: "List sessions",
			details: "Lists sessions on the pinned tmux server.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: sinkMap(),
		}, (*tools).catalogListSessions),
		defineTool(toolDefinition{
			name: "list_windows", title: "List windows",
			details: "Lists windows, optionally only those in one named session.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: sinkMap(input("session", sinkTmuxLookup)),
		}, (*tools).catalogListWindows),
		defineTool(toolDefinition{
			name: "list_panes", title: "List panes",
			details: "Lists pane metadata and stable pane IDs.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: sinkMap(),
		}, (*tools).catalogListPanes),
		defineTool(toolDefinition{
			name: "get_server_info", title: "Get server info",
			details: "Reports whether the pinned server exists and its version.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true, inputSinks: sinkMap(),
		}, (*tools).catalogGetServerInfo),
		defineTool(toolDefinition{
			name: "get_session_info", title: "Get session info",
			details: "Returns metadata for one session.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true,
			inputSinks:       sinkMap(input("session_id", sinkTmuxLookup)),
		}, (*tools).catalogGetSessionInfo),
		defineTool(toolDefinition{
			name: "get_window_info", title: "Get window info",
			details: "Returns metadata for one window.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true,
			inputSinks:       sinkMap(input("window_id", sinkTmuxLookup)),
		}, (*tools).catalogGetWindowInfo),
		defineTool(toolDefinition{
			name: "get_pane_info", title: "Get pane info",
			details: "Returns metadata for one pane.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true,
			inputSinks:       sinkMap(input("pane_id", sinkTmuxLookup)),
		}, (*tools).catalogGetPaneInfo),
	)

	captureSinks := sinkMap(
		input("pane_id", sinkTmuxLookup),
		input("history", sinkNone),
		input("max_lines", sinkNone),
	)
	terminal := outputs(outputTerminalContent, outputTmuxMetadata)
	definitions = append(definitions,
		defineTool(toolDefinition{
			name: "capture_pane", title: "Capture a pane",
			details: "Returns bounded pane content and a cursor.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: terminal,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: captureSinks,
		}, (*tools).catalogCapturePane),
		defineTool(toolDefinition{
			name: "capture_since", title: "Capture new pane output",
			details: "Returns pane output produced after a cursor.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: terminal,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pane_id", sinkTmuxLookup), input("cursor", sinkNone),
				input("max_lines", sinkNone),
			),
		}, (*tools).catalogCaptureSince),
		defineTool(toolDefinition{
			name: "snapshot_pane", title: "Snapshot a pane",
			details: "Returns pane metadata and bounded terminal content together.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: terminal,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: captureSinks,
		}, (*tools).catalogSnapshotPane),
		defineTool(toolDefinition{
			name: "search_panes", title: "Search panes",
			details: "Searches the visible output of every pane.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: terminal,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pattern", sinkRegex), input("regex", sinkNone),
				input("max_matches_per_pane", sinkNone), input("max_lines", sinkNone),
			),
		}, (*tools).catalogSearchPanes),
		defineTool(toolDefinition{
			name: "find_pane_by_position", title: "Find pane by position",
			details: "Finds a pane at one of a window's four corners.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: metadata,
			mayExposeSecrets: true,
			inputSinks: sinkMap(
				input("window_id", sinkTmuxLookup), input("position", sinkTmuxLookup),
			),
		}, (*tools).catalogFindPaneByPosition),
		defineTool(toolDefinition{
			name: "wait_for_text", title: "Wait for pane text",
			details: "Waits for new pane output without accepting executable input.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: terminal,
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pane_id", sinkTmuxLookup), input("patterns", sinkRegex),
				input("stop", sinkRegex), input("regex", sinkNone), input("timeout", sinkNone),
				input("cursor", sinkNone), input("max_lines", sinkNone),
			),
		}, (*tools).catalogWaitForText),
		defineTool(toolDefinition{
			name: "get_tmux_variables", title: "Get tmux variables",
			details: "Reads a capped list of validated tmux variable names, not free-form formats.",
			toolset: toolsetInspect, processReach: processNone,
			effects:          effects(effectObserve),
			outputClasses:    outputs(outputTmuxMetadata, outputConfiguredCommand),
			mayExposeSecrets: true,
			inputSinks:       sinkMap(input("names", sinkTmuxLookup, sinkTmuxFormat)),
			inputLiteralization: map[string]string{
				"names": "validated-variable-name",
			},
		}, (*tools).catalogTmuxVariables),
		defineTool(toolDefinition{
			name: "show_option", title: "Show one option",
			details: "Reads one named tmux option.",
			toolset: toolsetInspect, processReach: processNone,
			effects:          effects(effectObserve),
			outputClasses:    outputs(outputTmuxMetadata, outputConfiguredCommand),
			mayExposeSecrets: true,
			inputSinks: sinkMap(
				input("name", sinkTmuxLookup), input("scope", sinkTmuxLookup),
				input("target", sinkTmuxLookup), input("effective", sinkNone),
			),
		}, (*tools).catalogShowOption),
		defineTool(toolDefinition{
			name: "show_environment", title: "Show tmux environment",
			details: "Reads the environment tmux passes to processes.",
			toolset: toolsetInspect, processReach: processNone,
			effects: effects(effectObserve), outputClasses: outputs(outputProcessEnvironment),
			mayExposeSecrets: true,
			inputSinks:       sinkMap(input("session", sinkTmuxLookup)),
		}, (*tools).catalogShowEnvironment),
		defineTool(toolDefinition{
			name: "show_hooks", title: "Show hooks",
			details: "Reads configured tmux hooks.",
			toolset: toolsetInspect, processReach: processNone,
			effects:          effects(effectObserve),
			outputClasses:    outputs(outputConfiguredCommand),
			mayExposeSecrets: true,
			inputSinks: sinkMap(
				input("scope", sinkTmuxLookup), input("target", sinkTmuxLookup),
				input("name", sinkTmuxLookup),
			),
		}, (*tools).catalogShowHooks),
	)

	nested := []string{
		"list_sessions", "list_windows", "list_panes", "get_server_info",
		"get_session_info", "get_window_info", "get_pane_info", "capture_pane",
		"capture_since", "snapshot_pane", "search_panes", "find_pane_by_position",
		"get_tmux_variables", "show_option", "show_environment", "show_hooks",
	}
	definitions = append(definitions, defineTool(toolDefinition{
		name: "call_read_tools_batch", title: "Call read tools in a batch",
		details: "Calls up to sixteen eligible inspect tools serially; inner tools receive no separate approval. Retained rows contain full nested envelopes, oversized results are marked resultTruncated, and the complete JSON-RPC response is at most 1,000,000 bytes.",
		toolset: toolsetInspect, processReach: processNone,
		effects:          effects(effectObserve),
		outputClasses:    outputs(outputTmuxMetadata),
		mayExposeSecrets: true, mayReturnUntrustedContent: true,
		inputSinks: sinkMap(
			input("operations", sinkNestedTool), input("on_error", sinkNone),
		),
		nestedAuthority: nested, aggregate: true,
	}, (*tools).catalogReadBatch))
	return definitions
}

func appendManageDefinitions(definitions []toolDefinition) []toolDefinition {
	manage := func(name, title, details string, sinks map[string][]inputSink, definition toolDefinition) toolDefinition {
		definition.name = name
		definition.title = title
		definition.details = details
		definition.toolset = toolsetManage
		definition.processReach = processNone
		if len(definition.effects) == 0 {
			definition.effects = effects(effectObserve, effectChange)
		}
		definition.outputClasses = outputs(outputTmuxMetadata)
		definition.mayReturnUntrustedContent = true
		definition.inputSinks = sinks
		return definition
	}
	definitions = append(definitions,
		defineTool(manage(
			"rename_session", "Rename a session", "Replaces a session's name.",
			sinkMap(
				input("session_id", sinkTmuxLookup),
				input("new_name", sinkTmuxState, sinkTmuxFormat),
			),
			toolDefinition{inputLiteralization: map[string]string{"new_name": "double-hash-once"}},
		), (*tools).catalogRenameSession),
		defineTool(manage(
			"rename_window", "Rename a window", "Replaces a window's name.",
			sinkMap(
				input("window_id", sinkTmuxLookup),
				input("new_name", sinkTmuxState, sinkTmuxFormat),
			),
			toolDefinition{inputLiteralization: map[string]string{"new_name": "double-hash-once"}},
		), (*tools).catalogRenameWindow),
		defineTool(manage(
			"select_window", "Select a window", "Makes one window active.",
			sinkMap(input("window_id", sinkTmuxLookup)), toolDefinition{},
		), (*tools).catalogSelectWindow),
		defineTool(manage(
			"select_pane", "Select a pane", "Makes one pane active.",
			sinkMap(input("pane_id", sinkTmuxLookup)), toolDefinition{},
		), (*tools).catalogSelectPane),
		defineTool(manage(
			"select_layout", "Select a layout",
			"Applies a named layout, a unique abbreviation for the running tmux version, "+
				"or a saved layout from get_window_info. Invalid syntax is rejected before "+
				"window lookup; tmux validates geometry when applying the layout.",
			sinkMap(input("window_id", sinkTmuxLookup), input("layout", sinkTmuxState)),
			toolDefinition{},
		), (*tools).catalogSelectLayout),
		defineTool(manage(
			"resize_window", "Resize a window", "Sets a window's width, height, or both.",
			sinkMap(
				input("window_id", sinkTmuxLookup), input("width", sinkTmuxState),
				input("height", sinkTmuxState),
			), toolDefinition{},
		), (*tools).catalogResizeWindow),
		defineTool(manage(
			"resize_pane", "Resize a pane", "Sets a pane's width, height, or both.",
			sinkMap(
				input("pane_id", sinkTmuxLookup), input("width", sinkTmuxState),
				input("height", sinkTmuxState),
			), toolDefinition{},
		), (*tools).catalogResizePane),
		defineTool(manage(
			"move_window", "Move a window", "Moves a window to another session, optionally at an index.",
			sinkMap(
				input("window_id", sinkTmuxLookup), input("session_id", sinkTmuxLookup),
				input("index", sinkTmuxState),
			), toolDefinition{},
		), (*tools).catalogMoveWindow),
		defineTool(manage(
			"swap_pane", "Swap panes", "Swaps the positions of two panes.",
			sinkMap(input("pane_id", sinkTmuxLookup), input("other_pane_id", sinkTmuxLookup)),
			toolDefinition{},
		), (*tools).catalogSwapPane),
		defineTool(manage(
			"set_pane_title", "Set pane title", "Replaces a pane's literal title.",
			sinkMap(
				input("pane_id", sinkTmuxLookup),
				input("title", sinkTmuxState, sinkTmuxFormat),
			),
			toolDefinition{inputLiteralization: map[string]string{"title": "double-hash-once"}},
		), (*tools).catalogSetPaneTitle),
	)
	definitions = append(definitions,
		defineTool(toolDefinition{
			name: "wait_for_channel", title: "Wait for a channel",
			details: "Waits on tmux's channel state with a bounded timeout.",
			toolset: toolsetManage, processReach: processNone,
			effects:       effects(effectChange),
			outputClasses: outputs(outputTmuxMetadata), mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("channel", sinkTmuxState), input("timeout", sinkNone),
				input("drain_first", sinkTmuxState),
			),
		}, (*tools).catalogWaitForChannel),
		defineTool(manage(
			"signal_channel", "Signal a channel", "Signals one server-wide tmux channel.",
			sinkMap(input("channel", sinkTmuxState)),
			toolDefinition{effects: effects(effectChange)},
		), (*tools).catalogSignalChannel),
		defineTool(manage(
			"set_mouse_enabled", "Set mouse handling", "Enables or disables tmux mouse handling.",
			sinkMap(input("enabled", sinkTmuxState)),
			toolDefinition{effects: effects(effectChange)},
		), (*tools).catalogSetMouseEnabled),
		defineTool(manage(
			"set_history_limit", "Set history limit",
			"Sets a bounded integer scrollback limit for future panes in a session.",
			sinkMap(input("session_id", sinkTmuxLookup), input("lines", sinkTmuxState)),
			toolDefinition{effects: effects(effectChange)},
		), (*tools).catalogSetHistoryLimit),
	)
	return definitions
}

func appendExecuteDefinitions(definitions []toolDefinition) []toolDefinition {
	definitions = append(definitions,
		defineTool(toolDefinition{
			name: "create_session", title: "Create a session",
			details: "Creates a detached session whose first pane runs the configured process.",
			toolset: toolsetExecute, processReach: processConfiguredProcess,
			effects: effects(effectObserve, effectChange), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("session_name", sinkTmuxState, sinkTmuxFormat),
				input("window_name", sinkTmuxState, sinkTmuxFormat),
				input("start_directory", sinkTmuxState, sinkTmuxFormat), input("width", sinkTmuxState),
				input("height", sinkTmuxState),
			),
			inputLiteralization: map[string]string{
				"session_name": "double-hash-once", "window_name": "double-hash-once",
				"start_directory": "double-hash-once",
			},
		}, (*tools).catalogCreateSession),
		defineTool(toolDefinition{
			name: "create_window", title: "Create a window",
			details: "Creates a window whose first pane runs the configured process.",
			toolset: toolsetExecute, processReach: processConfiguredProcess,
			effects: effects(effectObserve, effectChange), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("session_id", sinkTmuxLookup),
				input("window_name", sinkTmuxState, sinkTmuxFormat),
				input("start_directory", sinkTmuxState, sinkTmuxFormat), input("attach", sinkTmuxState),
				input("direction", sinkTmuxState),
			),
			inputLiteralization: map[string]string{
				"window_name": "double-hash-once", "start_directory": "double-hash-once",
			},
		}, (*tools).catalogCreateWindow),
		defineTool(toolDefinition{
			name: "split_window", title: "Split a window",
			details: "Creates a pane whose configured process starts after the split.",
			toolset: toolsetExecute, processReach: processConfiguredProcess,
			effects: effects(effectObserve, effectChange), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pane_id", sinkTmuxLookup), input("direction", sinkTmuxState),
				input("percent", sinkTmuxState),
				input("start_directory", sinkTmuxState, sinkTmuxFormat),
			),
			inputLiteralization: map[string]string{"start_directory": "double-hash-once"},
		}, (*tools).catalogSplitWindow),
		defineTool(toolDefinition{
			name: "respawn_pane", title: "Respawn a pane",
			details: "Kills the pane's current process and starts its configured process again.",
			toolset: toolsetExecute, processReach: processConfiguredProcess,
			effects: effects(effectObserve, effectChange, effectDelete), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pane_id", sinkTmuxLookup),
				input("start_directory", sinkTmuxState, sinkTmuxFormat),
			),
			inputLiteralization: map[string]string{"start_directory": "double-hash-once"},
		}, (*tools).catalogRespawnPane),
		defineTool(toolDefinition{
			name: "run_shell_command", title: "Run a shell command",
			details: "Runs one authored command only for configured singleton membership, checks it before setup and again before dispatch, and waits for framed completion.",
			toolset: toolsetExecute, processReach: processPaneCommand,
			effects:          effects(effectObserve, effectChange),
			outputClasses:    outputs(outputTerminalContent, outputTmuxMetadata),
			mayExposeSecrets: true, mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pane_id", sinkTmuxLookup), input("command", sinkPaneInput, sinkShellCommand),
				input("timeout", sinkNone), input("max_lines", sinkNone),
				input("suppress_history", sinkNone),
			),
		}, (*tools).catalogRunShellCommand),
		defineTool(toolDefinition{
			name: "send_keys", title: "Send keys",
			details: "Sends input after validating sorted configured synchronized membership; reported ids describe preflight membership, not proven effects.",
			toolset: toolsetExecute, processReach: processPaneInput,
			effects: effects(effectObserve, effectChange), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pane_id", sinkTmuxLookup), input("keys", sinkPaneInput),
				input("literal", sinkNone),
			),
		}, (*tools).catalogSendKeys),
		defineTool(toolDefinition{
			name: "send_keys_batch", title: "Send keys in a batch",
			details: "Sends up to sixty-four ordered pane-input operations, each with a fresh configured-membership preflight.",
			toolset: toolsetExecute, processReach: processPaneInput,
			effects: effects(effectObserve, effectChange), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("operations", sinkTmuxLookup, sinkPaneInput), input("on_error", sinkNone),
			),
		}, (*tools).catalogSendKeysBatch),
		defineTool(toolDefinition{
			name: "paste_text", title: "Paste text",
			details: "Pastes literal text only to its target; optional Enter appends one newline to the same private buffer.",
			toolset: toolsetExecute, processReach: processPaneInput,
			effects: effects(effectObserve, effectChange), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true,
			inputSinks: sinkMap(
				input("pane_id", sinkTmuxLookup), input("text", sinkPaneInput),
				input("enter", sinkPaneInput),
			),
		}, (*tools).catalogPasteText),
		defineTool(toolDefinition{
			name: "set_synchronize_panes", title: "Set synchronized panes",
			details: "Sets the window synchronization default; pane-level overrides determine later configured input membership and effects can still differ.",
			toolset: toolsetExecute, processReach: processNone,
			effects: effects(effectChange), outputClasses: outputs(outputTmuxMetadata),
			mayReturnUntrustedContent: true, amplifiesFutureInput: true,
			inputSinks: sinkMap(
				input("window_id", sinkTmuxLookup), input("enabled", sinkTmuxState),
			),
		}, (*tools).catalogSetSynchronizePanes),
	)
	return definitions
}

func appendTeardownDefinitions(definitions []toolDefinition) []toolDefinition {
	teardown := func(name, title, details string, sinks map[string][]inputSink) toolDefinition {
		definition := toolDefinition{
			name: name, title: title, details: details,
			toolset: toolsetTeardown, processReach: processNone,
			effects: effects(effectDelete), outputClasses: outputs(outputTmuxMetadata),
			inputSinks: sinks,
		}
		if name != "clear_pane_scrollback" {
			definition.effects = effects(effectObserve, effectDelete)
		}
		return definition
	}
	definitions = append(definitions,
		defineTool(teardown(
			"clear_pane_scrollback", "Clear pane scrollback",
			"Deletes retained scrollback from one pane.",
			sinkMap(input("pane_id", sinkTmuxLookup)),
		), (*tools).catalogClearPaneScrollback),
		defineTool(teardown(
			"kill_pane", "Kill a pane", "Deletes one pane and ends its process.",
			sinkMap(input("pane_id", sinkTmuxLookup), input("confirm_self", sinkNone)),
		), (*tools).catalogKillPane),
		defineTool(teardown(
			"kill_window", "Kill a window", "Deletes one window and every pane in it.",
			sinkMap(input("window_id", sinkTmuxLookup), input("confirm_self", sinkNone)),
		), (*tools).catalogKillWindow),
		defineTool(teardown(
			"kill_session", "Kill a session", "Deletes one session and every window and pane in it.",
			sinkMap(input("session_id", sinkTmuxLookup), input("confirm_self", sinkNone)),
		), (*tools).catalogKillSession),
	)
	return definitions
}
