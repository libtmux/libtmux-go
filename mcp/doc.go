// Package mcp exposes one startup-pinned tmux server to Model Context Protocol
// clients.
//
// The module is separate so the core tmux module keeps its small dependency
// graph. Import aliases distinguish it from the MCP SDK:
//
//	import (
//		tmuxmcp "github.com/libtmux/libtmux-go/mcp"
//		sdk "github.com/modelcontextprotocol/go-sdk/mcp"
//	)
//
// Every registered tool comes from one immutable native manifest that also
// drives startup selection, schemas, annotations, disclosure metadata, and the
// static tmux://capabilities resource.
//
// # Selection
//
// The four unordered toolsets are inspect, manage, execute, and teardown.
// LIBTMUX_TOOLSETS selects groups, LIBTMUX_TOOLS includes exact names, and
// LIBTMUX_EXCLUDE_TOOLS removes exact names after inclusion. Invalid startup
// policy fails before tmux opens. Tool arguments cannot change the selected
// socket, and identifiers that look like flags or foreign targets are rejected.
//
// # Lifecycle and transport
//
// The server requires tmux 3.2a or newer. [NewServer] validates a target
// without performing I/O, then returns an [Instance] that owns its sessions
// and runtime resources. [Instance.Connect] checks the tmux version before
// opening the transport and applies handshake ordering. Custom transports must
// use [AssumeResponseCommit] to assert that every successful write commits one
// response.
//
// The server admits at most 32 unsettled calls per client and 128 per instance
// before the SDK queue. Exceeding either limit closes only the offending client
// session; [ServerSession.Wait] then matches [ErrRequestCapacity]. Close the
// instance after serving. [Instance.Run] provides the same lifecycle for a
// supplied transport; [Run] serves it over stdio.
//
// On tmux 3.2a through 3.5, destroying the session that supplied the runtime's
// control client follows that session's detach-on-destroy policy and may end
// the runtime. tmux 3.6 or newer can move the client to another remaining
// session.
//
// # Target and security boundary
//
// Tool selection is not a sandbox. Pane and environment reads can disclose
// secrets or untrusted content, while pane-input operations can execute with
// the tmux user's authority. Each tool publishes its process reach, tmux
// effects, output classes, input literalization, nested authority, and
// conservative MCP annotations. The complete input-sink table stays internal.
//
// Writes to the caller pane require MCP elicitation. A decline or a client
// without elicitation fails the call. This prevents accidental writes to the
// conversation pane; it does not reduce the authority of other write tools.
//
// # Pane I/O
//
// run_shell_command stages a POSIX-compatible command as a private file for a
// pane shell to source, then returns bounded output and the exit status. It
// requires one effectively synchronized pane before setup and rechecks that
// singleton before dispatch. Send paths refuse dead or modal configured
// members before caller confirmation. Paste text is target-only; optional
// Enter has separate configured membership. Reported pane IDs describe the
// observed preflight, not atomic delivery.
// wait_for_text observes control notifications and advances a gap-checked pane
// cursor, so output between attachment and observation is not lost.
// capture_since returns output written after an opaque cursor. Search and
// pattern work has fixed count, byte, and matching-work ceilings. Bounded
// results report omitted data. capture_pane remains the direct screen read.
//
// Listing filters run against a materialized snapshot rather than tmux format
// filters, which may execute shell commands through #(...). An unreachable
// server is an empty topology for list tools; reads that require a live object
// and all mutations return tmux failures.
//
// # Tool discovery
//
// The static tmux://capabilities resource and each tool's metadata expose the
// startup-frozen public capability report. TOOLS.md is generated from the same
// manifest and documents arguments, results, workflows, and retired names.
package mcp

//go:generate go run ./internal/generate/toolsref -output TOOLS.md
