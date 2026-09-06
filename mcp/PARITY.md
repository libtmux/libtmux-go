# Against the Python server

There are two MCP servers for tmux under the libtmux name: this one, and
[libtmux-mcp](https://github.com/libtmux/libtmux-mcp) in Python. They serve the
same tmux and answer to the same clients, so a person choosing between them, or
running both, wants to know where they differ.

This is not the root [PARITY.md](../PARITY.md). That file compares Go library
symbols with the Python library. This file compares two MCP servers. Neither is
a port of the other, and this comparison is not an API promise.

Every count below was measured over stdio JSON-RPC from this branch and Python
0.1.0a21 with their full advertised surfaces. Both servers were driven by the
same raw client; invalid calls were sent rather than inferred from schemas.

## The surface, counted

| Wire surface | Go | Python |
| --- | ---: | ---: |
| Tools | 45 | 54 |
| Argument fields across tool schemas | 103 | 289 |
| Arguments published with `enum` | 7 | 3 |
| Tools carrying an output schema | 45 | 54 |
| Tools carrying annotations | 45 | 54 |
| Described argument fields | 103 | 287 |
| Collections published as null-or-array | 0 | 0 |
| Prompts | 0 | 4 |
| Listed resources, and templates | 1 and 0 | 0 and 6 |
| Server-instruction characters | 144 | 1,847 |

The Go handshake declares logging, resources, and tools. The Python handshake
also declares prompts, experimental data, and UI extensions. Both declare
`listChanged` for tools. Go declares it for its one static resource; Python's
resource capability reports neither subscriptions nor changing lists.

The Go count is generated from one native manifest and grouped into the shared
`inspect`, `manage`, `execute`, and `teardown` toolsets. Its unfiltered
inventory is exactly 18, 14, 9, and 4 tools. A fresh authenticated product-owned
server defaults to all 45; an existing or explicitly selected server defaults
to the 41 non-teardown tools.

## The tools

Thirty-nine tool names are common to both measured servers. Six are Go-only:

- `clear_pane_scrollback`
- `get_tmux_variables`
- `run_shell_command`
- `set_history_limit`
- `set_mouse_enabled`
- `set_synchronize_panes`

Fifteen are Python-only:

- `clear_pane`, `delete_buffer`, `display_message`, `enter_copy_mode`, and
  `exit_copy_mode`
- `kill_server`, `list_servers`, `load_buffer`, `paste_buffer`, and `pipe_pane`
- `run_command`, `set_environment`, `set_option`, `show_buffer`, and
  `show_hook`

Both current schemas use snake_case input names. Moving between the servers no
longer changes every field's casing, but it still requires inspecting the
chosen tool's schema: a similarly named workflow may use different selectors,
bounds, or result fields.

The different inventories express different boundaries. Python offers broader
buffer, option, environment, server-discovery, copy-mode, and pipe operations.
Go exposes named settings and validated variable lookup, keeps one socket per
process, omits human modal-interface control, and uses one bounded pane-command
route. Go's `run_shell_command` is therefore not a spelling alias for Python's
`run_command`.

Go deliberately omits entering and cancelling copy mode from its MCP manifest.
Capture, snapshot, search, and cursor tools observe terminal content without
taking ownership of an attached person's view or selection. The core tmux
module still exposes `Pane.CopyMode` for applications that own the complete
interaction; library parity does not require MCP parity.

One immutable Go `toolDefinition` registry owns each tool's name, controlled
description, toolset, process reach, effects, output classes, trust flags,
future-input amplification, annotations, schemas, input sinks, format controls,
nested authority, and handler. Registration, dispatch, startup filtering,
per-tool metadata, capability reporting, and generated [TOOLS.md](TOOLS.md) all
read that registry. A missing, extra, or duplicate registration fails server
construction.

The Go capability row appears at
`_meta["com.git-pull.libtmux-mcp/capability"]` on each listed tool and in
`tmux://capabilities`; those rows are byte-equivalent after JSON normalization.
It distinguishes `configured-process`, `pane-input`, and `pane-command` reach,
secret output from untrusted output, and literal input from constrained tmux
variables. Conservative annotations stay identical because an existing tmux
server may have user-configured hooks and commands.

Earlier Go alphas exposed more experimental routes. Their useful workflows now
map to the fixed 45-tool surface:

| Earlier Go route | Current route | What remains available |
| --- | --- | --- |
| `get_pane` | `get_pane_info` | Typed metadata for one pane |
| `server_info` and `whoami` | `get_server_info` plus pane rows | Socket and caller identity |
| `observe` | `snapshot_pane`, then `capture_since` | Incremental output with an opaque cursor |
| `run_command` and `get_job` | `run_shell_command` plus pane capture | Bounded status and long-running pane visibility |
| `display_message` | `get_tmux_variables` | Validated lookup without free-form formats |
| `new_session` and `new_window` | `create_session` and `create_window` | Commandless configured-process creation |
| generic option writers | named setters or `run_shell_command` | Typed common settings and explicit execution |
| public buffer tools | `paste_text` | Literal target-only delivery through a private buffer |
| dynamic pane resources | inspect tools | Startup-selected topology and content reads |
| prompt recipes | documented call sequences | Workflows without another callable surface |

See the [retired tool mapping](TOOLS.md#retired-tool-mapping) for every old
name. It is migration context, not an alternate inventory.

## Knowing its own pane

Both servers use `TMUX` and `TMUX_PANE` to decide whether a pane belongs to the
terminal carrying the MCP process. Both compare socket identity rather than a
pane id alone, because two independent tmux servers can each have `%1`.

Go also falls back to the process tree. A client may start an MCP server with a
curated environment that omits tmux variables even though the process remains a
descendant of a pane. Go finds the pane whose foreground process ancestry
contains its own process. `get_server_info` reports `insideThisServer` and
`callerPaneId`, and pane summaries carry `isCaller`.

The protection differs. Python refuses teardown that would end its caller pane,
window, session, or server. Go protects both teardown and input. Writing to or
ending the caller asks through MCP elicitation and fails closed when the client
cannot ask or the person declines. Confirmation cannot make an unselected tool
reachable.

Go input checks one more human boundary. Before send, paste, or command
dispatch, it freshly lists tmux clients and excludes control-mode clients. A
non-control client makes its active pane attended while viewing a zoomed
window, or every visible pane in that window attended otherwise. Malformed or
incomplete client rows fail closed. Every configured synchronized cohort member
is checked; `paste_text` remains target-only, but its target must still be
unattended. This is protection against racing a human terminal, not an
operating-system sandbox.

## Watching a pane

Both servers offer `capture_since`, which returns only output after an opaque
cursor, plus `wait_for_text` and `wait_for_channel`. A client can wait for
output instead of polling. Go's cursor binds pane and process generation and
reports `linesMissed` when tmux discarded required history.

Neither measured handshake offers resource subscriptions. Python publishes six
dynamic resource templates for hierarchy and content, which a client reads on
demand. Go publishes no dynamic template: topology and terminal content are
tools so one startup selection governs every operation. Its one listed resource,
`tmux://capabilities`, is static disclosure of that selected surface.

Go bounds pattern size before regular-expression compilation. `search_panes`
also caps aggregate panes, lines, bytes, and elapsed work. `wait_for_text` has a
startup-frozen duration ceiling. `run_shell_command` returns a framed exit
status and bounded output for one authored command; long-lived work instead
stays visible in the pane and is observed with a cursor or marker.

## What the schemas say

Both servers publish closed value sets as JSON Schema `enum`, output schemas on
every tool, and arrays rather than null-or-array unions. Both rejected a bad
enum, an unknown field, and an argument of the wrong type when those calls were
sent over the wire.

Go sends every direct and nested operation through the same native validator
and handler binding. A schema field and its declared input sink must match.
Format-expanding tmux inputs are either literalized exactly once or constrained
to a validated variable name.

`call_read_tools_batch` has exact nested authority over 16 inspect operations
before named exclusions. Startup filtering prunes its schema enum and dispatch
authority together. With nothing left, it remains advertised with an
unsatisfiable operations schema rather than becoming a stringly typed escape.
Calls execute serially and retain typed result envelopes under one
1,000,000-byte response ceiling.

Both servers' read batches accept `on_error` as `stop` or `continue` and stop by
default. Stopping suits a dependent sequence; continuing lets independent reads
report all their failures. Go additionally prunes nested batch authority from
the selected startup surface before a call can name an inner tool.

Go's pane-input arrays describe a fresh configured synchronization cohort, not
proven delivery. Source-off means source-only; source-on includes the window's
effective-on panes. Dead, input-disabled, modal, missing, attended, or malformed
members refuse the complete operation. `run_shell_command` requires a singleton
cohort in exactly two full checkpoints; `paste_text` sends its text and optional
newline only through one target's private buffer.

## Being found

Python publishes four prompts: `build_dev_workspace`,
`diagnose_failing_pane`, `interrupt_gracefully`, and `run_and_wait`. Its six
resource templates also make the tmux hierarchy discoverable through resource
pickers, and its longer server instructions carry operating context.

Go publishes no prompt or completion route. It keeps recipes in
[TOOLS.md](TOOLS.md), whose generated reference comes from the same schemas and
capability rows as `tools/list`. This trades protocol-level discovery helpers
for a smaller startup surface and documentation that can be checked for drift.

The static capability resource gives a Go client the effective toolsets, socket
boundary, provenance, selection inputs, and complete per-tool rows. It does not
bypass tool selection to expose pane contents.

## Addressing the hierarchy

Python can select a socket and hierarchy object on individual calls, and its
resource templates carry socket and session or window selectors. One MCP
process can therefore inspect several tmux servers over successive requests.

Go pins one socket for the lifetime of the process. `LIBTMUX_SOCKET` selects a
name and `LIBTMUX_SOCKET_PATH` an absolute path; they are mutually exclusive.
`LIBTMUX_TMUX_CONFIG` selects a nonempty absolute configuration path. There is
no per-call retargeting or public server-discovery tool.

With no selector, Go uses the product-dedicated `libtmux-mcp` socket and bundled
minimal configuration. Startup gives a new daemon a random owner nonce, reads
back a global marker, and removes the nonce from tmux's environment. Only a
matching marker proves process ownership and permits teardown by default. A
racing, inherited, explicitly named, path-selected, or user-configured daemon
cannot inherit that claim.

The trade is operational. Python provides broader per-call reach. Go makes the
operator's startup choice the authority boundary, then addresses sessions,
windows, and panes by their exact tmux ids inside that server.

## Limiting what a client can do

Both servers use the shared unordered `inspect`, `manage`, `execute`, and
`teardown` toolsets selected by `LIBTMUX_TOOLSETS`. Both accept named additions
and exclusions, and both treat a present retired `LIBTMUX_SAFETY` variable as a
fatal startup error rather than silently translating it.

Go freezes selection at construction. An empty `LIBTMUX_TOOLSETS` value is the
valid zero subset; a nonempty value with a leading, trailing, or interior empty
token is malformed. Unknown names, relative socket or configuration paths, and
the retired
`LIBTMUX_MCP_CAPABILITIES` or `LIBTMUX_MCP_PROMPTS_AS_TOOLS` variables fail
before tmux opens. Named exclusions win over additions.

Selection limits advertised protocol authority, not what the tmux user can do.
A pane-command tool runs with that user's permissions, and user configuration
can attach hooks to otherwise narrow tmux operations. The capability metadata
states these boundaries; it does not claim sandboxing or exact downstream
effects.

## Testing the server

The measurements in this document use one raw JSON-RPC driver against both
servers. It counts actual `tools/list`, prompt, resource, initialization,
schema, annotation, and instruction data, then sends invalid enum, unknown
field, and wrong-type calls. That keeps the comparison about observable MCP
behavior rather than source-language conventions.

Python ships Sphinx documentation and project test commands. Go also ships an
agent testing skill under `.agents/skills/testing-the-mcp-server/`; it defines
isolated sockets, raw-wire checks, real-client preflights, and which failure
belongs to the server versus a client adapter.

Go's focused manifest tests enumerate the 45 tools, all 16 toolset subsets,
named include/exclude precedence, zero-authority batch behavior, schema-to-sink
equality, wire metadata/resource parity, response caps, and authenticated
startup ownership. Live tests prove a losing launch cannot claim teardown and
the ownership nonce does not remain in tmux's environment. They use isolated
sockets and multiple supported tmux versions. The generated reference has a
check mode so CI fails when checked-in schemas or capability rows drift from
the advertised server.
