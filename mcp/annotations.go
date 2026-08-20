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
// would miss, and that accumulates: splitting twice makes two panes.
func mutating(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  false,
		DestructiveHint: new(false),
		OpenWorldHint:   new(false),
	}
}

// settling describes a tool that changes tmux to a state rather than by a
// step: renaming a window twice leaves one window with the second name.
//
// The distinction is what a client needs after a timeout. A call that may or
// may not have landed can be retried when repeating it cannot compound, and a
// client with no hint has to treat every change as though it might. Only tools
// whose every argument lands in the same state qualify — resize_pane takes a
// zoom that toggles and enter_copy_mode takes a scroll that goes further each
// time, so neither is one of these.
func settling(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  true,
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
