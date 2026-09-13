# MCP tool reference

Every tool this server exposes, what a client sends it, and what comes back.
Reference material: read it by search rather than start to finish.

For installing the server and pointing a client at it, see
[README.md](README.md).

**Contents** — [Resources](#resources) · [Tools](#tools) ·
[Recipes](#recipes) · [Gotchas](#gotchas) · [Logs](#logs) ·
[Keeping a record](#keeping-a-record) · [Error handling](#error-handling) ·
[Retired MCP surface](#retired-mcp-surface) · [Every tool](#every-tool)

## Resources

`tmux://capabilities` is the only MCP resource. It is a static disclosure of
the startup-frozen effective surface, not another way to read or mutate tmux.
It reports the selected socket boundary and provenance, the selection inputs,
and one complete capability row for every advertised tool.

The same row appears on that tool at
`_meta["com.git-pull.libtmux-mcp/capability"]`. Registration, schemas,
descriptions, annotations, nested batch authority, this reference, and the
resource all come from the same native manifest.

There are no dynamic resources, prompts, resource subscriptions, or completion
routes. Topology and pane output are tools so the startup selection governs
every callable operation in one place.

## Tools

The four unordered toolsets are `inspect`, `manage`, `execute`, and `teardown`.
`LIBTMUX_TOOLSETS` selects any subset; `LIBTMUX_TOOLS` adds named tools and
`LIBTMUX_EXCLUDE_TOOLS` removes named tools last. An empty `LIBTMUX_TOOLSETS`
is the valid zero subset. Unknown names and malformed comma lists fail startup.

Every tool carries conservative MCP annotations: `readOnlyHint=false`,
`destructiveHint=true`, `idempotentHint=false`, and `openWorldHint=true`.
Those hints do not claim that an unknown tmux configuration is harmless. The
capability row below each tool gives the precise process reach, tmux effects,
output classes, trust flags, and input interpreter boundaries.

### Finding out what is there

| Tool | Does |
| --- | --- |
| `list_sessions` | Lists sessions and their materialized metadata |
| `list_windows` | Lists windows, optionally within a named session |
| `list_panes` | Lists pane identity, geometry, process state, and caller identity without reading terminal content |
| `get_server_info` | Reports the pinned socket, liveness, topology counts, clients, and whether this process is inside the selected server |
| `get_session_info` | Reads one session selected by exact id |
| `get_window_info` | Reads one window's size, layout, and panes by exact id |
| `get_pane_info` | Reads one pane's process and mode state without its contents |
| `find_pane_by_position` | Finds a pane at one corner of a window |
| `get_tmux_variables` | Reads a bounded list of validated variable names without accepting free-form tmux format text |

### Reading what a pane holds

| Tool | Does |
| --- | --- |
| `capture_pane` | Reads bounded visible output, or scrollback when requested |
| `capture_since` | Reads only output after an opaque cursor |
| `snapshot_pane` | Returns bounded pane content with pane metadata |
| `search_panes` | Searches pane output under fixed pane, line, byte, pattern, and time ceilings |
| `wait_for_text` | Waits for one of several success or failure patterns without polling |
| `show_environment` | Reads the tmux environment; values can contain secrets |
| `show_hooks` / `show_option` | Reads configured commands and option state |
| `call_read_tools_batch` | Runs up to sixteen permitted inspect operations serially with typed inner validation |

### Changing tmux without reaching a workload process

The `manage` tools rename, select, resize, move, swap, title, and change a small
set of explicit options. They do not accept commands, environment values, or a
generic option name/value pair.

`set_synchronize_panes` is the one future-input amplifier. Enabling it means
the window default allows subsequent input to fan out. Pane-level overrides
determine configured membership for each input preflight. `send_keys` and
`send_keys_batch` report that sorted membership rather than claiming which
panes ultimately received input.

### Starting configured processes and sending input

`create_session`, `create_window`, `split_window`, and `respawn_pane` start the
process already configured for the pane. Their schemas deliberately contain no
command or environment payload.

`run_shell_command` is the one pane-command route. `send_keys`,
`send_keys_batch`, and `paste_text` reach the program already running in a
pane. There is no host-command tool: every command payload runs inside the
selected tmux pane with the user's permissions.

### Ending things

`clear_pane_scrollback`, `kill_pane`, `kill_window`, and `kill_session` belong
to `teardown`. A new authenticated product-dedicated server with the bundled
minimal configuration enables all four toolsets by default. An existing,
explicitly named, path-selected, or user-configured server omits teardown by
default; selecting teardown explicitly is the opt-in.

### Reading a pane repeatedly

`capture_pane` returns the screen a caller has often already seen.
`capture_since` returns what the pane wrote after a cursor from the previous
call, so a quiet pane costs almost nothing and a busy pane costs only its new
lines. The cursor is opaque and binds the pane and its process generation.

tmux can discard scrollback. When the cursor anchor is gone, `linesMissed`
reports that the caller's record has a gap instead of silently treating the
current screen as complete. Keep the new cursor even after a gap so later
reads can continue from a known point.

### Bounded patterns and replies

Patterns are bounded before dispatch: at most 32 patterns, 4,096 bytes each,
and 16,384 bytes combined. `search_panes` also stops after 200 panes, 20,000
lines, 1,000,000 inspected bytes, or five seconds, and reports the effective
work ceilings with its result.

Pane-output tools keep the newest lines within their declared bounds and
report truncation. The read batch executes at most sixteen operations in order,
retains full nested envelopes when they fit, and marks an oversized nested
result `resultTruncated`. Its complete JSON-RPC response is at most 1,000,000
bytes. A separate response backstop refuses any future tool that forgets to
bound its own result.

### Waiting rather than looking

Prefer `run_shell_command` to sending a command and immediately capturing the
screen. A shell echoes input before it executes it, so the capture can find the
request rather than the result. `run_shell_command` frames one authored command,
waits for its completion record, and returns its exit status and bounded output.

For output the client did not author, use `wait_for_text`. It can match output
already present or output that arrives later, and `stop` patterns turn known
failure text into an early answer. Every wait has a ceiling: 300 seconds by
default, configurable with `LIBTMUX_MCP_WAIT_MAX_SECONDS`. A longer requested
timeout is clamped and disclosed in the response.

Commands for `run_shell_command` are staged as private files and sourced by the
pane's POSIX-compatible shell. They do not cross the interactive line editor.
A command runs in a subshell, so `cd`, `export`, `exit`, and other shell-state
changes do not persist in the pane's interactive shell.
A timeout can leave the pane's program running; inspect it with
`get_pane_info`, then send `C-c` if interruption is intended.

### How large an argument can be

The native schemas cap patterns, operation counts, command output requests, and
other fields whose cost grows with caller input. Pane text for `paste_text` and
commands for `run_shell_command` use private files or buffers rather than being
packed into an unrestricted shell or tmux-format expression. A rejected bound
is a tool error before its handler reaches tmux.

### Narrowing a listing

Use `list_windows` with a session name when the session is known. `list_panes`
returns metadata for the pinned server; use its ids with the exact getter tools
instead of repeatedly capturing unrelated panes. When the question is about
terminal text, `search_panes` performs the narrowing under its aggregate work
budget and returns only matching lines.

### No detached jobs

The server creates no MCP-side background job or handle. A long-running process
lives in a tmux pane, where `get_pane_info`, `capture_since`, and
`wait_for_text` continue to describe it even if the MCP process restarts. This
keeps lifecycle authority in tmux rather than in an in-memory job registry.

### The pane this server is running in

Pane summaries carry `isCaller`. A true value identifies the terminal carrying
this MCP process after both pane id and socket match. Writing to or ending that
pane can disrupt the conversation, so those routes request MCP elicitation and
fail closed when the client cannot ask. `confirm_self` never bypasses a missing
teardown selection; it only confirms the target after the tool exists.

Splitting beside the caller pane is not blocked because it preserves the pane.
The MCP does not enter or cancel pane modes; an attached person's modal state
stays under that person's control.

### Read retained scrollback without changing pane state

Reading old output does not require copy mode. Call `get_pane_info` first when
the amount of retained output matters: `historyLines` reports what tmux still
holds and `historyLimit` reports the configured ceiling.

Call `capture_pane` with `history: true` and a bounded `max_lines` to read the
newest retained rows together with the visible screen. Its truncation fields
say when the requested bound omitted older rows. This read does not move an
attached client's view, create a selection, change a key table, or write to the
clipboard.

Use `snapshot_pane` with the same `history` and `max_lines` controls when the
decision also needs pane metadata. It returns terminal content and pane
metadata in one MCP round trip; it does not promise an atomic or temporally
coherent view of a pane that changes while the tool runs.

Use `capture_since` for output after an initial observation. Keep its opaque
cursor and check `linesMissed` on every continuation. A missing cursor starts a
new observation; it does not request all older scrollback.

The current `search_panes` schema searches bounded visible output. It has no
history selector, so use a history capture when the sought text may have
scrolled away. The MCP also does not expose tmux's mode-screen capture: pane
captures report terminal content, not a person's copy-mode viewport or
selection.

| Need | Use | Constraint |
| --- | --- | --- |
| Newest visible rows | `capture_pane` | Keep `max_lines` bounded |
| Retained scrollback | `capture_pane`, `history: true` | Old rows may be gone |
| Content plus state | `snapshot_pane` | Enable `history` only when needed |
| New output | `capture_since` | Keep its cursor; check `linesMissed` |
| A text match across panes | `search_panes` | Searches visible output only |

These reads preserve the distinction between observing terminal state and
controlling a human terminal interface.

### When a person owns the pane's current mode

`get_pane_info.inMode` reports that tmux, rather than the workload program,
currently owns the pane's input. It does not identify the mode, its key table,
its selection, or who entered it. Treat the state as human-owned unless the
caller has independent lifecycle knowledge.

Input tools refuse a pane while `inMode` is true. Sending `C-c`, `Escape`, or a
command would otherwise invoke mode bindings instead of reaching the program.
The MCP does not cancel the mode because a generic cancellation can discard a
selection or leave a different modal interface than the caller assumed.

Continue with observation while the mode is active:

1. Use `capture_pane` or `snapshot_pane` to read terminal content without
   changing the view.
2. Use `capture_since` or `wait_for_text` when progress can be observed from
   new workload output.
3. Recheck `get_pane_info`; resume input only after `inMode` becomes false.
4. Ask the attached person to leave the mode when coordination is required.

Do not poll by sending keys. The transition belongs to the attached client,
and input is safe only after tmux reports that the program owns it again.

Applications built directly on the Go tmux module still have
`Pane.CopyMode`. They can pair entry and cleanup when they own the complete
interaction. The MCP is a curated detached-safe surface, so it does not expose
every operation available in the core library.

### Text, keys, and synchronized targets

`send_keys` and each `send_keys_batch` operation take tmux key names such as
`C-c`, `Escape`, and `Enter`. Use `literal` when the strings themselves should
be sent. Use `paste_text` for an arbitrary block whose words must never be
interpreted as key names.

Input tools read effective `pane_synchronized` values from a fresh pane
snapshot. A source configured off has source-only membership; a source
configured on includes the window's effective-on panes. Unreadable flags or a
dead or modal configured member refuse the whole operation before caller
confirmation or mutation. These checks observe tmux state but cannot make the
later input command atomic with that observation.

`send_keys` and each batch row return sorted configured membership in
`resolved_pane_ids`. `run_shell_command` requires that membership to contain
one pane both before setup and immediately before dispatch because it returns
one output stream and exit status. `paste_text` always delivers only to its
target. Optional Enter is one newline appended to the same private buffer, so
neither part is broadcast through synchronized-input key handling.

The send and run arrays describe preflight membership, not proven delivery or
effects. Successful calls and batch rows expose them directly. Ordinary
direct-tool refusals name relevant membership in error text because the Go
dispatcher discards structured output with handler errors. Paste reports only
its target and accepted byte count because text and optional newline share one
target-only buffer operation.

### Batches

`call_read_tools_batch` has exact nested authority: every inspect tool except
itself and `wait_for_text`. Startup exclusions prune that authority even when an
inner tool is not separately advertised. If no inner authority remains, the
batch stays advertised with an unsatisfiable operations schema and cannot run.

Operations execute serially and receive the same native schema validation as a
top-level call. `on_error` is `stop` or `continue`. Each result row carries its
index, tool, success, error, full retained nested result, and truncation flag;
the aggregate reports succeeded, failed, stoppedAt, and truncation totals.

## Recipes

Jobs in the order the calls actually go. The generated entries say what each
tool accepts; these say what to reach for and where a shortcut goes wrong.

### Start a service and wait for it before running dependent work

**Situation.** A session has no service running, and integration tests need
one.

> Start the API server in my backend session and run the integration tests once
> it is ready.

**Discover.** Use `list_panes` and `search_panes` to avoid starting a duplicate.

**Decide.** Give the service its own pane so its output stays separate from the
tests.

**Act.** Make room with `split_window`. Take a `capture_since` cursor, start the
service in that pane with `paste_text` and `enter: true`, then call
`wait_for_text` with the cursor, the ready marker in `patterns`, and known
failure markers in `stop`. Once ready, run the tests in another pane with
`run_shell_command` and inspect its exit status.

**The non-obvious part.** The wait replaces a fixed sleep and stops early on
known failure output. Construct a completion marker at execution time instead
of putting the exact marker in the command text, or the shell's echo can match
before the service is ready. `split_window` carries no hidden command; process
creation and caller-authored input remain separate decisions.

### Find the failing pane without opening random terminals

**Situation.** Several panes are working and one failed.

> Which one failed, and why?

**Discover.** Call `search_panes` with a bounded `pattern`, such as `FAILED`,
`error:`, or `Traceback`. It returns the matching panes and lines.

**Decide.** If those lines explain the failure, stop. They usually do.

**Act.** If those lines are insufficient, call `snapshot_pane` for the pane it
named and request history only when needed.

**The non-obvious part.** Capturing every pane sends all terminal content
through the client's context. Searching where the text already lives is both
smaller and bounded by the server's aggregate work budget.

### Watch a long job across several turns

**Situation.** A build will outlast this exchange.

> Keep an eye on the build and tell me when it breaks.

**Discover.** Call `capture_since` with the pane id and no cursor. It returns
the visible baseline and a cursor.

**Decide.** Keep the cursor. It is the only client-side state needed.

**Act.** Pass the pane id and cursor on later `capture_since` calls. Each reply
contains only new output and a fresh cursor to keep instead. Use
`wait_for_text` when one ready or failure marker should end the current call.

**The non-obvious part.** Repeated `capture_pane` calls resend the same screen
and cannot prove whether anything changed. Check `linesMissed` before treating
the accumulated record as complete: true means tmux discarded scrollback
between reads and the record has a hole.

There is deliberately no detached job handle. A command that must outlive one
turn runs visibly in its pane; the pane id and capture cursor are the durable
observation state.

### Run a build without spending the turn on it

**Situation.** A test suite takes minutes, and there is other work to do while
it runs.

> Run the suite and start reading the failing module while it goes.

**Discover.** Select a dedicated shell pane and take a `capture_since` cursor
before starting the suite.

**Decide.** Keep lifecycle state in tmux rather than in an MCP-side job handle.
The pane survives a client or MCP server restart.

**Act.** Start the suite with target-only `paste_text` and `enter: true`, then
continue the other work. On a later turn, use `get_pane_info` to inspect process
state, `capture_since` to collect only new lines, or `wait_for_text` with known
completion and failure markers. Use `run_shell_command` instead when the work
fits inside one bounded call and a framed exit status is the desired result.

**The non-obvious part.** There is no handle to collect and no detached process
owned by this server. The pane id, capture cursor, visible process, and output
are recoverable after a restart; the tradeoff is that the client recognizes
completion from pane state or a marker rather than an in-memory job record.

### Check on eight panes without reading any of them

**Situation.** Eight workspace panes are partway through a long job.

> Which of those are still going?

**Discover.** Use one `list_panes` call for the server-wide identity, active
state, current command, window relation, caller identity, and geometry.

**Decide.** Narrow those rows to the intended session or window. Follow only
the panes whose process or mode state matters with `get_pane_info`.

**Act.** Compare current commands and the detailed dead, exit, and history
metadata with the prior reading. Capture only panes whose terminal output is
actually needed.

**The non-obvious part.** The first orientation is one snapshot for every pane,
not one screen read per pane. Listing metadata does not send eight terminals'
contents through the client's context.

### Recover a pane that stopped answering

**Situation.** A `run_shell_command` timed out, and later input to that pane is
not making progress.

**Discover.** Call `get_pane_info` and inspect `currentCommand`, `inMode`, and
`dead`. The timeout result's `running` field also identifies a foreground
program when one was observed.

**Decide.** A known shell may still be running the command. A different
foreground program may have owned input. A human-owned tmux mode consumes keys
before they reach either, and a dead pane reads nothing.

**Act.** If `inMode` is true, keep observing with captures or snapshots and ask
the attached person to leave the mode. Otherwise, use `send_keys` with `C-c`
for an intentional interruption. Use `respawn_pane` only to restart a
configured process deliberately; it is not a mode-recovery shortcut.

**The non-obvious part.** A timeout deliberately leaves authored work visible
in the pane. Its symptom can resemble a busy foreground program, so inspect
the pane before interrupting it. Input tools fail closed on dead, modal,
attended, caller-protected, or malformed configured state rather than waiting
on an input path that cannot safely complete.

## Gotchas

**Reading immediately after sending input is a race.** Input tools return when
tmux accepts the input, not when the program finishes. Use
`run_shell_command` for authored commands or `wait_for_text` for externally
produced output.

**A pane mode consumes keys.** `get_pane_info` reports `inMode`. Capture output
without changing it, and wait for the attached person to leave the mode before
sending input.

**Window names are not unique.** Exact window ids are stable lookup targets
within a running server. Pane ids are unambiguous while the pane exists but may
be reused later; capture cursors also bind process identity.

**Shell-history suppression is best effort.** `suppress_history` prefixes the
staged command for shells configured to ignore space-prefixed history. It does
not promise behavior from an unknown user configuration.

**Listing is not reading.** Metadata listings do not return terminal content.
`capture_pane`, `snapshot_pane`, `search_panes`, and `wait_for_text` do.

**Names and paths can contain tmux format syntax.** Manifest rows mark every
format-expanding boundary. Fields controlled as literal are escaped exactly
once by doubling `#`; `get_tmux_variables` accepts only validated variable
names instead of arbitrary formats.

## Logs

A client that sets an MCP logging level can receive why a wait ended without a
match: which pane was watched, what it was running, and how long the wait took.
Tool results say what happened; logs add diagnostic context. A client that does
not set a logging level receives none.

## Keeping a record

Set `LIBTMUX_AUDIT` to `stderr` or to an append path to record every tool call
as one JSON line.

```console
$ LIBTMUX_AUDIT=/tmp/tmux-mcp.log libtmux-mcp -socket-name my-application
```

```json
{"time":"...","level":"INFO","msg":"tool call","tool":"send_keys",
 "outcome":"ok","elapsedMillis":2,
 "arguments":{"pane_id":"%0","keys":{"len":18,"sha256":"c27651833c4e"}}}
```

Command and text payloads are summarized by byte length and a digest prefix,
not stored in cleartext. Identifiers and numeric controls come from a small
allowlist; a new unclassified field defaults to payload treatment. New audit
files use mode `0600`, and auditing is off unless configured.

## Error handling

`list_sessions`, `list_windows`, and `list_panes` treat a missing server as an
empty topology and include a `serverNote`. This lets a client orient before
`create_session`. Reads requiring a live object and every mutation preserve
tmux failures as tool errors; a mutation that silently did nothing would leave
the caller with the wrong state.

```text
tmux: command failed: kill-session exited 1: can't find session: no-such-session
```

The startup selectors fail before tmux opens when a tool name or toolset is
unknown, a nonempty comma list contains an empty token, a socket path or config
path is relative, or any of the retired `LIBTMUX_SAFETY`,
`LIBTMUX_MCP_CAPABILITIES`, and `LIBTMUX_MCP_PROMPTS_AS_TOOLS` variables is
present.

## Retired MCP surface

The capability-model migration intentionally removed routes that bypassed the
fixed socket boundary, exposed broad interpreters, created background authority,
or duplicated the 45-tool cross-port inventory. These names are not hidden
aliases: a client must migrate its calls.

### Selection and resource migration

`LIBTMUX_SAFETY` and the independent `LIBTMUX_MCP_CAPABILITIES` allowlist are
retired. Their presence stops startup; use the unordered toolset and named
include/exclude variables described above. The migration table in the
[MCP README](README.md#moving-from-an-earlier-alpha) maps every earlier tier,
capability class, and profile.

Per-call socket selection and `list_servers` are gone. Run another pinned
server process for another socket. Dynamic resources migrate as follows:

| Earlier resource | Current path |
| --- | --- |
| `tmux://sessions` | `list_sessions` |
| `tmux://sessions/{session}` | `get_session_info` |
| `tmux://sessions/{session}/windows` | `list_windows` with that session |
| `tmux://windows/{window}` | `get_window_info` |
| `tmux://windows/{window}/panes` | `list_panes`, then select rows with that `windowId` |
| `tmux://panes/{pane}` | `get_pane_info` |
| `tmux://panes/{pane}/content` | `capture_pane` or `capture_since` |

Subscriptions and completions have no replacement. Any present
`LIBTMUX_MCP_PROMPTS_AS_TOOLS` value stops startup instead of exporting prompts
through `get_recipe`.

### Retired prompt workflow mapping

- `diagnose_pane`: call `get_pane_info`, then `snapshot_pane`; use
  `wait_for_text` for progress or `run_shell_command` for bounded work, and
  `show_option` or `show_hooks` when configured behavior is suspect.
- `watch_pane`: keep one `capture_since` cursor per pane across turns; use
  `wait_for_text` instead when waiting for one expected marker.
- `recover_pane`: inspect `get_pane_info`; use `wait_for_text` when work is
  merely slow, `send_keys` with `C-c` only when `inMode` is false, and leave a
  human-owned pane mode unchanged while observing it through capture tools.
- `set_up_workspace`: compose `create_session`, `create_window`,
  `split_window`, and the layout, title, and selection tools. Use
  `run_shell_command` for bounded work, or `paste_text` with `wait_for_text` or
  `capture_since` for long-lived visible work. Before `select_window`, inspect
  `get_server_info.attachedClients`; changing the selected window can change
  what an attached person sees.

### Retired tool mapping

| Earlier tool | Current path |
| --- | --- |
| `enter_copy_mode`, `exit_copy_mode` | capture; the person owns modes |
| `run_command` | `run_shell_command`; detached mode was removed |
| `get_job` | no handle; observe the pane with `capture_since` or `wait_for_text` |
| `call_readonly_tools_batch` | `call_read_tools_batch`, with exact inspect-only nested authority |
| `call_mutating_tools_batch` / `call_destructive_tools_batch` | no generic mutation batch; call the typed tools explicitly |
| `clear_pane` | `clear_pane_scrollback`, classified as teardown |
| `set_option` | constrained tools such as `set_mouse_enabled`, `set_history_limit`, and `set_synchronize_panes` |
| `set_environment` | no generic caller-controlled process environment route |
| `display_message` | `get_tmux_variables` for validated variable names; no free-form tmux-format interpreter |
| `capture_pane styles` / colored output | No current MCP replacement; captures return plain rendered text, so do not infer status from color alone |
| `build_workspace` | compose `create_session`, `create_window`, `split_window`, and layout tools |
| `move_pane` | compose the retained layout operations; no direct public MCP replacement |
| `load_buffer` / `paste_buffer` | `paste_text` stages an ephemeral private buffer internally |
| `show_buffer` / `delete_buffer` | no public buffer namespace |
| `pipe_pane` | no shell-command pipe route; use bounded capture/wait tools |
| `kill_server` | no server-wide teardown route; kill selected sessions explicitly or administer tmux outside MCP |

The old prompt routes, dynamic resources, generic setters, background jobs,
buffer namespace, pipe route, and broad batch families remain absent from MCP
registration and dispatch. The prompt workflows above remain available as
typed-tool guidance. Internal helpers survive only where a retained tool uses
them.

## Every tool

The entries below are rendered from the same manifest schemas and capability
metadata sent over MCP. Edit the native definitions, not this generated region.

<!-- toolsref -->

45 tools. Generated from the schemas by `go generate ./...`; edit the tools, not this.

### `call_read_tools_batch`

Read pane output; accepts no client-supplied executable input. Returned content may be sensitive or untrusted. Calls up to sixteen eligible inspect tools serially; inner tools receive no separate approval. Retained rows contain full nested envelopes, oversized results are marked resultTruncated, and the complete JSON-RPC response is at most 1,000,000 bytes.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata`, `terminal-content`, `process-environment`, `configured-command` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | `list_sessions`, `list_windows`, `list_panes`, `get_server_info`, `get_session_info`, `get_window_info`, `get_pane_info`, `capture_pane`, `capture_since`, `snapshot_pane`, `search_panes`, `find_pane_by_position`, `get_tmux_variables`, `show_option`, `show_environment`, `show_hooks` |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `operations` **required** | array | up to sixteen inspect operations |
| `on_error` | `stop`, `continue` | stop or continue; defaults to stop |

| Returns | Type |
| --- | --- |
| `failed` **required** | integer |
| `onError` **required** | string |
| `results` **required** | array |
| `stoppedAt` **required** | integer |
| `succeeded` **required** | integer |
| `truncated` **required** | boolean |
| `truncatedBytes` **required** | integer |

### `capture_pane`

Read pane output; accepts no client-supplied executable input. Returned content may be sensitive or untrusted. Returns bounded pane content and a cursor.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `terminal-content`, `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `history` | boolean | include scrollback as well as the visible screen |
| `max_lines` | integer | maximum lines, keeping the newest |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `truncated` **required** | boolean |
| `lines` | array |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `capture_since`

Read pane output; accepts no client-supplied executable input. Returned content may be sensitive or untrusted. Returns pane output produced after a cursor.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `terminal-content`, `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `cursor` | string | a cursor returned by an earlier capture |
| `max_lines` | integer | maximum new lines, keeping the newest |

| Returns | Type |
| --- | --- |
| `cursor` **required** | string |
| `lines` **required** | array |
| `linesMissed` **required** | boolean |
| `paneId` **required** | string |
| `truncated` **required** | boolean |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `clear_pane_scrollback`

Delete tmux state; accepts no command payload. Deletes retained scrollback from one pane.

Belongs to the `teardown` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `delete` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

**Deletes tmux state.** Repeating it can remove more state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |

| Returns | Type |
| --- | --- |
| `pane_id` **required** | string |

### `create_session`

Start a pane's configured process; accepts no command payload. Creates a detached session whose first pane runs the configured process.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `configured-process` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `height` | integer | initial height; supply with width |
| `session_name` | string | a literal session name |
| `start_directory` | string | an absolute literal start directory |
| `width` | integer | initial width; supply with height |
| `window_name` | string | a literal first-window name |

| Returns | Type |
| --- | --- |
| `sessionId` **required** | string |
| `sessionName` **required** | string |

### `create_window`

Start a pane's configured process; accepts no command payload. Creates a window whose first pane runs the configured process.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `configured-process` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `session_id` **required** | string | the session id, such as $1 |
| `attach` | boolean | make the new window active |
| `direction` | `before`, `after` | before or after |
| `start_directory` | string | an absolute literal start directory |
| `window_name` | string | a literal window name |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `windowId` **required** | string |

### `find_pane_by_position`

Inspect tmux metadata; accepts no client-supplied executable input. Finds a pane at one of a window's four corners.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `position` **required** | `top-left`, `top-right`, `bottom-left`, `bottom-right` | top-left, top-right, bottom-left, or bottom-right |
| `window_id` **required** | string | the window id, such as @1 |

| Returns | Type |
| --- | --- |
| `found` **required** | boolean |
| `geometry` **required** | object |
| `paneId` **required** | string |

### `get_pane_info`

Inspect tmux metadata; accepts no client-supplied executable input. Returns metadata for one pane.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |

| Returns | Type |
| --- | --- |
| `dead` **required** | boolean |
| `historyLimit` **required** | integer |
| `historyLines` **required** | integer |
| `inMode` **required** | boolean |
| `pane` **required** | object |
| `path` **required** | string |
| `pid` **required** | integer |
| `title` **required** | string |
| `zoomed` **required** | boolean |
| `exitStatus` | integer |

### `get_server_info`

Inspect tmux metadata; accepts no client-supplied executable input. Reports whether the pinned server exists and its version.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Returns | Type |
| --- | --- |
| `alive` **required** | boolean |
| `attachedClients` **required** | array |
| `clients` **required** | integer |
| `insideThisServer` **required** | boolean |
| `panes` **required** | integer |
| `sessions` **required** | integer |
| `socketPath` **required** | string |
| `truncated` **required** | boolean |
| `version` **required** | string |
| `windows` **required** | integer |
| `callerPaneId` | string |
| `messages` | array |
| `messagesUnavailable` | string |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `get_session_info`

Inspect tmux metadata; accepts no client-supplied executable input. Returns metadata for one session.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `session_id` **required** | string | the session id, such as $1 |

| Returns | Type |
| --- | --- |
| `activeWindowId` **required** | string |
| `created` **required** | string |
| `path` **required** | string |
| `session` **required** | object |
| `windows` **required** | array |

### `get_tmux_variables`

Read configured tmux commands; accepts no client-supplied executable input. Returned values may contain executable configuration. Reads a capped list of validated tmux variable names, not free-form formats.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata`, `configured-command` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `names` **required** | array | variable names matching [A-Za-z][A-Za-z0-9_]* |

| Returns | Type |
| --- | --- |
| `values` **required** | object |

### `get_window_info`

Inspect tmux metadata; accepts no client-supplied executable input. Returns metadata for one window.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `window_id` **required** | string | the window id, such as @1 |

| Returns | Type |
| --- | --- |
| `height` **required** | integer |
| `layout` **required** | string |
| `panes` **required** | array |
| `width` **required** | integer |
| `window` **required** | object |
| `zoomed` **required** | boolean |

### `kill_pane`

Delete tmux state; accepts no command payload. Deletes one pane and ends its process.

Belongs to the `teardown` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `delete` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

**Deletes tmux state.** Repeating it can remove more state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `confirm_self` | boolean | permit ending the pane this MCP process runs in |

| Returns | Type |
| --- | --- |
| `killed` **required** | string |
| `windowEnded` **required** | boolean |

### `kill_session`

Delete tmux state; accepts no command payload. Deletes one session and every window and pane in it.

Belongs to the `teardown` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `delete` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

**Deletes tmux state.** Repeating it can remove more state.

| Argument | Type | |
| --- | --- | --- |
| `session_id` **required** | string | the session id, such as $1 |
| `confirm_self` | boolean | permit ending the pane this MCP process runs in |

| Returns | Type |
| --- | --- |
| `killed` **required** | string |

### `kill_window`

Delete tmux state; accepts no command payload. Deletes one window and every pane in it.

Belongs to the `teardown` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `delete` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

**Deletes tmux state.** Repeating it can remove more state.

| Argument | Type | |
| --- | --- | --- |
| `window_id` **required** | string | the window id, such as @1 |
| `confirm_self` | boolean | permit ending the pane this MCP process runs in |

| Returns | Type |
| --- | --- |
| `killed` **required** | string |
| `sessionEnded` **required** | boolean |

### `list_panes`

Inspect tmux metadata; accepts no client-supplied executable input. Lists pane metadata and stable pane IDs.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Returns | Type |
| --- | --- |
| `panes` **required** | array |
| `total` **required** | integer |
| `serverNote` | string |
| `skipped` | integer |

### `list_sessions`

Inspect tmux metadata; accepts no client-supplied executable input. Lists sessions on the pinned tmux server.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Returns | Type |
| --- | --- |
| `sessions` **required** | array |
| `total` **required** | integer |
| `serverNote` | string |
| `skipped` | integer |

### `list_windows`

Inspect tmux metadata; accepts no client-supplied executable input. Lists windows, optionally only those in one named session.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `session` | string | only windows in this session name |

| Returns | Type |
| --- | --- |
| `total` **required** | integer |
| `windows` **required** | array |
| `serverNote` | string |
| `skipped` | integer |

### `move_window`

Change tmux state; no client-supplied executable input. Moves a window to another session, optionally at an index.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `session_id` **required** | string | the destination session id, such as $1 |
| `window_id` **required** | string | the window id, such as @1 |
| `index` | integer | a destination window index; omit for tmux's choice |

| Returns | Type |
| --- | --- |
| `index` **required** | integer |
| `session` **required** | string |
| `windowId` **required** | string |

### `paste_text`

Send input to a pane's program; a shell that receives it runs it with your user's permissions. Pastes literal text only to its target; optional Enter appends one newline to the same private buffer.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `pane-input` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `text` **required** | string | the literal text to paste |
| `enter` | boolean | append a newline that submits the text |

| Returns | Type |
| --- | --- |
| `bytes` **required** | integer |
| `pane_id` **required** | string |

### `rename_session`

Change tmux state; no client-supplied executable input. Replaces a session's name.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `new_name` **required** | string | the literal new session name |
| `session_id` **required** | string | the session id, such as $1 |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `sessionId` **required** | string |

### `rename_window`

Change tmux state; no client-supplied executable input. Replaces a window's name.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `new_name` **required** | string | the literal new window name |
| `window_id` **required** | string | the window id, such as @1 |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `windowId` **required** | string |

### `resize_pane`

Change tmux state; no client-supplied executable input. Sets a pane's width, height, or both.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `height` | integer | height in terminal cells; omit to retain it |
| `width` | integer | width in terminal cells; omit to retain it |

| Returns | Type |
| --- | --- |
| `height` **required** | integer |
| `paneId` **required** | string |
| `width` **required** | integer |

### `resize_window`

Change tmux state; no client-supplied executable input. Sets a window's width, height, or both.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `window_id` **required** | string | the window id, such as @1 |
| `height` | integer | height in terminal cells; omit to retain it |
| `width` | integer | width in terminal cells; omit to retain it |

| Returns | Type |
| --- | --- |
| `height` **required** | integer |
| `width` **required** | integer |
| `windowId` **required** | string |

### `respawn_pane`

Start a pane's configured process; accepts no command payload. Kills the pane's current process and starts its configured process again.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `configured-process` |
| `tmuxEffects` | `observe`, `change`, `delete` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

**Deletes tmux state.** Repeating it can remove more state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `start_directory` | string | an absolute literal start directory |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `gone` | boolean |

### `run_shell_command`

Run a shell command in a pane with your user's permissions. Runs one authored command only for configured singleton membership, checks it before setup and again before dispatch, and waits for framed completion.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `pane-command` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `terminal-content`, `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `command` **required** | string | the shell command run in the pane's interactive shell |
| `pane_id` **required** | string | the pane id, such as %1 |
| `max_lines` | integer | maximum output lines, keeping the newest |
| `suppress_history` | boolean | best-effort persistent history suppression |
| `timeout` | number | seconds to wait before giving up |

| Returns | Type |
| --- | --- |
| `output` **required** | array |
| `pane_id` **required** | string |
| `resolved_pane_ids` **required** | array |
| `timed_out` **required** | boolean |
| `effective_timeout_seconds` | integer |
| `exit_status` | integer |
| `lines_missed` | boolean |
| `output_unavailable` | string |
| `running` | string |
| `timeout_clamped` | boolean |

### `search_panes`

Read pane output; accepts no client-supplied executable input. Returned content may be sensitive or untrusted. Searches the visible output of every pane.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `terminal-content`, `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `pattern` **required** | string | bounded text or regular expression to search for |
| `max_lines` | integer | maximum matching panes to return |
| `max_matches_per_pane` | integer | maximum matching lines per pane |
| `regex` | boolean | treat pattern as a regular expression |

| Returns | Type |
| --- | --- |
| `bytesInspected` **required** | integer |
| `linesInspected` **required** | integer |
| `panes` **required** | array |
| `panesInspected` **required** | integer |
| `workLimited` **required** | boolean |
| `workTimeLimitSeconds` **required** | number |
| `morePanes` | integer |

### `select_layout`

Change tmux state; no client-supplied executable input. Applies a named layout, a unique abbreviation for the running tmux version, or a saved layout from get_window_info. Invalid syntax is rejected before window lookup; tmux validates geometry when applying the layout.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `layout` **required** | string | a named layout, a unique abbreviation for the running tmux version, or a checksummed saved layout from get_window_info |
| `window_id` **required** | string | the window id, such as @1 |

| Returns | Type |
| --- | --- |
| `layout` **required** | string |
| `windowId` **required** | string |

### `select_pane`

Change tmux state; no client-supplied executable input. Makes one pane active.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |

### `select_window`

Change tmux state; no client-supplied executable input. Makes one window active.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `window_id` **required** | string | the window id, such as @1 |

| Returns | Type |
| --- | --- |
| `windowId` **required** | string |

### `send_keys`

Send input to a pane's program; a shell that receives it runs it with your user's permissions. Sends input after validating sorted configured synchronized membership; reported ids describe preflight membership, not proven effects.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `pane-input` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `keys` **required** | array | key names or literal strings to send |
| `pane_id` **required** | string | the pane id, such as %1 |
| `literal` | boolean | send strings literally instead of as key names |

| Returns | Type |
| --- | --- |
| `pane_id` **required** | string |
| `resolved_pane_ids` **required** | array |
| `sent` **required** | integer |

### `send_keys_batch`

Send input to a pane's program; a shell that receives it runs it with your user's permissions. Sends up to sixty-four ordered pane-input operations, each with a fresh configured-membership preflight.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `pane-input` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `operations` **required** | array | up to sixty-four ordered pane-input operations |
| `on_error` | `stop`, `continue` | stop or continue; defaults to stop |

| Returns | Type |
| --- | --- |
| `completed` **required** | integer |
| `results` **required** | array |
| `failed` | integer |

### `set_history_limit`

Change tmux state; no client-supplied executable input. Sets a bounded integer scrollback limit for future panes in a session.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `lines` **required** | integer | the nonnegative retained line count |
| `session_id` **required** | string | the session id, such as $1 |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `enabled` | boolean |
| `target` | string |
| `value` | string |

### `set_mouse_enabled`

Change tmux state; no client-supplied executable input. Enables or disables tmux mouse handling.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `enabled` | boolean | whether the setting is enabled |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `enabled` | boolean |
| `target` | string |
| `value` | string |

### `set_pane_title`

Change tmux state; no client-supplied executable input. Replaces a pane's literal title.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `title` **required** | string | the literal title |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `title` **required** | string |

### `set_synchronize_panes`

Change tmux state; no client-supplied executable input. Sets the window synchronization default; pane-level overrides determine later configured input membership and effects can still differ.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `true` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `window_id` **required** | string | the window id, such as @1 |
| `enabled` | boolean | whether pane input is synchronized |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `enabled` | boolean |
| `target` | string |
| `value` | string |

### `show_environment`

Read the tmux environment; accepts no client-supplied executable input. Returned values may contain secrets. Reads the environment tmux passes to processes.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `process-environment` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `session` | string | a session name; omit for the only session |

| Returns | Type |
| --- | --- |
| `sessionName` **required** | string |
| `truncated` **required** | boolean |
| `variables` **required** | array |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |
| `valuesWithheld` | boolean |

### `show_hooks`

Read configured tmux commands; accepts no client-supplied executable input. Returned values may contain executable configuration. Reads configured tmux hooks.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `configured-command` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `name` | string | one hook name; omit to read all hooks in the scope |
| `scope` | `""`, `global`, `server`, `session`, `window`, `pane` | global, server, session, window, or pane |
| `target` | string | the target required by session, window, and pane scopes |

| Returns | Type |
| --- | --- |
| `hooks` **required** | array |
| `scope` **required** | string |

### `show_option`

Read configured tmux commands; accepts no client-supplied executable input. Returned values may contain executable configuration. Reads one named tmux option.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `tmux-metadata`, `configured-command` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `false` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the exact option name |
| `effective` | boolean | include an inherited value |
| `scope` | `""`, `global`, `server`, `session`, `window`, `pane` | global, server, session, window, or pane |
| `target` | string | the target required by session, window, and pane scopes |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `scope` **required** | string |
| `set` **required** | boolean |
| `value` **required** | string |
| `inherited` | boolean |

### `signal_channel`

Change tmux state; no client-supplied executable input. Signals one server-wide tmux channel.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `channel` **required** | string | the tmux wait-for channel to signal |

| Returns | Type |
| --- | --- |
| `channel` **required** | string |

### `snapshot_pane`

Read pane output; accepts no client-supplied executable input. Returned content may be sensitive or untrusted. Returns pane metadata and bounded terminal content together.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `terminal-content`, `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `history` | boolean | include scrollback as well as the visible screen |
| `max_lines` | integer | maximum lines, keeping the newest |

| Returns | Type |
| --- | --- |
| `dead` **required** | boolean |
| `lines` **required** | array |
| `pane` **required** | object |
| `truncated` **required** | boolean |
| `exitStatus` | integer |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `split_window`

Start a pane's configured process; accepts no command payload. Creates a pane whose configured process starts after the split.

Belongs to the `execute` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `configured-process` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `direction` | `""`, `below`, `above`, `right`, `left` | below, above, left, or right |
| `percent` | integer | share of the split occupied by the new pane |
| `start_directory` | string | an absolute literal start directory |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |

### `swap_pane`

Change tmux state; no client-supplied executable input. Swaps the positions of two panes.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe`, `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `other_pane_id` **required** | string | the other pane id, such as %2 |
| `pane_id` **required** | string | one pane id, such as %1 |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `withPaneId` **required** | string |

### `wait_for_channel`

Change tmux state; no client-supplied executable input. Waits on tmux's channel state with a bounded timeout.

Belongs to the `manage` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `change` |
| `outputClasses` | `tmux-metadata` |
| `mayExposeSecrets` | `false` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Changes tmux state.

| Argument | Type | |
| --- | --- | --- |
| `channel` **required** | string | a server-wide tmux channel name |
| `drain_first` | boolean | consume a pending signal before waiting |
| `timeout` | number | seconds to wait before giving up |

| Returns | Type |
| --- | --- |
| `effectiveTimeoutSeconds` **required** | integer |
| `signalled` **required** | boolean |
| `timeoutClamped` | boolean |

### `wait_for_text`

Read pane output; accepts no client-supplied executable input. Returned content may be sensitive or untrusted. Waits for new pane output without accepting executable input.

Belongs to the `inspect` toolset.

| Capability | Manifest value |
| --- | --- |
| `processReach` | `none` |
| `tmuxEffects` | `observe` |
| `outputClasses` | `terminal-content`, `tmux-metadata` |
| `mayExposeSecrets` | `true` |
| `mayReturnUntrustedContent` | `true` |
| `amplifiesFutureInput` | `false` |
| `inputLiteralization` | none |
| `nestedAuthority` | none |
| `annotations` | `readOnlyHint=false`, `destructiveHint=true`, `idempotentHint=false`, `openWorldHint=true` |

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `pane_id` **required** | string | the pane id, such as %1 |
| `cursor` | string | a cursor returned by an earlier capture |
| `max_lines` | integer | maximum observed lines to return |
| `patterns` | array | text to wait for; any one ends the wait |
| `regex` | boolean | treat patterns and stops as regular expressions |
| `stop` | array | failure text; any one ends the wait |
| `timeout` | number | seconds to wait before giving up |

| Returns | Type |
| --- | --- |
| `effectiveTimeoutSeconds` **required** | integer |
| `elapsedSeconds` **required** | number |
| `found` **required** | boolean |
| `matchedAtEntry` **required** | boolean |
| `outcome` **required** | string |
| `paneId` **required** | string |
| `truncated` **required** | boolean |
| `entryNote` | string |
| `lines` | array |
| `matched` | string |
| `timeoutClamped` | boolean |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

<!-- toolsref:end -->
