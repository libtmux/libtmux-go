package mcp

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// logToClient sends one log message to the client that made the call.
//
// This server advertises the logging capability, and a capability advertised
// and never used is a promise to a client that it may set a level and hear
// nothing. What belongs here is the context a tool result has no room for: why
// a wait ended with nothing, what a pane was doing instead. The result says
// what happened; the log says why.
//
// The SDK sends nothing until a client sets a level, so this costs a client
// that did not ask for logs precisely nothing.
//
// MCP deprecated logging in protocol version 2026-07-28, keeping it working for
// at least twelve months. It stays until clients stop reading it: a client that
// sets a level today still hears why a wait ended with nothing, and dropping
// that quietly is worse than outliving the deprecation by a release.
func logToClient(
	ctx context.Context,
	request *mcp.CallToolRequest,
	level string,
	data map[string]any,
) {
	if request == nil || request.Session == nil {
		// A call made from a batch has no request of its own. Losing a log
		// line is not worth failing the call it describes.
		return
	}
	//nolint:staticcheck // SA1019: deprecated with a twelve-month window; see above.
	_ = request.Session.Log(ctx, &mcp.LoggingMessageParams{
		Logger: "libtmux",
		Level:  mcp.LoggingLevel(level), //nolint:staticcheck // SA1019: as above.
		Data:   data,
	})
}
