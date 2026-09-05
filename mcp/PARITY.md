# Against the shared capability contract

There are several MCP servers for tmux under the libtmux name. They serve the
same tmux and answer to the same clients, so a person choosing a port needs the
portable contract before language-specific implementation detail.

This is not the root [PARITY.md](../PARITY.md). That file compares Go library
symbols with the Python library. This file records how the Go MCP server maps
the shared 47-tool capability model into its native SDK and core.

## The surface, counted

| Surface | Go MCP |
| --- | --- |
| Public tools | 47 |
| Unordered toolsets | `inspect`, `manage`, `execute`, `teardown` |
| Static resources | one, `tmux://capabilities` |
| Dynamic resources and templates | none |
| Prompts and completion routes | none |
| Host-command tools | none |
| Read-batch nested operations | 16 before named exclusions |

The count is generated from one native manifest. It is not a second list
maintained in this document.

## The tools

One immutable Go `toolDefinition` registry owns every public tool's name,
title, controlled description, toolset, process reach, nonempty direct effect
set, output classes, secret and untrusted-output flags, future-input amplifier,
four MCP annotations, native input and output schemas, schema-keyed input
sinks, tmux-format controls, nested authority, and handler.

Registration, list-tools output, dispatch, startup filtering, generated
[TOOLS.md](TOOLS.md), per-tool wire metadata, and capability reporting all read
that registry. Missing, extra, or duplicate registrations fail construction.

The schemas use the shared snake_case input names. A schema field and its input
sink set must match exactly. A value that reaches a tmux format-expanding
position is either literalized by doubling `#` once or constrained by the
declared `validated-variable-name` control.

## Knowing its own pane

The server works out whether it runs in a pane of the tmux it controls from
`TMUX`, `TMUX_PANE`, and a process-tree fallback. It compares socket identity as
well as pane id because two servers can both have a `%1`.

`get_server_info` reports the relationship and pane summaries carry `isCaller`.
Writing to or ending the caller pane asks through MCP elicitation and fails
closed when the client cannot ask. The guard is additional to capability
selection; it never makes an unselected teardown tool reachable.

## Watching and waiting

`capture_since` returns an opaque cursor and only later pane output. The cursor
binds the pane and process generation, and `linesMissed` reports when tmux has
discarded history needed for a complete continuation.

`wait_for_text` and `search_panes` bound pattern count and size before regular
expression compilation. Search work has fixed aggregate ceilings of 200 panes,
20,000 lines, 1,000,000 bytes, and five seconds. A wait also has a startup-frozen
duration ceiling, 300 seconds by default.

`run_shell_command` uses a pane-local framed completion record and returns a
real exit status plus bounded output. It has no detached mode or background
handle. A long-running process remains visible and inspectable in its pane.

## What the schemas say

Every direct and nested call goes through the same native input validator and
handler binding. Arrays and enums are constrained in the generated schema, and
unknown fields fail rather than being ignored.

The capability row is published at
`_meta["com.git-pull.libtmux-mcp/capability"]` on each listed tool. The
corresponding row in `tmux://capabilities` is byte-equivalent after JSON
normalization. Conservative annotations are identical on every route because
an existing or user-configured tmux server can add hooks and commands the MCP
process did not create.

The generated tool reference prints the same native input/output schemas and
capability fields. It is a projection of the registry, not an independent
authority.

## Read-batch authority

`call_read_tools_batch` may invoke every inspect tool except itself and the
self-bounded `wait_for_text`: 16 operations in the full surface. Named
exclusions prune both the schema enum and dispatch authority, even when an
inner operation is retained only for aggregate use and is not separately
advertised.

A zero-authority batch remains advertised with direct `observe` effect, empty
aggregate output classes, and an unsatisfiable operations schema. It cannot be
used as a stringly typed escape hatch.

Calls execute serially. Each row reports index, tool, success, error, full
retained nested envelope, and `resultTruncated`. The aggregate reports
`onError`, succeeded and failed counts, `stoppedAt`, `truncated`, and
`truncatedBytes`; its complete JSON-RPC response is at most 1,000,000 bytes.

## Addressing the server

The server pins one socket for the process. `LIBTMUX_SOCKET` selects a name and
`LIBTMUX_SOCKET_PATH` selects an absolute path; they are mutually exclusive.
`LIBTMUX_TMUX_CONFIG` is a nonempty absolute path to a user configuration.
There is no per-call socket argument and no public server-discovery tool.

