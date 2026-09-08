package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CapabilityMetaKey is the MCP metadata key carrying the complete capability
// row that also drives registration and tmux://capabilities.
const CapabilityMetaKey = "com.git-pull.libtmux-mcp/capability"

type toolset string

const (
	toolsetInspect  toolset = "inspect"
	toolsetManage   toolset = "manage"
	toolsetExecute  toolset = "execute"
	toolsetTeardown toolset = "teardown"
)

var allToolsets = []toolset{
	toolsetInspect,
	toolsetManage,
	toolsetExecute,
	toolsetTeardown,
}

type processReach string

const (
	processNone              processReach = "none"
	processConfiguredProcess processReach = "configured-process"
	processPaneInput         processReach = "pane-input"
	processPaneCommand       processReach = "pane-command"
	processHostCommand       processReach = "host-command"
)

type tmuxEffect string

const (
	effectObserve tmuxEffect = "observe"
	effectChange  tmuxEffect = "change"
	effectDelete  tmuxEffect = "delete"
)

type outputClass string

const (
	outputTmuxMetadata       outputClass = "tmux-metadata"
	outputTerminalContent    outputClass = "terminal-content"
	outputProcessEnvironment outputClass = "process-environment"
	outputConfiguredCommand  outputClass = "configured-command"
)

type inputSink string

const (
	sinkNone         inputSink = "none"
	sinkTmuxLookup   inputSink = "tmux-lookup"
	sinkTmuxState    inputSink = "tmux-state"
	sinkPaneInput    inputSink = "pane-input"
	sinkShellCommand inputSink = "shell-command"
	sinkProcessArgv  inputSink = "process-argv"
	sinkRegex        inputSink = "regex"
	sinkNestedTool   inputSink = "nested-tool"
	sinkTmuxFormat   inputSink = "tmux-format"
)

type toolHandler[In, Out any] func(
	*tools,
	context.Context,
	*sdk.CallToolRequest,
	In,
) (*sdk.CallToolResult, Out, error)

type toolDefinition struct {
	name                      string
	title                     string
	details                   string
	toolset                   toolset
	processReach              processReach
	effects                   []tmuxEffect
	outputClasses             []outputClass
	mayExposeSecrets          bool
	mayReturnUntrustedContent bool
	amplifiesFutureInput      bool
	inputSinks                map[string][]inputSink
	inputLiteralization       map[string]string
	nestedAuthority           []string
	aggregate                 bool
	inputSchema               *jsonschema.Schema
	outputSchema              *jsonschema.Schema
	register                  func(*sdk.Server, *tools, toolDefinition)
	bindDispatcher            func(*tools, toolDefinition)
}

func defineTool[In, Out any](definition toolDefinition, handler toolHandler[In, Out]) toolDefinition {
	input, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("%s input schema: %v", definition.name, err))
	}
	constrain(definition.name, input)
	output, err := jsonschema.For[Out](nil)
	if err != nil {
		panic(fmt.Sprintf("%s output schema: %v", definition.name, err))
	}
	listsAreLists(output)
	definition.inputSchema = input
	definition.outputSchema = output
	definition.effects = slices.Clone(definition.effects)
	definition.outputClasses = slices.Clone(definition.outputClasses)
	definition.inputSinks = cloneSinks(definition.inputSinks)
	definition.inputLiteralization = cloneLiteralization(definition.inputLiteralization)
	definition.nestedAuthority = slices.Clone(definition.nestedAuthority)
	definition.register = func(server *sdk.Server, registry *tools, effective toolDefinition) {
		registerDefinition(server, registry, effective, handler)
	}
	definition.bindDispatcher = func(registry *tools, effective toolDefinition) {
		bindDefinitionDispatcher(registry, effective, handler)
	}
	return definition
}

func cloneSinks(source map[string][]inputSink) map[string][]inputSink {
	cloned := make(map[string][]inputSink, len(source))
	for input, sinks := range source {
		cloned[input] = slices.Clone(sinks)
	}
	return cloned
}

func (definition toolDefinition) clone() toolDefinition {
	definition.effects = slices.Clone(definition.effects)
	definition.outputClasses = slices.Clone(definition.outputClasses)
	definition.inputSinks = cloneSinks(definition.inputSinks)
	definition.inputLiteralization = cloneLiteralization(definition.inputLiteralization)
	definition.nestedAuthority = slices.Clone(definition.nestedAuthority)
	return definition
}

