package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Batches dispatch only the aggregate's exclusion-pruned nested authority.

type dispatcher struct {
	// The originating request retains the session needed for caller-pane elicitation.
	call func(context.Context, *mcp.CallToolRequest, json.RawMessage) (*mcp.CallToolResult, error)
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
	Index           int     `json:"index"`
	Tool            string  `json:"tool"`
	Success         bool    `json:"success"`
	Error           *string `json:"error"`
	Result          any     `json:"result"`
	ResultTruncated bool    `json:"resultTruncated"`
}

type batchOutput struct {
	Results        []batchResult `json:"results"`
	OnError        string        `json:"onError"`
	Succeeded      int           `json:"succeeded"`
	Failed         int           `json:"failed"`
	StoppedAt      *int          `json:"stoppedAt"`
	Truncated      bool          `json:"truncated"`
	TruncatedBytes int           `json:"truncatedBytes"`
}

const (
	onErrorStop                 = "stop"
	onErrorContinue             = "continue"
	readBatchStructuredMaxBytes = 950_000
	readBatchWireMaxBytes       = 1_000_000
	readBatchErrorMaxBytes      = 4_096
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

// runReadBatch dispatches only the aggregate's startup-frozen, exclusion-
// pruned nested authority. Each dispatcher applies the direct tool's typed
// schema before invoking its handler.
func (t *tools) runReadBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	if len(input.Calls) == 0 {
		return nil, batchOutput{}, errors.New("a batch needs at least one call")
	}
	if len(input.Calls) > 16 {
		return nil, batchOutput{}, errors.New("a read batch accepts at most sixteen calls")
	}
	aggregate, served := t.surface.byName["call_read_tools_batch"]
	if !served {
		return nil, batchOutput{}, errors.New("call_read_tools_batch is not served")
	}
	allowed := make(map[string]bool, len(aggregate.nestedAuthority))
	for _, name := range aggregate.nestedAuthority {
		allowed[name] = true
	}
	for _, call := range input.Calls {
		if !allowed[call.Tool] {
			return nil, batchOutput{}, fmt.Errorf(
				"%q is not in this batch's nested authority, so this batch ran nothing",
				call.Tool,
			)
		}
		if _, served := t.dispatchers[call.Tool]; !served {
			return nil, batchOutput{}, fmt.Errorf(
				"%q has no nested dispatcher, so this batch ran nothing",
				call.Tool,
			)
		}
	}
	return t.executeBatch(ctx, request, input)
}

func (t *tools) executeBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input batchInput,
) (*mcp.CallToolResult, batchOutput, error) {
	onError, err := resolveOnError(input.OnError)
	if err != nil {
		return nil, batchOutput{}, err
	}
	output := batchOutput{
		Results: make([]batchResult, 0, len(input.Calls)),
		OnError: onError,
	}
	for index, call := range input.Calls {
		var envelope *mcp.CallToolResult
		encodedArguments, err := json.Marshal(call.Arguments)
		if err != nil {
			envelope = nestedErrorResult(errors.New("arguments could not be encoded: " + err.Error()))
		} else {
			envelope, err = t.dispatchers[call.Tool].call(ctx, request, encodedArguments)
			callErr := err
			if callErr != nil {
				envelope = nestedErrorResult(callErr)
			}
		}
		decoded, err := nestedEnvelope(envelope)
		if err != nil {
			decoded, _ = nestedEnvelope(nestedErrorResult(
				fmt.Errorf("result could not be encoded: %w", err),
			))
		}
		row := batchResult{
			Index: index, Tool: call.Tool, Success: !envelope.IsError, Result: decoded,
		}
		if envelope.IsError {
			message := boundedBatchError(batchErrorText(envelope))
			row.Error = &message
			output.Failed++
		} else {
			output.Succeeded++
		}
		output.Results = append(output.Results, row)
		if err := enforceReadBatchLimit(&output); err != nil {
			return nil, batchOutput{}, err
		}
		if envelope.IsError && onError == onErrorStop {
			stoppedAt := index
			output.StoppedAt = &stoppedAt
			break
		}
	}
	summary := fmt.Sprintf(
		"Read batch completed: %d succeeded, %d failed.",
		output.Succeeded,
		output.Failed,
	)
	if output.Truncated {
		summary = fmt.Sprintf(
			"Read batch completed: %d succeeded, %d failed; nested result bytes were truncated.",
			output.Succeeded,
			output.Failed,
		)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, output, nil
}

func enforceReadBatchLimit(output *batchOutput) error {
	for {
		encoded, err := json.Marshal(output)
		if err != nil {
			return fmt.Errorf("measure batch output: %w", err)
		}
		if len(encoded) <= readBatchStructuredMaxBytes {
			return nil
		}
		candidate := -1
		candidateSize := -1
		for index := range output.Results {
			if output.Results[index].Result == nil {
				continue
			}
			encodedResult, err := json.Marshal(output.Results[index].Result)
			if err != nil {
				return fmt.Errorf("measure nested batch result: %w", err)
			}
			if len(encodedResult) > candidateSize {
				candidate = index
				candidateSize = len(encodedResult)
			}
		}
		if candidate < 0 {
			return errors.New("batch metadata exceeds its output limit")
		}
		output.Results[candidate].Result = nil
		output.Results[candidate].ResultTruncated = true
		output.Truncated = true
		if candidateSize > len("null") {
			output.TruncatedBytes += candidateSize - len("null")
		}
	}
}

func boundedBatchError(message string) string {
	if len(message) <= readBatchErrorMaxBytes {
		return message
	}
	message = message[:readBatchErrorMaxBytes-len("...")]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message + "..."
}

func nestedErrorResult(err error) *mcp.CallToolResult {
	result := &mcp.CallToolResult{}
	result.SetError(err)
	return result
}

func nestedEnvelope(result *mcp.CallToolResult) (map[string]any, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func batched[In, Out any](
	ctx context.Context,
	request *mcp.CallToolRequest,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
	arguments json.RawMessage,
	inputSchema *jsonschema.Resolved,
	outputSchema *jsonschema.Resolved,
) (*mcp.CallToolResult, error) {
	// The SDK validates direct calls only; batches apply the same schema here.
	if inputSchema != nil {
		if err := inputSchema.Validate(argumentValue(arguments)); err != nil {
			return nestedErrorResult(fmt.Errorf("arguments are not what this tool takes: %w", err)), nil
		}
	}
	// Strict decoding still rejects unknown fields if schema resolution fails.
	var input In
	if len(arguments) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(arguments))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nestedErrorResult(fmt.Errorf("arguments are not what this tool takes: %w", err)), nil
		}
	}
	result, output, err := handler(ctx, request, input)
	if err != nil {
		return nestedErrorResult(err), nil
	}
	if result == nil {
		result = &mcp.CallToolResult{}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("encode nested output: %w", err)
	}
	if outputSchema != nil {
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, fmt.Errorf("decode nested output: %w", err)
		}
		if err := outputSchema.Validate(value); err != nil {
			return nil, fmt.Errorf("nested output is not what this tool returns: %w", err)
		}
	}
	structured := json.RawMessage(encoded)
	result.StructuredContent = structured
	if result.Content == nil {
		result.Content = []mcp.Content{&mcp.TextContent{Text: string(structured)}}
	}
	return result, nil
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
