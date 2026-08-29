package mcp

import mcp "github.com/modelcontextprotocol/go-sdk/mcp"

// Annotation helpers return fresh values so tools cannot mutate shared hints.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		IdempotentHint:  true,
		DestructiveHint: new(false),
		OpenWorldHint:   new(false),
	}
}

// mutating marks a non-idempotent change not annotated destructive.
func mutating(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  false,
		DestructiveHint: new(false),
		OpenWorldHint:   new(false),
	}
}

// settling marks changes safe to retry because every argument converges on one state.
func settling(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  true,
		DestructiveHint: new(false),
		OpenWorldHint:   new(false),
	}
}

// destructive marks tools that may irreversibly end tmux objects.
func destructive(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  false,
		DestructiveHint: new(true),
		OpenWorldHint:   new(false),
	}
}
