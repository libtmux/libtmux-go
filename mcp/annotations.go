package mcp

import mcp "github.com/modelcontextprotocol/go-sdk/mcp"

// Tool annotations describe what a call does to tmux, so a client can decide
// how to treat it without reading the description.
//
// A client that reads hints can approve a listing without asking, confirm a
// split, and refuse a kill outright. Prose cannot be acted on that way, and a
// server that leaves the hints off makes every tool look equally dangerous.
//
// Each is returned as a fresh value rather than shared, because the SDK takes
// a pointer and a shared one could be edited through any tool holding it.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		IdempotentHint:  true,
		DestructiveHint: new(false),
		OpenWorldHint:   new(false),
	}
}

// mutating describes a tool that changes tmux without ending anything a caller
// would miss. These are not idempotent: splitting twice makes two panes.
func mutating(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  false,
		DestructiveHint: new(false),
		OpenWorldHint:   new(false),
	}
}

// destructive describes a tool that ends something: a session and every
// process in it, which no later call brings back.
func destructive(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  false,
		DestructiveHint: new(true),
		OpenWorldHint:   new(false),
	}
}
