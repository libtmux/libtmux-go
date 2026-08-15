// Package mcp exposes a tmux server to Model Context Protocol clients.
//
// It is a consumer of the tmux module rather than part of it: the tmux module
// takes no runtime dependency, while speaking MCP needs one, so this lives in
// its own module.
//
// This package is named mcp and so is the SDK's, which [NewServer] returns a
// value from. A file using both must rename one of them, and naming both is
// what using this package looks like:
//
//	import (
//		sdk "github.com/modelcontextprotocol/go-sdk/mcp"
//		tmuxmcp "github.com/libtmux/libtmux-go/mcp"
//	)
//
// Every tool addresses one tmux server chosen when the MCP server is
// constructed. A client cannot retarget a tool: identifiers are validated, so a
// pane id of "-L" or a session target of "other:0" is refused rather than
// passed to tmux as a flag or a foreign target.
//
// That is a limit on targeting, not a sandbox. build_workspace and send_keys
// run commands inside a pane, and a shell in that pane can do whatever the user
// running tmux can, including "tmux -L other kill-server" against a different
// socket. Treat a client with access to those two tools as able to run
// arbitrary commands as that user, and do not expose them to an untrusted
// caller on the strength of the server selection alone.
//
// Waiting is a tool rather than a loop. run_command runs a command and reports
// its exit status; wait_for_text waits for a pane to write something. Both read
// tmux rather than a screen, so a shell's echo of a command is not mistaken for
// its output, and both cost one call where reading the pane until it looks
// right costs a round trip per look.
//
// Observing repeatedly is capture_since, which returns what a pane wrote after
// a cursor a previous call handed out. Reading a pane every turn otherwise
// costs the whole screen every turn, most of it already read.
//
// Everything a tool returns from a pane is bounded, because a pane's
// scrollback is measured against a terminal's memory and the reply is measured
// against a caller's context. A bound that was hit is reported rather than
// hidden: every such reply carries what it dropped.
//
// Read tools use the tmux module's lenient default, so an unreachable server
// reads as an empty result; capture_pane is an exception, because it addresses
// one pane and cannot report a missing one as emptiness. Write tools use strict
// errors, because a mutation that silently did nothing is worse than a reported
// failure.
//
// # Which tool
//
// Reading what a pane holds:
//   - reading the same pane again as it changes: capture_since, which returns
//     what was written since the cursor the last call gave you
//   - one look at a pane you can name: capture_pane
//   - contents and state together: snapshot_pane
//   - you do not know which pane: search_panes, which returns the lines that
//     matched as well as the panes
//   - state without contents, such as whether the process exited:
//     get_pane_info
//   - anything else tmux knows: display_message, which expands a tmux format
//
// Waiting, rather than looking again:
//   - a command you run, its exit status and its output: run_command
//   - output you did not author, such as a service announcing itself:
//     wait_for_text, with stop set to the failure markers you already know
//   - anything that signals a tmux channel: wait_for_channel
//
// Putting something into a pane, in order of how much tmux reads:
//   - a command line, with tmux key names honoured: send_keys
//   - key names in order with no Enter, to drive a pager or an editor:
//     send_keys_batch
//   - text taken literally, for anything you did not write by hand: paste_text
//
// Making and arranging: create_session, create_window, split_window,
// build_workspace; then select_layout, resize_pane, swap_pane, select_pane and
// select_window to arrange it, and rename_window and set_pane_title to label
// it. call_mutating_tools_batch does a run of these in one request.
//
// Ending things: kill_pane, kill_window, kill_session and kill_server, none of
// which any later call undoes.
//
// When a pane makes no sense and its contents do not explain it, the reason is
// usually a setting: show_option, show_hooks, show_environment.
//
// # Finding your own pane
//
// Every pane this server reports carries isCaller, and get_server_info answers
// the same question directly. A pane it reports as this one is the terminal
// the conversation is happening through, so acting on it acts on the
// conversation. See _examples/agent-workflow for a program that finds its own
// pane, splits it, runs a command in the new one and reports the layout.
package mcp