With no socket or config selector, the CLI uses the product-dedicated
`libtmux-mcp` socket and the bundled minimal configuration. Startup passes a
random owner nonce into that configuration, starts the daemon, reads back a
global marker, and removes the nonce from tmux's environment. Only a matching
marker authenticates that this process created the daemon and permits teardown
in the default selection. A racing or pre-existing daemon cannot inherit that
claim.

An explicit socket name, explicit path, user configuration, existing daemon,
or unknown provenance defaults to `inspect,manage,execute`. Teardown requires
explicit toolset or named-tool selection, with exclusions still winning.

## Limiting what a client can do

`LIBTMUX_TOOLSETS` selects an unordered subset. An entirely empty value is the
valid zero subset; once nonempty, leading, trailing, or interior empty tokens
are errors. `LIBTMUX_TOOLS` includes exact names and
`LIBTMUX_EXCLUDE_TOOLS` removes exact names last.

Unknown toolsets, unknown tool names, relative paths, and a present retired
`LIBTMUX_SAFETY`, `LIBTMUX_MCP_CAPABILITIES`, or
`LIBTMUX_MCP_PROMPTS_AS_TOOLS` variable fail startup before tmux opens. The
surface is frozen after construction, so later environment changes cannot widen
it.

Selection is not an operating-system sandbox. Pane commands and input run with
the tmux user's permissions. The manifest therefore distinguishes
`configured-process`, `pane-input`, and `pane-command` reach, marks secret and
untrusted outputs separately, and prohibits host-command reach.

## Retired surface context

Earlier Go releases exposed ordered `readonly`, `mutating`, and `destructive`
tiers, a parallel capability allowlist, prompts, dynamic hierarchy resources,
background jobs, generic setters, raw tmux formats, buffers, pipe commands,
server discovery, and broad mutation batches. Those were useful experiments,
but together they formed several overlapping authorities and did not match the
portable 47-tool contract.

The migration keeps the useful workflows while narrowing the routes:

- `run_command` became bounded `run_shell_command`; `get_job` and detached
  handles were removed in favor of pane-visible state and capture cursors.
- `call_readonly_tools_batch` became the typed, exact-authority
  `call_read_tools_batch`; mutating and destructive generic batches were
  removed.
- Generic `set_option`, `set_environment`, and `display_message` routes became
  constrained named settings and validated tmux variables.
- Public buffer operations collapsed into the ephemeral internal buffer used
  by `paste_text`; pipe and server-wide teardown routes were removed.
- Dynamic topology/content resources became ordinary selected tools. The one
  static resource now explains the effective trust boundary instead of
  bypassing it.

See the [retired tool mapping](TOOLS.md#retired-tool-mapping) for each old name.

| Earlier alpha route | Portable route | What remains available |
| --- | --- | --- |
| `get_pane` | `get_pane_info` | Typed metadata for one pane |
| `server_info` and `whoami` | `get_server_info` plus pane rows | Socket and caller-pane identity |
| `observe` | `snapshot_pane` then `capture_since` | Incremental output with an opaque cursor |
| `run_command` and `get_job` | `run_shell_command` plus pane capture | Completion status and long-running pane visibility |
| `display_message` | `get_tmux_variables` | Validated variable lookup without free-form formats |
| `new_session` and `new_window` | `create_session` and `create_window` | Commandless configured-process creation |
| generic option writers | named setters or `run_shell_command` | Common typed changes and an explicit execution route |
| public buffer tools | `paste_text` | Literal text delivery with ephemeral cleanup |
| dynamic pane resources | inspect tools | Approval-bearing topology and content reads |
| prompt recipes | documented call sequences | The workflows without a second protocol surface |

This table is historical migration context, not an alternate inventory. The
generated 47-tool list and each startup-filtered capability report remain the
authoritative current surfaces.

## Testing the server

Focused manifest tests enumerate the exact 47 tools, all 16 toolset subsets,
named include/exclude precedence, zero-authority batch behavior, schema/sink
equality, wire metadata/resource parity, the complete 1,000,000-byte JSON-RPC
batch-response cap, and authenticated startup ownership. Live tests use isolated
socket names and verify that a losing launch does not claim teardown and that
the nonce is not visible through `show-environment`.

The generated reference has a check mode, so CI can fail when the checked-in
schemas or capability rows drift from what the server advertises.
