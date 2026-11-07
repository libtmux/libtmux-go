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
	// Protocol 2026-07-28 deprecates logging, but earlier clients still read it
	// and the SDK keeps each request's level to that request.
	//nolint:staticcheck // SA1019: the SDK has no other way to send a log message
	_ = request.Session.Log(ctx, &mcp.LoggingMessageParams{
		Logger: "libtmux",
		Level:  mcp.LoggingLevel(level),
		Data:   data,
	})
}