func cloneLiteralization(source map[string]string) map[string]string {
	if source == nil {
		return map[string]string{}
	}
	return maps.Clone(source)
}

func (definition toolDefinition) description() string {
	return definition.controlledOpener() + " " + definition.details
}

func (definition toolDefinition) controlledOpener() string {
	switch definition.toolset {
	case toolsetInspect:
		switch {
		case slices.Contains(definition.outputClasses, outputTerminalContent):
			return "Read pane output; accepts no client-supplied executable input. " +
				"Returned content may be sensitive or untrusted."
		case slices.Contains(definition.outputClasses, outputProcessEnvironment):
			return "Read the tmux environment; accepts no client-supplied executable input. " +
				"Returned values may contain secrets."
		case slices.Contains(definition.outputClasses, outputConfiguredCommand):
			return "Read configured tmux commands; accepts no client-supplied executable input. " +
				"Returned values may contain executable configuration."
		default:
			return "Inspect tmux metadata; accepts no client-supplied executable input."
		}
	case toolsetManage:
		return "Change tmux state; no client-supplied executable input."
	case toolsetExecute:
		switch definition.processReach {
		case processConfiguredProcess:
			return "Start a pane's configured process; accepts no command payload."
		case processPaneInput:
			return "Send input to a pane's program; a shell that receives it runs it with your user's permissions."
		case processPaneCommand:
			return "Run a shell command in a pane with your user's permissions."
		case processHostCommand:
			panic("host-command reach is prohibited: " + definition.name)
		case processNone:
			return "Change tmux state; no client-supplied executable input."
		}
	case toolsetTeardown:
		return "Delete tmux state; accepts no command payload."
	}
	panic("tool has no controlled capability opener: " + definition.name)
}

func (definition toolDefinition) annotations() *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{
		Title:           definition.title,
		ReadOnlyHint:    false,
		DestructiveHint: new(true),
		IdempotentHint:  false,
		OpenWorldHint:   new(true),
	}
}

func (definition toolDefinition) capability() map[string]any {
	effects := make([]string, 0, len(definition.effects))
	for _, effect := range definition.effects {
		effects = append(effects, string(effect))
	}
	outputs := make([]string, 0, len(definition.outputClasses))
	for _, output := range definition.outputClasses {
		outputs = append(outputs, string(output))
	}
	return map[string]any{
		"name":                      definition.name,
		"title":                     definition.title,
		"description":               definition.description(),
		"toolset":                   string(definition.toolset),
		"processReach":              string(definition.processReach),
		"tmuxEffects":               effects,
		"outputClasses":             outputs,
		"mayExposeSecrets":          definition.mayExposeSecrets,
		"mayReturnUntrustedContent": definition.mayReturnUntrustedContent,
		"amplifiesFutureInput":      definition.amplifiesFutureInput,
		"annotations": map[string]any{
			"readOnlyHint":    false,
			"destructiveHint": true,
			"idempotentHint":  false,
			"openWorldHint":   true,
		},
		"inputSchema":         definition.inputSchema,
		"outputSchema":        definition.outputSchema,
		"inputLiteralization": maps.Clone(definition.inputLiteralization),
		"nestedAuthority":     slices.Clone(definition.nestedAuthority),
	}
}

func (definition toolDefinition) sdkTool() *sdk.Tool {
	return &sdk.Tool{
		Name:         definition.name,
		Title:        definition.title,
		Description:  definition.description(),
		Annotations:  definition.annotations(),
		InputSchema:  definition.inputSchema,
		OutputSchema: definition.outputSchema,
		Meta: sdk.Meta{
			CapabilityMetaKey: definition.capability(),
		},
	}
}

func registerDefinition[In, Out any](
	server *sdk.Server,
	registry *tools,
	definition toolDefinition,
	handler toolHandler[In, Out],
) {
	registered := boundDefinitionHandler(registry, handler)
	if !definition.aggregate {
		registered = withRequestRuntime(registry, registered)
	}
	sdk.AddTool(server, definition.sdkTool(), registered)
	if definition.aggregate {
		return
	}
	installDispatcher(registry, definition, registered)
}

