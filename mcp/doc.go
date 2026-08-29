// Package mcp exposes one tmux server to Model Context Protocol clients.
//
// This module is separate so the core tmux module keeps its standard-library-only
// dependency graph. Import aliases distinguish it from the MCP SDK:
//
//	import (
//		tmuxmcp "github.com/libtmux/libtmux-go/mcp"
//		sdk "github.com/modelcontextprotocol/go-sdk/mcp"
//	)
//
// The MCP server requires tmux 3.2a or newer. [NewServer] validates a target
// without performing I/O, then returns an [Instance] that owns its sessions
// and background resources. Connect checks the tmux version before opening the
// transport, applies handshake ordering, and gives each client isolated
// consent, subscriptions, waits, and detached jobs. Custom transports must use
// [AssumeResponseCommit] to assert that a successful write commits one response.
// The server admits at most 32 unsettled calls per client and 128 per instance
// before the SDK queue. Exceeding either limit closes only that client session;
// [ServerSession.Wait] then matches [ErrRequestCapacity]. Close the instance
// after serving. [Run] provides the same lifecycle for the command-line server.
//
// On tmux 3.2a through 3.5, destroying the session that supplied the runtime's
// control client follows that session's detach-on-destroy policy and may end
// the runtime. tmux 3.6 or newer can move the client to another remaining
// session.
//
// # Target and security boundary
//
// Every tool addresses the tmux server selected at construction. Tool arguments
// cannot change its socket, and identifiers that look like flags or foreign
// targets are rejected.
//
// That restriction is not a sandbox. Tools that write to panes or build
// workspaces can execute commands with the tmux user's authority. Read tools
// can disclose pane contents. Limit exposed tools and authenticate remote
// clients; see the module security documentation for the complete boundary.
//
// Safety profiles withhold tools during registration. They classify tmux
// operations, not filesystem authority or data sensitivity. A tool that sends
// input can run a destructive shell command even when it does not kill a tmux
// object.
//
// Writes to the caller pane require MCP elicitation. A decline or a client
// without elicitation fails the call. The guard prevents accidental writes to
// the conversation pane; it does not reduce the authority of other write tools.
//
// # Pane I/O
//
// run_command stages a POSIX-compatible command as a file for the pane shell
// to source, avoiding line-editor interpretation. Known incompatible shells
// fail before delivery. It returns the command's output and exit status;
// detached commands return a job handle for later collection with get_job.
//
// wait_for_text uses control notifications to observe output and advances a
// gap-checked pane cursor, so output between attachment and observation is not
// lost.
// capture_since returns only output written after an opaque cursor from an
// earlier call. capture_pane remains the direct visible-screen read.
//
// Waits have operator-defined ceilings. A request above the ceiling is shortened
// and reports the applied limit. Pane content and other potentially large
// results are bounded and report omitted data.
//
// List criteria are matched against a materialized snapshot rather than passed
// to tmux as format filters, which can execute shell commands through #(...).
// Read collections treat an unreachable server as empty; reads targeting one
// object and all mutations return the failure.
//
// # Tool discovery
//
// The server publishes schemas, annotations, prompts, resources, and completion
// data. TOOLS.md is generated from the registered tools and is the detailed
// reference for choosing arguments and interpreting results.
package mcp

//go:generate go run ./internal/generate/toolsref -output TOOLS.md
