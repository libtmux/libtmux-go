package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// backstopMaxBytes catches tools that miss their own result bounds. Reaching it
// is a server defect, not a caller limit.
const backstopMaxBytes = 4 * ceilingMaxBytes

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

// resultSize measures the serialized reply. Marshal failures remain for the SDK.
func resultSize(result *mcp.CallToolResult) int {
	encoded, err := json.Marshal(result)
	if err != nil {
		return 0
	}
	return len(encoded)
}
