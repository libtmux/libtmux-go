package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Batches dispatch only registered tools, so withheld tools remain unreachable.

type dispatcher struct {
	annotations *mcp.ToolAnnotations
	// The originating request retains the session needed for caller-pane elicitation.
	call func(context.Context, *mcp.CallToolRequest, json.RawMessage) (any, error)
}

type batchCall struct {
	Tool      string         `json:"tool" jsonschema:"the name of the tool to call"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"the tool's arguments"`
}

type batchInput struct {
	Calls []batchCall `json:"calls" jsonschema:"the calls to run, in order"`
	// OnError is "stop" (default) or "continue".
	OnError string `json:"onError,omitempty" jsonschema:"what a failing call does to the calls after it; empty stops the batch"`
}

type batchResult struct {
	Tool   string         `json:"tool"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type batchOutput struct {
	Results   []batchResult `json:"results"`
	Completed int           `json:"completed"`
	// Skipped lists calls not attempted after a stop.
	Skipped []string `json:"skipped"`
	Failed  int      `json:"failed,omitempty"`
}

const (
	onErrorStop     = "stop"
	onErrorContinue = "continue"
)

// resolveOnError rejects unknown values because the modes leave different state.
func resolveOnError(requested string) (string, error) {
	switch requested {
	case "", onErrorStop:
		return onErrorStop, nil
	case onErrorContinue:
		return onErrorContinue, nil
	default:
		return "", fmt.Errorf("onError %q is not stop or continue", requested)
	}
}

// callReadOnlyToolsBatch permits only tools annotated read-only.
func (t *tools) callReadOnlyToolsBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	return t.runBatch(ctx, request, input, SafetyReadOnly)
}

// Completed mutations are not rolled back after a later failure.
func (t *tools) callMutatingToolsBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	return t.runBatch(ctx, request, input, SafetyMutating)
}

func (t *tools) callDestructiveToolsBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	return t.runBatch(ctx, request, input, SafetyDestructive)
}

// runBatch preflights registration and safety for every call. Argument schemas
// are checked immediately before each call executes.
func (t *tools) runBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
	tier SafetyLevel,
) (*mcp.CallToolResult, batchOutput, error) {
	if len(input.Calls) == 0 {
		return nil, batchOutput{}, errors.New("a batch needs at least one call")
	}
	onError, err := resolveOnError(input.OnError)
	if err != nil {
		return nil, batchOutput{}, err
	}
	for _, call := range input.Calls {
		known, served := t.dispatchers[call.Tool]
		if !served {
			if _, advertised := t.unbatchable[call.Tool]; advertised {
				return nil, batchOutput{}, fmt.Errorf(
					"%q cannot be called from inside a batch, so this batch ran "+
						"nothing; list its calls here instead", call.Tool)
			}
			return nil, batchOutput{}, fmt.Errorf(
				"%q is not a tool this server serves, so this batch ran nothing", call.Tool)
		}
		if !tier.permits(known.annotations) {
			return nil, batchOutput{}, fmt.Errorf(
				"%q is beyond what this batch may call, so this batch ran nothing", call.Tool)
		}
	}

	output := batchOutput{Results: make([]batchResult, 0, len(input.Calls))}
	for _, call := range input.Calls {
		encodedArguments, err := json.Marshal(call.Arguments)
		if err != nil {
			output.Results = append(output.Results, batchResult{
				Tool: call.Tool, Error: "arguments could not be encoded: " + err.Error(),
			})
			output.Failed++
			if onError == onErrorStop {
				break
			}
			continue
		}
		value, err := t.dispatchers[call.Tool].call(ctx, request, encodedArguments)
		if err != nil {
			output.Results = append(output.Results, batchResult{
				Tool: call.Tool, Error: err.Error(),
			})
			output.Failed++
			if onError == onErrorStop {
				break
			}
			continue
		}
		decoded := map[string]any{}
		encoded, err := json.Marshal(value)
		if err == nil {
			err = json.Unmarshal(encoded, &decoded)
		}
		if err != nil {
			output.Results = append(output.Results, batchResult{
				Tool: call.Tool, Error: "result could not be encoded: " + err.Error(),
			})
			output.Failed++
			if onError == onErrorStop {
				break
			}
			continue
		}
		output.Results = append(output.Results, batchResult{Tool: call.Tool, Result: decoded})
		output.Completed++
	}

	skipped := input.Calls[len(output.Results):]
	output.Skipped = make([]string, 0, len(skipped))
	for _, call := range skipped {
		output.Skipped = append(output.Skipped, call.Tool)
	}
	return nil, output, nil
}

func batched[In, Out any](
	ctx context.Context,
	request *mcp.CallToolRequest,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
	arguments json.RawMessage,
	schema *jsonschema.Resolved,
) (any, error) {
	// The SDK validates direct calls only; batches apply the same schema here.
	if schema != nil {
		if err := schema.Validate(argumentValue(arguments)); err != nil {
			return nil, fmt.Errorf("arguments are not what this tool takes: %w", err)
		}
	}
	// Strict decoding still rejects unknown fields if schema resolution fails.
	var input In
	if len(arguments) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(arguments))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, fmt.Errorf("arguments are not what this tool takes: %w", err)
		}
	}
	result, output, err := handler(ctx, request, input)
	if err != nil {
		return nil, err
	}
	if result != nil && result.IsError {
		return nil, fmt.Errorf("%s", batchErrorText(result))
	}
	return output, nil
}

// Absent arguments validate as an empty object rather than null.
func argumentValue(arguments json.RawMessage) any {
	var value any
	if len(arguments) == 0 || json.Unmarshal(arguments, &value) != nil || value == nil {
		return map[string]any{}
	}
	return value
}

func batchErrorText(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && text.Text != "" {
			return text.Text
		}
	}
	return "the call failed without a message"
}

// Batch tools register last and remain unbatchable to prevent nested batches.
func addBatchTools(server *mcp.Server, t *tools) {
	t.batchable = false
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "call_readonly_tools_batch",
		Annotations: readOnly("Batch of Reading Tools"),
		Description: "Run several reading tools in one request, in order. " +
			"Refuses the whole batch if any call would change tmux, so a batch " +
			"believed to be read-only never alters a session. Stops at the first " +
			"failure, and names the calls it skipped.",
	}, t.callReadOnlyToolsBatch)
	register(server, t, CapabilityPaneControl, &mcp.Tool{
		Name:        "call_mutating_tools_batch",
		Annotations: mutating("Batch of Changing Tools"),
		Description: "Run several tools in one request, in order, including ones " +
			"that change tmux. Laying out a window is a split, a resize, and a " +
			"command per pane; this makes it one call. Stops at the first " +
			"failure, and what already ran stays, because tmux has no " +
			"transaction; the reply names the calls it skipped.",
	}, t.callMutatingToolsBatch)
	register(server, t, CapabilityTmuxDestroy, &mcp.Tool{
		Name:        "call_destructive_tools_batch",
		Annotations: destructive("Batch of Ending Tools"),
		Description: "Run several tools in one request, in order, including the " +
			"ones that end something. Stops at the first failure, and nothing it " +
			"already ended comes back; the reply names the calls it skipped.",
	}, t.callDestructiveToolsBatch)
}
