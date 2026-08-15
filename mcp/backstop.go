package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A last cap on what any tool can return, whatever it meant to.
//
// Every tool that reads pane text bounds its own reply and says what it
// dropped, which is the right place for it: the tool knows which end matters
// and can report the loss in its own shape. This is not that. This is the
// backstop for the tool that forgets — a new one added later, or an old one
// meeting a pane nobody imagined — because the cost of forgetting is paid by
// whoever is talking to the model, in a context window they cannot get back.
//
// It is deliberately blunt. It does not try to trim intelligently or to
// describe what it removed in the tool's own vocabulary, because it does not
// know the shape it is holding. It replaces the reply with a refusal that says
// how big the answer was and which tool produced it, which is enough to fix
// the tool.
//
// Reaching it is a defect. A reply that hits this cap is one a tool should
// have bounded, so the message says so rather than presenting it as a limit
// the caller should work around.
const backstopMaxBytes = 4 * ceilingMaxBytes

// backstop caps the serialized size of every tool result.
func backstop() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(
			ctx context.Context,
			method string,
			request mcp.Request,
		) (mcp.Result, error) {
			result, err := next(ctx, method, request)
			if err != nil || method != "tools/call" {
				return result, err
			}
			call, ok := result.(*mcp.CallToolResult)
			if !ok {
				return result, err
			}
			size := resultSize(call)
			if size <= backstopMaxBytes {
				return result, err
			}
			name := "a tool"
			if params, ok := request.GetParams().(*mcp.CallToolParamsRaw); ok && params.Name != "" {
				name = params.Name
			}
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
					"%s tried to return %d bytes, past the %d this server will send. "+
						"That is a fault in the tool rather than in the request: every "+
						"tool that returns pane text is supposed to bound its own reply "+
						"and say what it dropped. Ask for less, and report it.",
					name, size, backstopMaxBytes)}},
			}, nil
		}
	}
}

// resultSize is how much a result would occupy on the wire.
//
// Measured by serializing rather than by adding up what is known, because the
// point is to bound what actually goes out and a shape this does not recognise
// is exactly the shape it exists for. A result that cannot be serialized is
// reported as fitting: the SDK is about to fail on it anyway, and failing here
// first would replace a precise error with a vague one.
func resultSize(result *mcp.CallToolResult) int {
	encoded, err := json.Marshal(result)
	if err != nil {
		return 0
	}
	return len(encoded)
}
