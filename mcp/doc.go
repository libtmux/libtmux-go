// Package mcp exposes a tmux server to Model Context Protocol clients.
//
// It is a consumer of the tmux module rather than part of it: the tmux module
// takes no runtime dependency, while speaking MCP needs one, so this lives in
// its own module.
//
// This package is named mcp and so is the SDK's, whose server [Instance]
// embeds. A file using both must rename one of them, and naming both is what
// using this package looks like:
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
// A command reaches its pane as a file the shell is told to source, rather than
// as keystrokes. A line editor acts on what arrives as keys, so a tab in a
// command would ask it to complete a filename; nothing a caller sends crosses
// it. What comes back is the command's own output, with rows the terminal
// wrapped rejoined and the shell's next prompt left out.
//
// Observing repeatedly is capture_since, which returns what a pane wrote after
// a cursor a previous call handed out. Reading a pane every turn otherwise
// costs the whole screen every turn, most of it already read.
//
// Waiting is also optional. run_command with detach returns a handle as soon
// as the command is typed and get_job collects the exit status and the output
// later, so a build costs what typing it costs rather than what running it
// does. What a wait costs is the caller's turn, since MCP gives a caller no way
// to change its mind once a call is in flight; every wait is therefore bounded
// by a ceiling, and a request above it is shortened and reported rather than
// refused.
//
// Listings narrow. Every one of them used to answer with the whole server,
// which grows with somebody else's tmux rather than with the question; they
// now take criteria, and report the total they selected from. The criteria are
// matched against the snapshot the tool already takes rather than pushed into
// tmux as a -f expression, because a tmux filter is a format and a format
// containing #(...) runs it as a shell command against a long-lived client --
// which this server holds. list_panes with detail full also reports each
// matching pane's process state from that same snapshot, which is how to check
// on several panes without capturing any of them.
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
//     get_pane_info for one pane, or list_panes with detail full for every
//     pane matching some criteria
//   - what a program said in colour rather than in words: capture_pane with
//     styles, which keeps the escape sequences a capture otherwise strips
//   - anything else tmux knows: display_message, which expands a tmux format
//     but is withheld read-only because tmux formats may run shell commands
//
// Waiting, rather than looking again:
//   - a command you run, its exit status and its output: run_command
//   - a command whose answer you do not need yet: run_command with detach,
//     collected later by get_job
//   - output you did not author, such as a service announcing itself:
//     wait_for_text, with stop set to the failure markers you already know
//   - a program whose finishing you cannot predict the words of:
//     wait_for_text with idleSeconds, which ends when the pane goes quiet
//   - anything that signals a tmux channel: wait_for_channel
//
// Putting something into a pane, in order of how much tmux reads:
//   - a command line, with tmux key names honoured: send_keys
//   - key names in order with no Enter, to drive a pager or an editor:
//     send_keys_batch
//   - text taken literally, for anything you did not write by hand: paste_text
//
// Making and arranging: create_session, create_window, split_window,
// build_workspace; then select_layout, resize_pane, swap_pane, move_pane,
// select_pane and select_window to arrange it, and rename_window and
// set_pane_title to label it. move_pane is the one that keeps what a pane is
// running, which killing and splitting again does not.
// call_mutating_tools_batch does a run of these in one request.
//
// Ending things: kill_pane, kill_window, kill_session and kill_server, none of
// which any later call undoes.
//
// When a pane makes no sense and its contents do not explain it, the reason is
// usually a setting: show_option, show_hooks, show_environment, or
// get_server_info with includeMessages for tmux's own log of what it refused.
//
// # Finding your own pane
//
// Every pane this server reports carries isCaller, and get_server_info answers
// the same question directly. A pane it reports as this one is the terminal
// the conversation is happening through, so acting on it acts on the
// conversation.
//
// A write to that pane asks the person first, through MCP elicitation, and a
// decline fails the call. A client that did not declare the capability keeps
// the behaviour it had before, because this is a guard rail rather than a
// boundary: as above, a caller with send_keys can run anything the user can.
//
// See examples/agent-workflow for a program that finds its own pane, splits
// it, runs a command in the new one and reports the layout.
package mcp

//go:generate go run ./internal/generate/toolsref -output TOOLS.md