func bindDefinitionDispatcher[In, Out any](
	registry *tools,
	definition toolDefinition,
	handler toolHandler[In, Out],
) {
	if definition.aggregate {
		return
	}
	registered := withRequestRuntime(registry, boundDefinitionHandler(registry, handler))
	installDispatcher(registry, definition, registered)
}

func boundDefinitionHandler[In, Out any](
	registry *tools,
	handler toolHandler[In, Out],
) func(context.Context, *sdk.CallToolRequest, In) (*sdk.CallToolResult, Out, error) {
	return func(
		ctx context.Context,
		request *sdk.CallToolRequest,
		input In,
	) (*sdk.CallToolResult, Out, error) {
		return handler(registry, ctx, request, input)
	}
}

func installDispatcher[In, Out any](
	registry *tools,
	definition toolDefinition,
	registered func(context.Context, *sdk.CallToolRequest, In) (*sdk.CallToolResult, Out, error),
) {
	inputResolved, err := definition.inputSchema.Resolve(nil)
	if err != nil {
		registry.registrationErr = fmt.Errorf("%s input schema: %w", definition.name, err)
		return
	}
	outputResolved, err := definition.outputSchema.Resolve(nil)
	if err != nil {
		registry.registrationErr = fmt.Errorf("%s output schema: %w", definition.name, err)
		return
	}
	registry.dispatchers[definition.name] = dispatcher{
		call: func(
			ctx context.Context,
			request *sdk.CallToolRequest,
			arguments json.RawMessage,
		) (*sdk.CallToolResult, error) {
			return batched(
				ctx, request, registered, arguments, inputResolved, outputResolved,
			)
		},
	}
}

func validateToolManifest(definitions []toolDefinition) error {
	names := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.name == "" || definition.title == "" || definition.details == "" {
			return errors.New("tool manifest contains blank identity text")
		}
		if _, duplicate := names[definition.name]; duplicate {
			return fmt.Errorf("duplicate tool %q", definition.name)
		}
		names[definition.name] = struct{}{}
	}
	for _, definition := range definitions {
		if definition.register == nil || definition.inputSchema == nil || definition.outputSchema == nil ||
			(!definition.aggregate && definition.bindDispatcher == nil) {
			return fmt.Errorf("%s has no handler or schema", definition.name)
		}
		if len(definition.effects) == 0 || len(definition.outputClasses) == 0 {
			return fmt.Errorf("%s has an empty effect or output set", definition.name)
		}
		if definition.processReach == processHostCommand {
			return fmt.Errorf("%s exposes prohibited host-command reach", definition.name)
		}
		if definition.amplifiesFutureInput != (definition.name == "set_synchronize_panes") {
			return fmt.Errorf("%s has incorrect future-input amplification", definition.name)
		}
		if err := validateSchemaSinks(definition); err != nil {
			return err
		}
		if err := validateReach(definition); err != nil {
			return err
		}
		if !strings.HasPrefix(definition.description(), definition.controlledOpener()+" ") {
			return fmt.Errorf("%s does not begin with its controlled opener", definition.name)
		}
		for _, nested := range definition.nestedAuthority {
			if nested == definition.name {
				return fmt.Errorf("%s declares itself as nested authority", definition.name)
			}
			if _, ok := names[nested]; !ok {
				return fmt.Errorf("%s has unknown nested authority %q", definition.name, nested)
			}
		}
		if definition.aggregate {
			expected := definition.clone()
			byName := make(map[string]toolDefinition, len(definitions))
			for _, candidate := range definitions {
				byName[candidate.name] = candidate
			}
			deriveAggregateCapabilities(&expected, byName)
			if !slices.Equal(definition.effects, expected.effects) ||
				!slices.Equal(definition.outputClasses, expected.outputClasses) {
				return fmt.Errorf("%s capability sets do not equal its nested authority union", definition.name)
			}
		}
	}
	return nil
}

