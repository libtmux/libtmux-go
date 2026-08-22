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

// Several calls in one request, because a client that knows what it wants
// should not pay a round trip per step.
//
// Laying out a window is a split, a resize, and a command in each pane: five
// calls whose answers the client does not read until the last. A batch is one.
//
// What a batch may reach is the same set a single call may reach, and for the
// same reason: a tool is dispatched here only if it was advertised, so a tool
// the safety level withheld is not reachable by putting its name in a list.
// That is a property of how registration works rather than a second list to
// keep in step — the earlier arrangement kept one, and a tool added without
// remembering it would have been quietly unbatchable.

// dispatcher is one advertised tool, as a batch reaches it.
type dispatcher struct {
	// annotations are the tool's own, which is what decides the tier a batch
	// may call it from. A tool that says it is destructive is governed by
	// having said so, here as well as at registration.
	annotations *mcp.ToolAnnotations
	// call runs the tool with arguments a client supplied. The originating
	// request travels with it, because a guard that asks the person needs the
	// session to ask on -- a batched write to the caller's own pane is the
	// same write as a direct one.
	call func(context.Context, *mcp.CallToolRequest, json.RawMessage) (any, error)
}

// batchCall is one tool call inside a batch.
type batchCall struct {
	// Tool is the tool's name, as it appears on its own.
	Tool string `json:"tool" jsonschema:"the name of the tool to call"`
	// Arguments are that tool's arguments, exactly as it takes them alone.
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"the tool's arguments"`
}

// batchInput runs several calls in one request.
type batchInput struct {
	// Calls run in order, each after the one before it finished.
	Calls []batchCall `json:"calls" jsonschema:"the calls to run, in order"`
	// OnError decides what a failure does to the calls after it. Stopping is
	// the default because a batch is usually a sequence -- split, then resize,
	// then run -- where a step nobody took makes the ones after it wrong.
	// Independent calls are the other ordinary shape, and there a stop turns
	// one failure into a whole batch nobody can tell the state of.
	OnError string `json:"onError,omitempty" jsonschema:"what a failing call does to the calls after it; empty stops the batch"`
}

// batchResult is what one call in a batch produced.
type batchResult struct {
	// Tool is the tool that ran.
	Tool string `json:"tool"`
	// Result is that tool's structured result, absent when it failed.
	Result map[string]any `json:"result,omitempty"`
	// Error describes the failure, absent when the call succeeded.
	Error string `json:"error,omitempty"`
}

// batchOutput reports each call in the order it ran.
type batchOutput struct {
	// Results holds one entry per call attempted.
	Results []batchResult `json:"results"`
	// Completed is how many calls succeeded. A batch stops at its first
	// failure, so this is also how many ran without one; the call that failed
	// is the next entry in Results, with its error.
	Completed int `json:"completed"`
	// Skipped names the tools after the failure, in order, which never ran. A
	// caller of the mutating batch has to know which of its changes were not
	// made, and counting the difference between the calls it sent and the
	// results it got back is not something a reply should ask of it. It is
	// empty when the batch was told to continue, where nothing is skipped and
	// Failed is what says how it went.
	Skipped []string `json:"skipped"`
	// Failed is how many calls failed, which only a batch told to continue can
	// have more than one of. Reading it off Results means walking them.
	Failed int `json:"failed,omitempty"`
}

const (
	// onErrorStop ends a batch at its first failure.
	onErrorStop = "stop"
	// onErrorContinue runs every call whatever the ones before it did.
	onErrorContinue = "continue"
)

// resolveOnError reads what a failure should do. An unknown value is refused
// rather than treated as the default, because the two answers differ in what
// they leave behind and a caller that asked for one and got the other cannot
// tell from the reply.
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

// callReadOnlyToolsBatch runs several reading tools in one request.
//
// It refuses a tool that changes tmux rather than running the reads before it,
// because a batch a client believed was read-only having altered a session is
// worse than a batch that did nothing. The check happens before anything runs.
func (t *tools) callReadOnlyToolsBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	return t.runBatch(ctx, request, input, SafetyReadOnly)
}

// callMutatingToolsBatch runs several tools in one request, including ones
// that change tmux.
//
// A batch stops at its first failure. tmux has no transaction, so what already
// ran stays; the results say how far it got rather than implying it undid
// anything.
func (t *tools) callMutatingToolsBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	return t.runBatch(ctx, request, input, SafetyMutating)
}

// callDestructiveToolsBatch runs several tools in one request, including the
// ones that end something.
//
// It exists so that cleaning up is one call rather than several: a client
// tearing down what it built kills several panes, and doing that one call at a
// time races tmux's own teardown of the window when the last one goes.
func (t *tools) callDestructiveToolsBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	return t.runBatch(ctx, request, input, SafetyDestructive)
}

// runBatch performs the calls in order, stopping at the first failure.
//
// Every call is checked against the batch's tier before any of them runs. A
// batch that would have ended something halfway through is refused whole
// rather than run up to that point, which is the difference between a client
// learning it asked for the wrong thing and a client learning it after three
// panes were gone.
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

	// One result is appended per call attempted, so whatever is left is what
	// the stop skipped.
	skipped := input.Calls[len(output.Results):]
	output.Skipped = make([]string, 0, len(skipped))
	for _, call := range skipped {
		output.Skipped = append(output.Skipped, call.Tool)
	}
	return nil, output, nil
}

// batched decodes one call's arguments and runs its handler, reporting a
// tool-level failure as an error so the batch stops where the call did.
func batched[In, Out any](
	ctx context.Context,
	request *mcp.CallToolRequest,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
	arguments json.RawMessage,
	schema *jsonschema.Resolved,
) (any, error) {
	// The SDK applies a tool's schema only to a call it dispatches itself, so
	// a batch has to apply it here or a batched call would accept what the
	// same call alone is refused: a value outside a closed set, a number where
	// a string goes.
	if schema != nil {
		if err := schema.Validate(argumentValue(arguments)); err != nil {
			return nil, fmt.Errorf("arguments are not what this tool takes: %w", err)
		}
	}
	// Decoded strictly as well, so a tool whose schema would not resolve still
	// refuses a misspelled field rather than dropping it and running on
	// defaults, which reports success for something nobody asked for.
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

// argumentValue is one call's arguments as a schema validator reads them.
// Absent arguments are an empty object rather than null, because a tool that
// needs none is called in a batch by naming it and nothing else.
func argumentValue(arguments json.RawMessage) any {
	var value any
	if len(arguments) == 0 || json.Unmarshal(arguments, &value) != nil || value == nil {
		return map[string]any{}
	}
	return value
}

// batchErrorText reports what a failing call said, so a batch's report carries
// the same message the call would have given on its own.
func batchErrorText(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && text.Text != "" {
			return text.Text
		}
	}
	return "the call failed without a message"
}

// addBatchTools advertises the tools that run several calls in one request.
//
// They are registered last so that every other tool is already in the dispatch
// table, and they keep themselves out of it: a batch inside a batch would nest
// a stop-at-first-failure inside another one, where the outer report would say
// a call failed and not which of the inner ones did.
func addBatchTools(server *mcp.Server, t *tools) {
	t.batchable = false
	register(server, t, &mcp.Tool{
		Name:        "call_readonly_tools_batch",
		Annotations: readOnly("Batch of Reading Tools"),
		Description: "Run several reading tools in one request, in order. " +
			"Refuses the whole batch if any call would change tmux, so a batch " +
			"believed to be read-only never alters a session. Stops at the first " +
			"failure, and names the calls it skipped.",
	}, t.callReadOnlyToolsBatch)
	register(server, t, &mcp.Tool{
		Name:        "call_mutating_tools_batch",
		Annotations: mutating("Batch of Changing Tools"),
		Description: "Run several tools in one request, in order, including ones " +
			"that change tmux. Laying out a window is a split, a resize, and a " +
			"command per pane; this makes it one call. Stops at the first " +
			"failure, and what already ran stays, because tmux has no " +
			"transaction; the reply names the calls it skipped.",
	}, t.callMutatingToolsBatch)
	register(server, t, &mcp.Tool{
		Name:        "call_destructive_tools_batch",
		Annotations: destructive("Batch of Ending Tools"),
		Description: "Run several tools in one request, in order, including the " +
			"ones that end something. Stops at the first failure, and nothing it " +
			"already ended comes back; the reply names the calls it skipped.",
	}, t.callDestructiveToolsBatch)
}
