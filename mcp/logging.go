package mcp

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// logToClient sends optional diagnostics to the requesting session. Clients
// receive them only after setting a logging level.
func logToClient(
	ctx context.Context,
	request *mcp.CallToolRequest,
	level string,
	data map[string]any,
) {
	if request == nil || request.Session == nil {
		return
	}
	_ = request.Session.Log(ctx, &mcp.LoggingMessageParams{
		Logger: "libtmux",
		Level:  mcp.LoggingLevel(level),
		Data:   data,
	})
}