func validateSchemaSinks(definition toolDefinition) error {
	properties := definition.inputSchema.Properties
	for name := range properties {
		if name == "socket" || name == "socket_name" || name == "socket_path" {
			return fmt.Errorf("%s exposes per-call socket selector %q", definition.name, name)
		}
	}
	propertyNames := slices.Sorted(maps.Keys(properties))
	sinkNames := slices.Sorted(maps.Keys(definition.inputSinks))
	if !slices.Equal(propertyNames, sinkNames) {
		return fmt.Errorf(
			"%s sink/schema mismatch: schema=%v sinks=%v",
			definition.name,
			propertyNames,
			sinkNames,
		)
	}
	for input, sinks := range definition.inputSinks {
		if len(sinks) == 0 {
			return fmt.Errorf("%s input %q has no sink", definition.name, input)
		}
		control, controlled := definition.inputLiteralization[input]
		formatBearing := slices.Contains(sinks, sinkTmuxFormat)
		if formatBearing != controlled {
			return fmt.Errorf(
				"%s input %q tmux-format sink/control mismatch",
				definition.name,
				input,
			)
		}
		if controlled && control != "double-hash-once" && control != "validated-variable-name" {
			return fmt.Errorf(
				"%s input %q has unsupported literalization %q",
				definition.name,
				input,
				control,
			)
		}
	}
	for input := range definition.inputLiteralization {
		if _, known := definition.inputSinks[input]; !known {
			return fmt.Errorf("%s literalizes unknown input %q", definition.name, input)
		}
	}
	return nil
}

func validateReach(definition toolDefinition) error {
	all := make(map[inputSink]bool)
	for _, sinks := range definition.inputSinks {
		for _, sink := range sinks {
			all[sink] = true
		}
	}
	paneInput := all[sinkPaneInput]
	shellCommand := all[sinkShellCommand]
	processArgv := all[sinkProcessArgv]
	switch definition.processReach {
	case processNone:
		if paneInput || shellCommand || processArgv {
			return fmt.Errorf("%s has executable sinks with reach none", definition.name)
		}
	case processConfiguredProcess:
		if paneInput || shellCommand || processArgv {
			return fmt.Errorf("%s misstates configured-process reach", definition.name)
		}
	case processPaneInput:
		if !paneInput || shellCommand || processArgv {
			return fmt.Errorf("%s pane-input reach disagrees with its sinks", definition.name)
		}
	case processPaneCommand:
		if !shellCommand || processArgv {
			return fmt.Errorf("%s pane-command reach disagrees with its sinks", definition.name)
		}
	case processHostCommand:
		return fmt.Errorf("%s exposes prohibited host-command reach", definition.name)
	default:
		return fmt.Errorf("%s has unknown process reach %q", definition.name, definition.processReach)
	}
	switch definition.toolset {
	case toolsetInspect:
		allowedEffects := slices.Equal(definition.effects, []tmuxEffect{effectObserve})
		if definition.aggregate {
			allowedEffects = true
		}
		if definition.processReach != processNone || !allowedEffects {
			return fmt.Errorf("%s is not observational inspect authority", definition.name)
		}
	case toolsetManage:
		if definition.processReach != processNone {
			return fmt.Errorf("%s manage authority reaches a workload process", definition.name)
		}
	case toolsetExecute:
		if definition.processReach == processNone && definition.name != "set_synchronize_panes" {
			return fmt.Errorf("%s execute authority has no process reach", definition.name)
		}
	case toolsetTeardown:
		if definition.processReach != processNone || !slices.Contains(definition.effects, effectDelete) {
			return fmt.Errorf("%s is not direct teardown authority", definition.name)
		}
	default:
		return fmt.Errorf("%s has unknown toolset %q", definition.name, definition.toolset)
	}
	return nil
}

func sinkMap(entries ...sinkEntry) map[string][]inputSink {
	sinks := make(map[string][]inputSink, len(entries))
	for _, entry := range entries {
		if _, duplicate := sinks[entry.name]; duplicate {
			panic("duplicate sink declaration for " + entry.name)
		}
		sinks[entry.name] = slices.Clone(entry.sinks)
	}
	return sinks
}

type sinkEntry struct {
	name  string
	sinks []inputSink
}

func input(name string, first inputSink, rest ...inputSink) sinkEntry {
	return sinkEntry{name: name, sinks: append([]inputSink{first}, rest...)}
}

func effects(first tmuxEffect, rest ...tmuxEffect) []tmuxEffect {
	return append([]tmuxEffect{first}, rest...)
}

func outputs(first outputClass, rest ...outputClass) []outputClass {
	return append([]outputClass{first}, rest...)
}
