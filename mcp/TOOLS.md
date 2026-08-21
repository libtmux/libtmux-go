# Tool reference

Every tool this server exposes, what a client sends it, and what comes back.
Reference material: read it by search rather than start to finish.

For installing the server and pointing a client at it, see
[`README.md`](README.md).

**Contents** — [Prompts](#prompts) · [Resources](#resources) · [Tools](#tools) ·
[Recipes](#recipes) · [Gotchas](#gotchas) · [Logs](#logs) ·
[Keeping a record](#keeping-a-record) · [Error handling](#error-handling)

## Prompts

Invoked by name from a client's own menu:

| Prompt | Is |
| --- | --- |
| `diagnose_pane` | work out what a pane is doing and why it is stuck |
| `watch_pane` | follow a pane across turns without re-reading it |
| `recover_pane` | get back a pane that has stopped answering |
| `set_up_workspace` | lay out a session for a piece of work |

Tools are verbs and resources are nouns; neither says what a person wants done.
A prompt is the job with its method attached, so someone who has never read the
tool list can ask for the thing they want. There are deliberately few: a prompt
per tool would be a second, worse tool list. `set_up_workspace` and
`recover_pane` are withheld on a read-only server, where they would be advice
the server cannot carry out.

Most of what this server knows about which tool to use, and in what order, is
in these prompts. A client that does not implement the prompts protocol shows a
model none of it — so `LIBTMUX_MCP_PROMPTS_AS_TOOLS=1` also offers them as one
`get_recipe` tool, whose own description names them all. It is off by default,
because a server that offers both describes the same things twice, and the
tool list is the expensive place to say anything.

## Resources

The tmux hierarchy is addressable as well as callable, so a person can attach a
pane to a conversation and a client can browse without knowing a tool name:

| URI | Is |
| --- | --- |
| `tmux://sessions` | every session, with its windows and panes |
| `tmux://sessions/{name}/windows` | the windows of one session |
| `tmux://windows/{id}/panes` | the panes of one window |
| `tmux://panes/{id}` | one pane's identity, position, and command |
| `tmux://panes/{id}/content` | what one pane is showing, as text |

An id appears **without** its sigil: `tmux://panes/1`, not `tmux://panes/%1`.
A percent sign begins an escape in a URI, so the sigil form is not a URI a
client can parse. The sigil form is accepted if percent-encoded.

A client filling in one of these is offered the values that exist: the panes
and windows tmux has, narrowing to a session once one is chosen. Prompt
arguments complete the same way. MCP has no completion for tool arguments, so
this reaches the resources and prompts only.

Every resource only reads, so `LIBTMUX_SAFETY` never withholds one: a server
offering no tool that changes tmux can still be browsed.

A client that can subscribe may subscribe to any of these instead of re-reading
them. tmux already publishes what a subscription needs — a control-mode
connection reports every byte a pane writes and every structural change — so a
subscriber is told a pane changed rather than asking whether it did. One tmux
connection serves every subscription, opening with the first and closing with
the last, and notifications about one resource are coalesced so a pane printing
a build log does not become a notification per line.

## Tools

Every tool carries MCP annotations, so a client can act on what a call does
without reading its description: a title, `readOnlyHint`, `destructiveHint`,
`idempotentHint`, and `openWorldHint`. The safety level is derived from them, so
a tool that declares itself destructive is governed by having said so.

Most tools take an optional `paneId`, `windowId`, or `sessionName`. Omitting it
means the active pane, the current window, or the only session — refused rather
than guessed when there is more than one candidate — so a client that has not
read a listing does not spend a call on one.

### Finding out what is there

| Tool | Does |
| --- | --- |
| `list_sessions` | Sessions with their window count and attached clients; narrow with `name` or `attached` |
| `list_windows` | Windows with their session, name, index, pane count, and active flag; narrow with `sessionName`, `name`, or `active` |
| `list_panes` | Panes with their session, window, index, current command, active flag, and position; narrow with `sessionName`, `windowId`, `command`, `pathUnder`, `dead`, or `active`, and add per-pane state with `detail: full` |
| `list_servers` | The tmux servers running on this machine, with the addressed one marked; `includeDead` adds the socket files tmux left behind |
| `get_server_info` | Which socket these tools address, who is attached to it, whether this server runs in one of its panes, and tmux's own message log with `includeMessages` |
| `get_session_info` | One session's windows, working directory, and creation time |
| `get_window_info` | One window's size, layout string, and panes |
| `get_pane_info` | One pane's process, exit status, scrollback size, and mode, without its contents |
| `find_pane_by_position` | Report the pane bordering one side of another |
| `display_message` | Expand a tmux format string, for anything no tool here answers |

### Reading what a pane holds

| Tool | Does |
| --- | --- |
| `capture_pane` | What a pane holds: its screen, or its scrollback too; `styles` keeps colour |
| `capture_since` | Only what a pane wrote since the cursor a previous call returned |
| `snapshot_pane` | One pane's contents with its state, in one call |
| `search_panes` | Which panes show some text, and the lines that showed it |
| `clear_pane` | Clear a pane's screen, and its scrollback when asked |
| `pipe_pane` | Send everything a pane writes to a shell command as well |

### Waiting instead of polling

| Tool | Does |
| --- | --- |
| `run_command` | Run a command in one pane, wait for it, and report its exit status and output; `detach` returns a `jobId` instead of waiting |
| `get_job` | Collect a detached command: whether it finished, its exit status, and its output |
| `wait_for_text` | Wait until a pane writes one of several patterns, one of the failure markers you named, or `idleSeconds` of quiet |
| `wait_for_channel` | Wait until something signals a tmux wait-for channel |
| `signal_channel` | Signal a channel, releasing whoever waits on it |

### Putting something into a pane

| Tool | Does |
| --- | --- |
| `send_keys` | Type into one pane and press Enter; tmux key names such as `C-c` are read |
| `send_keys_batch` | Send tmux key names in order with no Enter, for driving a pager or an editor |
| `paste_text` | Deliver text exactly, with no tmux key names read |
| `enter_copy_mode` / `exit_copy_mode` | Put a pane into tmux's copy mode and back |
| `load_buffer` / `paste_buffer` / `show_buffer` / `delete_buffer` | Stage text in a tmux buffer, deliver it, read it back, remove it |

### Making and arranging

| Tool | Does |
| --- | --- |
| `create_session` | Start one detached session |
| `create_window` | Add one window to a session, returning its id and first pane |
| `split_window` | Divide one pane, placing the new one below, above, right, or left |
| `build_workspace` | Create a session from a tmuxp-style YAML document |
| `select_window` / `select_pane` | Make a window or pane the current one |
| `select_layout` | Arrange a window's panes, or restore a layout string |
| `resize_pane` / `resize_window` | Set a size in cells, or zoom a pane |
| `swap_pane` / `move_window` | Exchange two panes, or move a window's place or session |
| `move_pane` | Move a pane into another window, or break it out into one of its own |
| `rename_session` / `rename_window` / `set_pane_title` | Label what was built |
| `respawn_pane` | Restart what a pane runs, keeping the pane and its place |

### Settings, batches, and ending things

| Tool | Does |
| --- | --- |
| `show_option` / `set_option` | Read or set a tmux option at server, session, window, or pane scope |
| `show_environment` / `set_environment` | What new processes in a session will inherit |
| `show_hooks` | The commands tmux will run on its own, all of them or one by `name`; reading only |
| `call_readonly_tools_batch` | Run several reading tools in one request |
| `call_mutating_tools_batch` | Run several tools in one request, stopping at the first failure |
| `call_destructive_tools_batch` | The same, including the ones that end something |
| `kill_pane` / `kill_window` / `kill_session` / `kill_server` | End one pane, window, session, or the whole server |

### Reading a pane repeatedly

`capture_pane` returns the whole screen, most of which a client watching a pane
across turns has already read. `capture_since` returns what the pane wrote after
a cursor a previous call handed out, so a quiet pane costs nothing and a busy
one costs its new lines. The cursor is opaque: it carries where tmux stood and a
fingerprint of the rows there, because tmux renumbers every row when it trims
the oldest, and a row number alone would silently drift.

tmux discards scrollback, so "what is new" is not always answerable. When the
anchor has been trimmed away the reply is the current screen with `linesMissed`
set, which says the client's record of the pane has a gap in it rather than
implying it is whole.

### How large an argument can be

What a caller sends has a ceiling, and where it sits depends on how this server
is reaching tmux at the time.

tmux's own client packs a command into one frame of 16,384 bytes, so roughly
16.3 kB of arguments is all it carries; past that tmux answers `command too
long`. A control connection does not go through that frame, so while this
server holds one -- which is the ordinary case -- a much larger value goes
through. When it has fallen back to running a tmux process per command, which
happens after the tmux server it was talking to restarts, the frame limit
applies again and a very large `value`, `title`, `format` or `name` is refused.

Nothing here needs a 16 kB argument, so this matters mainly as an explanation
for a call that worked earlier in a session and stops working after a restart.
Pane text is unaffected: `load_buffer` and `paste_text` do not pass their
content as a command argument.

### Bounded replies

Every tool that returns pane text keeps the last lines within a bound and
reports what it dropped. A pane's scrollback is measured against a terminal's
memory and the reply is measured against a caller's context, and the caller
cannot tell which it is about to get before it asks. Raise the bound with
`maxLines`; ask for scrollback with `includeHistory`. There is a ceiling rather
than an off switch, because a caller asking for everything is asking precisely
when it does not know how much there is — `capture_since` is how to read past
it.

Pane text also arrives as a text content block rather than only as JSON. A
terminal reads better as a terminal than as an array of quoted strings.

Under all of that is a flat cap on any tool result, well above what the
per-tool bounds allow. Nothing should ever reach it; it exists for the tool
added later that forgets to bound itself, because the cost of forgetting is
paid by whoever is talking to the model. A reply that hits it is refused with a
message naming the tool, which is a defect report rather than a limit to work
around.

### Narrowing a listing

A listing used to answer with the whole server, which is the answer to a
question nobody asks: a caller wants the pane running the dev server, not the
forty around it, and pays for the difference in context it cannot get back. The
listings of sessions, windows, and panes take criteria, combined with AND, and every reply reports
the `total` it selected from — so a caller can tell a filter that matched one
pane from a server that only has one.

Measured against a real 18-pane server over stdio: the unfiltered listing is
7.3 kB, one session's panes 2.2 kB, and `{"command": "vim"}` 535 bytes.

The criteria are matched against the snapshot the tool already takes, not
pushed into tmux as a `-f` expression. tmux's filter language is a format, and
a format containing `#(...)` runs it as a shell command — reliably so against a
long-lived client, which is what this server holds. Compiling a caller's words
into that language would make every listing tool an execution vector while it
still reported `readOnlyHint: true`.

`detail: full` adds each matching pane's exit status, working directory, title,
history size, and whether it is in a mode that swallows keys. Every value comes
from the snapshot already taken, so it costs no further tmux command. It is how
to supervise several panes in one call without capturing any of them: a history
size that has not moved since the last reading means that pane wrote nothing.

### Not waiting at all

`run_command` waits, which is right when the answer is the point and wrong when
the command is a build and the caller has reading to do. `detach` returns a
`jobId` as soon as the command is typed; `get_job` collects it later — at once
to ask whether it has finished, or with `timeoutSeconds` to wait a bounded
while. The command is identical either way: the same wrapper records the same
exit status against the same tmux channel, and only who waits changes.

Collecting is idempotent. The first read that finds a status keeps it and
releases the files behind it; every later read is answered from what was kept.
Asking twice is how a caller checks on something, and a handle that stopped
answering once used would report a finished command as a lost one. The last 32
handles are kept.

A handle does not outlive the process that issued it, and it says which process
that was, so a handle presented after a restart is refused with that reason
rather than blamed on newer commands. The command itself is unaffected — it is
running in a pane, not in this server — so a lost handle is recovered by
reading the pane. Clients that keep one server per session, which is all of
them in ordinary use, never see this; a client that respawns the server per
call, as the MCP Inspector's `--cli` does, sees it every time.

### Waiting rather than looking

Prefer `run_command` to `send_keys` followed by `capture_pane`. A shell echoes
the command it was given, so a screen read finds the request rather than the
result and reports success before the command has run. `run_command` reads no
screen to decide the command is done: the command signals a tmux channel when it
ends and records its status. It also returns what the command printed, read from
marks the wrapper records inside the pane, so a caller does not have to guess
where in the scrollback its command began.

What the command printed is read back off the pane, so it is what tmux can
still see there. A tab is the one character that survives this only on newer
tmux: before 3.6 a tab moved the cursor and left spaces in the grid, and
`capture-pane` had nothing to reconstruct it from, so `printf 'a\tb'` is
reported as `a` and `b` separated by spaces. tmux 3.6 preserves tabs for
capture, and the same command is reported with its tab from there on. Nothing
in this server can recover it on the older releases; a command whose output is
parsed by column should be given a separator of its own.

What comes back is the command's own output and nothing else. Rows the terminal
wrapped are rejoined, so one line printed is one entry however narrow the pane
is, and the row the shell draws its prompt into is left out. When the output
cannot be read at all, `outputUnavailable` says why, which is what separates a
command that printed nothing from a pane that could not be reached.

Your command is written to a file and sourced, never typed. A shell's line
editor acts on what arrives as keys, so a command carrying a tab would ask it to
complete a filename and a command carrying `C-c` would be acted on rather than
run. Nothing you send crosses that line editor, whatever it contains.

A command that outlasts its wait leaves the pane holding it, and every later
`run_command` there times out too; send `C-c` with `send_keys` to get the pane
back.

For output the client did not author — a service announcing it is ready —
`wait_for_text` counts what the pane has already shown and then reads what it
produces next, so a program that announced itself before the wait began is still
found; `sinceEntry` turns that off for a client asking whether something has
happened *again*. Pass `stop` with the failure markers already known, and a run
that failed returns in milliseconds instead of at the deadline. The reply says
why the wait ended rather than only whether it succeeded.

When you cannot predict what finishing prints, `idleSeconds` ends the wait once
the pane has been quiet that long. The window is measured from the pane's own
output, so a program still working is not mistaken for one that has finished,
and an `idle` outcome with no lines means the pane never wrote at all — which
is what a command that was never started looks like.

Every wait is bounded by a ceiling, 300 seconds by default and set by
`LIBTMUX_MCP_WAIT_MAX_SECONDS`. A larger `timeoutSeconds` is clamped rather
than refused: the reply carries `effectiveTimeoutSeconds`, and
`timeoutClamped` when the ceiling was the lower of the two. The ceiling bounds
the caller rather than the transport — these tools await throughout, so a long
wait blocks nothing else. What it costs is the turn it happens in, because MCP
gives a caller no way to change its mind once a call is in flight.

`send_keys` and `run_command` both take `suppressHistory`, which prefixes the
command with a space so a shell told to ignore such lines keeps it out of its
history. An agent typing into a person's pane otherwise fills their history with
commands they never ran.

### The pane this server is running in

Every pane summary carries `isCaller`, and a pane where it is true is the one
this server runs in: typing into it reaches the terminal the conversation is
happening in, and no later call undoes it. That was the whole of the
protection, and a note in a reply is something a model with forty tools and a
task does not always read.

So a write to that pane asks first, through MCP elicitation, and a decline
fails the call. A write here is what reaches the person's keyboard or their
shell: `send_keys`, `send_keys_batch`, `paste_text`, `paste_buffer`,
`run_command`, `respawn_pane`, `clear_pane`, `kill_pane`, and
`enter_copy_mode`, which takes their keystrokes away from their shell.
Splitting the pane is not one — finding your own pane and making room beside it
is the ordinary opening move — and neither is `exit_copy_mode`, which is the
way out of the one mode that is.

A client that did not declare the elicitation capability gets the behaviour it
had before — the write goes through with `isCaller` reported beside it. This is
a guard rail, not a boundary: a caller with `send_keys` can run anything the
user can, and refusing every write on every client that cannot be asked would
break them all to enforce something that was never enforceable.

### Text, keys, and the difference

`send_keys` looks up tmux key names, so text containing `Escape` or `C-c` is not
delivered as itself. Anything a client did not write by hand — a file, a
message, a generated command — goes through `paste_text`, which delivers bytes
rather than keys. `send_keys_batch` is the middle case: key names in order with
no Enter, for driving a program that reads keys rather than lines.

### Batches

A batch runs its calls in order, each after the one before it finished. Every
call is checked against the batch's tier before any of them runs, so a batch
that would have ended something halfway through is refused whole rather than run
up to that point. A batch dispatches through the same table registration builds,
so a tool the safety level withheld is not reachable by naming it in a list. The
batch tools are not themselves batchable: nesting one inside another buries
which call failed, and a batch asked to do it says so rather than claiming the
tool does not exist.

All three stop at the first failure. `results` holds one entry per call
attempted, ending with the one that failed and its error; `completed` counts the
ones before it; and `skipped` names the calls that never ran, in order. That
last one matters most for the mutating and destructive batches: tmux has no
transaction, so what already ran stays, and knowing what did *not* run is how a
caller decides what to do next.

## Recipes

Jobs in the order the calls actually go. The tool list says what exists; these
say what to reach for and what goes wrong.

### Start a service and wait for it before running dependent work

**Situation.** A session with no server running. The person wants integration
tests, and the tests need a live API.

> Start the API server in my backend session and run the integration tests once
> it is ready.

**Discover.** `list_panes` for the session — is something already serving? Then
`search_panes` for `listening`. Nothing, so nothing to reuse.

**Decide.** A pane of its own, so the server's output stays separate from the
tests'.

**Act.** `split_window`, then `send_keys` with `npm run serve` in the new pane.
Then `wait_for_text` on that pane with `patterns: ["Listening on"]` and
`stop: ["EADDRINUSE", "Error:"]`. Once it resolves, `run_command` in the
original pane and read `exitStatus`.

**The non-obvious part.** `wait_for_text` replaces `sleep`. The server might
take two seconds or twenty, and the agent adapts to either. `stop` is what
keeps a failed start from costing the whole timeout — without it, a port
collision waits out the full deadline before telling you anything.

### Find the failing pane without opening random terminals

**Situation.** Several jobs across panes. One went red.

> Which one failed, and why?

**Discover.** `search_panes` with `text: "FAILED"` — or `error:`, or
`Traceback`. It returns the panes *and the lines that matched*, so one call
answers both which and why.

**Decide.** If the matched lines are enough, stop. They usually are.

**Act.** When they are not, `snapshot_pane` on the pane it named, with
`includeHistory` if the failure has scrolled off.

**The non-obvious part.** Do not capture each pane in turn and search the
results yourself: that is one call per pane, and every pane's contents pass
through your context on the way. `search_panes` does the matching where the
text already is.

### Watch a long job across several turns

**Situation.** A build that will outlast this exchange.

> Keep an eye on the build and tell me when it breaks.

**Discover.** `capture_since` with no cursor. You get what the pane shows now,
and a cursor.

**Decide.** Keep the cursor. It is the whole state you need.

**Act.** Every later turn, `capture_since` with that cursor. You get only what
was written since, and a fresh cursor to keep instead.

**The non-obvious part.** `capture_pane` in a loop re-sends the same screen
every turn and cannot tell you whether anything changed. Also: check
`linesMissed`. True means tmux discarded scrollback between your reads and your
record of that pane has a hole in it — which is worth saying out loud rather
than quietly summarising over.

**When it is not your build.** If the pane is running something you started,
`run_command` with `detach` and `get_job` is cheaper still: no cursor to carry,
and an exit status at the end.

### Run a build without spending the turn on it

**Situation.** A test suite that takes minutes, and other work to do meanwhile.

> Run the suite and start reading the failing module while it goes.

**Discover.** Nothing to discover: `run_command` with `detach: true` returns a
`jobId` as soon as the command is typed.

**Decide.** Spend the turn on the other work. Come back with `get_job`.

**Act.** `get_job` with the handle and no timeout reports whether it has
finished and what the pane is running if it has not. With `timeoutSeconds` it
waits that long. A finished job carries the exit status and everything the
command printed.

**The non-obvious part.** Asking again is free and gives the same answer: the
first read that finds a status keeps it. And `detach` does not change the
command — the same wrapper records the same status against the same channel, so
a detached run and a waited one report identically.

### Check on eight panes without reading any of them

**Situation.** A workspace you built, several panes into a long job.

> Which of those are still going?

**Discover.** `list_panes` with `detail: full`, narrowed to the session you
built.

**Decide.** `status.dead` with `status.exitStatus` says which finished and how.
`status.historyLines` compared against your last reading says which wrote
anything since.

**Act.** Capture only the panes whose history moved.

**The non-obvious part.** All of it comes from the snapshot the listing already
takes, so it is one tmux command for every pane, not one per pane — and no
pane's contents are read, so nothing is paid for output already seen.

### Recover a pane that stopped answering

**Situation.** A `run_command` timed out. Every later one in that pane times out
too.

**Discover.** `get_pane_info`. Look at `currentCommand`, `inMode`, and `dead`.

**Decide.** A shell in `currentCommand` means the command is still going and you
were impatient. Anything else means the pane is busy and read your command as
that program's input. `inMode` true means the pane is in copy mode and never
saw your keys at all.

**Act.** For a busy pane, `send_keys` with `C-c`. For copy mode,
`exit_copy_mode`. Then re-run.

A pane whose program has exited reads no keys at all, so it never reaches this
situation: `run_command` refuses it outright and names `respawn_pane`, rather
than waiting out a timeout it cannot win.

**The non-obvious part.** A pane left holding a command poisons every later
`run_command` there, and the symptom — a timeout — looks identical to a slow
command. `running` in the timeout result is what tells the two apart, which is
why it is in the reply at all.

## Gotchas

**Reading a pane right after sending keys is a race.** `send_keys` returns when
tmux accepts the keystrokes, not when the command finishes, so a capture
straight afterwards usually catches the shell echoing your own command back.
For commands you author, `run_command`. For output you did not author,
`wait_for_text`.

**A busy pane times out forever.** See the recipe above. `send_keys` with `C-c`
is the way back.

**Copy mode swallows keys.** A pane in copy mode reads keys as tmux's own, so
`send_keys` reaches tmux rather than the shell and nothing appears to happen.
`get_pane_info` reports `inMode`; `exit_copy_mode` returns it.

**Window names are not unique.** Two sessions can both have a window called
`editor`. Window ids (`@1`) are unique; names are for people.

**Pane ids are unique but not permanent.** `%3` is unambiguous while it exists
and may be reused after the pane goes. A `capture_since` cursor notices —
it records the pane's process too, and refuses rather than reading a different
program's output as the same one's.

**Suppressing shell history is best effort.** `suppressHistory` prefixes a
space, which a shell configured with `HIST_IGNORE_SPACE` keeps out of its
history. A shell not configured that way keeps it anyway. It is a courtesy to
the person whose terminal this is, not a guarantee.

**Listing is not reading.** `list_panes` and `list_windows` report names,
indexes and positions, and with `detail: full` a pane's process state. They
never report what a pane is *showing* — `search_panes` and `capture_pane` do
that.

**`run_command` runs in a subshell.** The command is wrapped in `( ... )` so
that one ending in `exit` ends the subshell rather than the pane's shell, which
would take the status recording with it. A consequence: `cd`, `export`, and
anything else that changes the shell itself does not outlive the call. Use
`send_keys` for those.

**A capture strips colour.** A program that reports whether it passed by
colouring one word says nothing at all once the colour is gone. `styles` keeps
tmux's escape sequences.

## Logs

A client that sets a logging level hears why a wait ended with nothing: which
pane, what it was running instead, how long it waited. The tool result says
what happened, the log says why, and a client that never sets a level is sent
nothing at all.

## Keeping a record

An operator handing this to a model has a reasonable question — what did it
actually do? `LIBTMUX_AUDIT` answers it: set it to `stderr`, or to a path to
append to, and every tool call is recorded as one JSON line.

```console
$ LIBTMUX_AUDIT=/tmp/tmux-mcp.log libtmux-mcp -socket-name my-application
```

```json
{"time":"...","level":"INFO","msg":"tool call","tool":"send_keys",
 "outcome":"ok","elapsedMillis":2,
 "arguments":{"paneId":"%0","command":{"len":42,"sha256":"c27651833c4e"}}}
```

The commands themselves are not in it. This server types into people's
terminals, and what gets typed contains what commands contain: a token pasted
into a deploy, a password in a connection string, the contents of a file. So an
argument is either an identifier — a pane id, a direction, a bound — and logged
as itself, or a payload, and logged as its length and a digest prefix. The same
payload twice gives the same digest, so a loop is visible, and no digest is the
payload.

The identifiers are an allowlist, so a field added later is a payload until
somebody decides otherwise. It is off unless asked for: a server that writes
your command history to your terminal unbidden has answered a question nobody
put to it.

## Error handling

Reads use the tmux module's lenient default, so an unreachable server is an
empty result rather than a failure. `capture_pane` is an exception: it names one
pane, and a missing pane is a failure rather than emptiness. Writes use strict errors, because a
mutation that silently did nothing is worse than one that reports a problem.

A tmux failure reaches the client as tool error content carrying tmux's own
message, so a model can read what went wrong and choose a different call
instead of seeing an opaque protocol error:

```
tmux: command failed: kill-session exited 1: can't find session: no-such-session
```

[tmux module]: ../tmux/
[Go MCP SDK]: https://github.com/modelcontextprotocol/go-sdk
## Every tool

The entry for each tool, rendered from its own schema. The sections above say
when to reach for what; this says exactly what each one takes and returns.

<!-- toolsref -->

58 tools. Generated from the schemas by `go generate ./...`; edit the tools, not this.

### `build_workspace`

Create a session from a tmuxp-style YAML workspace document: windows, panes, layouts, and the command each pane runs, in one call.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `document` **required** | string | a tmuxp-style YAML workspace document |

| Returns | Type |
| --- | --- |
| `sessionId` **required** | string |
| `sessionName` **required** | string |

### `call_destructive_tools_batch`

Run several tools in one request, in order, including the ones that end something.

**Ends something.** Nothing brings it back, and it is withheld below the `destructive` safety level.

| Argument | Type | |
| --- | --- | --- |
| `calls` **required** | array | the calls to run, in order |

| Returns | Type |
| --- | --- |
| `completed` **required** | integer |
| `results` **required** | array |
| `skipped` **required** | array |

### `call_mutating_tools_batch`

Run several tools in one request, in order, including ones that change tmux.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `calls` **required** | array | the calls to run, in order |

| Returns | Type |
| --- | --- |
| `completed` **required** | integer |
| `results` **required** | array |
| `skipped` **required** | array |

### `call_readonly_tools_batch`

Run several reading tools in one request, in order.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `calls` **required** | array | the calls to run, in order |

| Returns | Type |
| --- | --- |
| `completed` **required** | integer |
| `results` **required** | array |
| `skipped` **required** | array |

### `capture_pane`

Read what one pane holds: its visible screen, or its scrollback too with includeHistory.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `endLine` | integer | last row to read, on the same scale as startLine |
| `includeHistory` | boolean | read scrollback as well as the visible screen |
| `joinWrapped` | boolean | rejoin lines the terminal wrapped, as tmux does it; a multi-row shell prompt can be joined to the line after it, so prefer run_command for output you ran yourself |
| `maxBytes` | integer | how many bytes to return at most, keeping the last lines |
| `maxLines` | integer | how many lines to return at most, keeping the last ones |
| `paneId` | string | a tmux pane id such as %1; empty reads the active pane |
| `sessionName` | string | which session's active pane to read when paneId is empty |
| `startLine` | integer | first row to read; 0 is the top of the screen and negatives are history |
| `styles` | boolean | keep colour and attribute escape sequences, for a program that says pass or fail in colour rather than in words |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `truncated` **required** | boolean |
| `lines` | array |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `capture_since`

Read only what a pane wrote since the cursor a previous call returned, and get a cursor for next time.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `cursor` | string | the cursor a previous capture_since returned; empty starts a new reading |
| `maxBytes` | integer | how many bytes to return at most, keeping the last lines |
| `maxLines` | integer | how many lines to return at most, keeping the last ones |
| `paneId` | string | the tmux pane id to read; the cursor already names it |
| `sessionName` | string | which session's active pane to read when paneId is empty |

| Returns | Type |
| --- | --- |
| `cursor` **required** | string |
| `lines` **required** | array |
| `linesMissed` **required** | boolean |
| `paneId` **required** | string |
| `truncated` **required** | boolean |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `clear_pane`

Clear a pane's screen, and its scrollback when asked.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `history` | boolean | also discard the pane's scrollback |
| `paneId` | string | the tmux pane id to clear; empty clears the active pane |
| `sessionName` | string | which session's active pane to clear when paneId is empty |

| Returns | Type |
| --- | --- |
| `historyCleared` **required** | boolean |
| `paneId` **required** | string |

### `create_session`

Start one detached session and return its id and name.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `command` | string | a command for the first window to run instead of a shell |
| `name` | string | the new session's name |
| `startDirectory` | string | the session's working directory |

| Returns | Type |
| --- | --- |
| `sessionId` **required** | string |
| `sessionName` **required** | string |

### `create_window`

Add one window to a session and return its id and the id of the pane tmux made with it.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `command` | string | a command for the window to run instead of a shell |
| `name` | string | the new window's name |
| `sessionName` | string | the exact session name to add the window to |
| `startDirectory` | string | the window's working directory |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `windowId` **required** | string |

### `delete_buffer`

Remove a buffer this server staged.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the buffer to remove, as load_buffer returned it |

| Returns | Type |
| --- | --- |
| `deleted` **required** | string |

### `display_message`

Ask tmux to expand one of its format strings, such as #{pane_current_path} or #{window_flags}.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `format` **required** | string | a tmux format string, such as #{pane_current_path} |
| `paneId` | string | the pane the format is about; empty uses the active pane |
| `sessionName` | string | which session's active pane to evaluate against when paneId is empty |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `value` **required** | string |

### `enter_copy_mode`

Put a pane into tmux's copy mode, where keys scroll and select rather than reaching the program in the pane.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `paneId` | string | the tmux pane id; empty uses the active pane |
| `scrollUp` | boolean | enter one page above the bottom |
| `sessionName` | string | which session's active pane to use when paneId is empty |

| Returns | Type |
| --- | --- |
| `inCopyMode` **required** | boolean |
| `paneId` **required** | string |

### `exit_copy_mode`

Return a pane from copy mode to passing keys to the program running in it.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `paneId` | string | the tmux pane id; empty uses the active pane |
| `sessionName` | string | which session's active pane to use when paneId is empty |

| Returns | Type |
| --- | --- |
| `inCopyMode` **required** | boolean |
| `paneId` **required** | string |

### `find_pane_by_position`

Report the pane bordering one side of another: above, below, left, or right.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `direction` **required** | string | the side to look toward: above, below, left, or right |
| `paneId` | string | the tmux pane id to look from; empty looks from the active pane |
| `sessionName` | string | which session's active pane to look from when paneId is empty |

| Returns | Type |
| --- | --- |
| `found` **required** | boolean |
| `geometry` **required** | object |
| `paneId` **required** | string |

### `get_job`

Collect a command started with run_command and detach.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `jobId` **required** | string | the handle a detached run_command returned |
| `maxBytes` | integer | how many bytes to return at most, keeping the last lines |
| `maxLines` | integer | how many lines of output to return at most, keeping the last ones |
| `timeoutSeconds` | integer | wait up to this long for the command to finish; zero reports whether it has and returns at once |

| Returns | Type |
| --- | --- |
| `command` **required** | string |
| `elapsedSeconds` **required** | number |
| `finished` **required** | boolean |
| `jobId` **required** | string |
| `paneId` **required** | string |
| `truncated` **required** | boolean |
| `effectiveTimeoutSeconds` | integer |
| `exitStatus` | integer |
| `output` | array |
| `running` | string |
| `timeoutClamped` | boolean |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `get_pane_info`

One pane's state without its contents: what it runs, its process id, whether that process has exited and with what status, how much scrollback there is, and whether the pane is in a mode that will eat the keys you send it.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `paneId` | string | the tmux pane id to describe; empty describes the active pane |
| `sessionName` | string | which session's active pane to describe when paneId is empty |

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

Which tmux socket these tools address, its version, how much it holds, and whether this MCP server is itself running in one of its panes.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `includeMessages` | boolean | add tmux's own server message log, which records what tmux refused and why |
| `maxBytes` | integer | how many bytes of log messages to return at most, keeping the most recent |
| `maxLines` | integer | how many log messages to return at most, keeping the most recent |

| Returns | Type |
| --- | --- |
| `alive` **required** | boolean |
| `attachedClients` **required** | array |
| `clients` **required** | integer |
| `insideThisServer` **required** | boolean |
| `panes` **required** | integer |
| `safetyLevel` **required** | string |
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

One session's windows, working directory, and when it was created.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `sessionName` | string | the exact session name; empty uses the only session |

| Returns | Type |
| --- | --- |
| `activeWindowId` **required** | string |
| `created` **required** | string |
| `path` **required** | string |
| `session` **required** | object |
| `windows` **required** | array |

### `get_window_info`

One window's size, layout string, and panes.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `sessionName` | string | which session's current window to describe when windowId is empty |
| `windowId` | string | the tmux window id to describe; empty describes the current window |

| Returns | Type |
| --- | --- |
| `height` **required** | integer |
| `layout` **required** | string |
| `panes` **required** | array |
| `width` **required** | integer |
| `window` **required** | object |
| `zoomed` **required** | boolean |

### `kill_pane`

End one pane and the program running in it.

**Ends something.** Nothing brings it back, and it is withheld below the `destructive` safety level.

| Argument | Type | |
| --- | --- | --- |
| `paneId` **required** | string | the tmux pane id to kill, such as %1 |

| Returns | Type |
| --- | --- |
| `killed` **required** | string |
| `windowEnded` **required** | boolean |

### `kill_server`

End the whole tmux server: every session, every window, every program.

**Ends something.** Nothing brings it back, and it is withheld below the `destructive` safety level.

| Argument | Type | |
| --- | --- | --- |
| `confirm` **required** | boolean | must be true; this ends every session on the server |

| Returns | Type |
| --- | --- |
| `sessionsKilled` **required** | integer |

### `kill_session`

End one session by its exact name, and every window and program in it.

**Ends something.** Nothing brings it back, and it is withheld below the `destructive` safety level.

| Argument | Type | |
| --- | --- | --- |
| `sessionName` **required** | string | the exact name of the session to kill |

| Returns | Type |
| --- | --- |
| `killed` **required** | string |

### `kill_window`

End one window and its panes.

**Ends something.** Nothing brings it back, and it is withheld below the `destructive` safety level.

| Argument | Type | |
| --- | --- | --- |
| `windowId` **required** | string | the tmux window id to kill, such as @1 |

| Returns | Type |
| --- | --- |
| `killed` **required** | string |
| `sessionEnded` **required** | boolean |

### `list_panes`

List tmux panes with their session, window, index, and current command.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `active` | boolean | keep only active panes, or only inactive ones |
| `command` | string | keep panes whose current command contains this text, ignoring case |
| `dead` | boolean | keep only panes whose process has exited, or only those still running |
| `detail` | string | how much to report per pane: standard, or full to add exit status, path, title, and history size |
| `pathUnder` | string | keep panes whose working directory is at or below this path |
| `sessionName` | string | list only this session's panes |
| `windowId` | string | list only this window's panes, such as @1 |

| Returns | Type |
| --- | --- |
| `panes` **required** | array |
| `total` **required** | integer |

### `list_servers`

The tmux servers running on this machine, with the one these tools address marked.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `includeDead` | boolean | include socket files with no server running, which tmux leaves behind when a server exits |
| `maxServers` | integer | how many servers to report at most; the target is always kept |
| `name` | string | keep servers whose name contains this text, ignoring case |

| Returns | Type |
| --- | --- |
| `searchedIn` **required** | string |
| `servers` **required** | array |
| `total` **required** | integer |
| `skipped` | integer |
| `unreachableNote` | string |

### `list_sessions`

Sessions with their name, window count, and how many clients are attached.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `attached` | boolean | keep only sessions a client is attached to, or only those nobody is watching |
| `name` | string | keep sessions whose name contains this text, ignoring case |

| Returns | Type |
| --- | --- |
| `sessions` **required** | array |
| `total` **required** | integer |

### `list_windows`

Windows with their session, name, index, pane count, and whether each is its session's current one.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `active` | boolean | keep only current windows, or only the others |
| `name` | string | keep windows whose name contains this text, ignoring case |
| `sessionName` | string | list only this session's windows |

| Returns | Type |
| --- | --- |
| `total` **required** | integer |
| `windows` **required** | array |

### `load_buffer`

Put text into a named tmux buffer, ready to paste into a pane once or several times.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `text` **required** | string | the text to store in the buffer |
| `name` | string | a name for the buffer; empty makes one up |

| Returns | Type |
| --- | --- |
| `bytes` **required** | integer |
| `name` **required** | string |

### `move_pane`

Move a pane into another window, or break it out into a window of its own by naming no destination.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `paneId` **required** | string | the tmux pane id to move, such as %1 |
| `direction` | string | where the moved pane goes in the destination: below, above, right, or left |
| `focus` | boolean | make the pane active where it lands, moving what a person sees |
| `name` | string | the new window's name when breaking the pane out |
| `percentage` | integer | the moved pane's share of the destination window, 1 to 100 |
| `toWindowId` | string | the window to move the pane into, such as @2; empty breaks it out into a new window |

| Returns | Type |
| --- | --- |
| `brokenOut` **required** | boolean |
| `paneId` **required** | string |
| `windowId` **required** | string |

### `move_window`

Move a window to another index, or into another session.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `windowId` **required** | string | the tmux window id to move |
| `index` | integer | the index to move it to; omit to use the next free one |
| `sessionName` | string | the session to move it to; empty keeps it in its own |

| Returns | Type |
| --- | --- |
| `index` **required** | integer |
| `session` **required** | string |
| `windowId` **required** | string |

### `paste_buffer`

Deliver a staged buffer into a pane as text, with no tmux key names read.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the buffer to paste, as load_buffer returned it |
| `delete` | boolean | remove the buffer once it has been pasted |
| `paneId` | string | the tmux pane id to paste into; empty uses the active pane |
| `sessionName` | string | which session's active pane to paste into when paneId is empty |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `paneId` **required** | string |

### `paste_text`

Deliver text into a pane exactly, with no tmux key names read.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `text` **required** | string | the text to deliver, taken literally |
| `bracket` | boolean | mark the text as pasted so an editor does not auto-indent it; on by default |
| `enter` | boolean | press Enter after the text |
| `paneId` | string | the tmux pane id to paste into; empty uses the active pane |
| `sessionName` | string | which session's active pane to paste into when paneId is empty |

| Returns | Type |
| --- | --- |
| `bytes` **required** | integer |
| `paneId` **required** | string |

### `pipe_pane`

Send everything a pane writes to a shell command as well as to the screen, such as "cat >> /tmp/build.log".

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `command` | string | a shell command to write the pane's output to, such as cat >> /tmp/build.log; empty stops piping |
| `paneId` | string | the tmux pane id to pipe; empty uses the active pane |
| `sessionName` | string | which session's active pane to pipe when paneId is empty |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `piping` **required** | boolean |

### `rename_session`

Give a session a name a person will recognise.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the new session name |
| `sessionName` | string | the exact session to rename; empty uses the only session |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `sessionId` **required** | string |

### `rename_window`

Name a window, which also stops tmux renaming it after whatever is running in it.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the new window name |
| `sessionName` | string | which session's current window to rename when windowId is empty |
| `windowId` | string | the tmux window id to rename; empty uses the current window |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `windowId` **required** | string |

### `resize_pane`

Set one pane's width or height in cells, or toggle it between its layout size and the whole window.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `height` | integer | the pane's new height in cells |
| `paneId` | string | the tmux pane id to resize; empty resizes the active pane |
| `sessionName` | string | which session's active pane to resize when paneId is empty |
| `width` | integer | the pane's new width in cells |
| `zoom` | boolean | toggle the pane between its size and the whole window |

| Returns | Type |
| --- | --- |
| `height` **required** | integer |
| `paneId` **required** | string |
| `width` **required** | integer |

### `resize_window`

Set a window's size in cells.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `height` | integer | the window's new height in cells |
| `sessionName` | string | which session's current window to resize when windowId is empty |
| `width` | integer | the window's new width in cells |
| `windowId` | string | the tmux window id to resize; empty uses the current window |

| Returns | Type |
| --- | --- |
| `height` **required** | integer |
| `width` **required** | integer |
| `windowId` **required** | string |

### `respawn_pane`

Restart what a pane runs, keeping the pane and its place in the layout.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `command` | string | a command to run instead; empty restarts what the pane ran before |
| `kill` | boolean | end a program that is still running first |
| `paneId` | string | the tmux pane id to restart; empty uses the active pane |
| `sessionName` | string | which session's active pane to restart when paneId is empty |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |

### `run_command`

Run a shell command in one pane, wait for it to finish, and return its exit status and its output.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `command` **required** | string | the shell command to run |
| `detach` | boolean | return a jobId at once instead of waiting; collect it later with get_job |
| `maxBytes` | integer | how many bytes of output to return at most, keeping the last lines |
| `maxLines` | integer | how many lines of output to return at most, keeping the last ones |
| `paneId` | string | the tmux pane id to run the command in; empty uses the active pane |
| `sessionName` | string | which session's active pane to run in when paneId is empty |
| `suppressHistory` | boolean | keep the command out of the shell's history by prefixing a space |
| `timeoutSeconds` | integer | how long to wait before giving up |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `timedOut` **required** | boolean |
| `truncated` **required** | boolean |
| `detached` | boolean |
| `effectiveTimeoutSeconds` | integer |
| `exitStatus` | integer |
| `jobId` | string |
| `linesMissed` | boolean |
| `output` | array |
| `outputUnavailable` | string |
| `running` | string |
| `timeoutClamped` | boolean |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `search_panes`

Find which panes show some text, and what they showed.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `text` **required** | string | text to look for in each pane's contents |
| `includeHistory` | boolean | search scrollback as well as the visible screens |
| `matchCase` | boolean | require the capitalisation to match |
| `maxMatchesPerPane` | integer | how many matching lines to report per pane |
| `maxPanes` | integer | how many matching panes to report at most |
| `regex` | boolean | read text as a regular expression |
| `sessionName` | string | search only this session's panes |

| Returns | Type |
| --- | --- |
| `panes` **required** | array |
| `morePanes` | integer |

### `select_layout`

Arrange a window's panes with one of tmux's presets (even-horizontal, even-vertical, main-horizontal, main-vertical, tiled, and the mirrored pair from tmux 3.5), spread them evenly, or restore a layout string read from get_window_info.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `layout` | string | even-horizontal, even-vertical, main-horizontal, main-vertical, tiled, main-horizontal-mirrored or main-vertical-mirrored from tmux 3.5, or a layout string from get_window_info |
| `sessionName` | string | which session's current window to arrange when windowId is empty |
| `spread` | boolean | give every pane an equal share of the space |
| `windowId` | string | the tmux window id to arrange; empty uses the current window |

| Returns | Type |
| --- | --- |
| `layout` **required** | string |
| `windowId` **required** | string |

### `select_pane`

Make one pane its window's active pane, which is where a person's keystrokes go.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `paneId` **required** | string | the tmux pane id to make active |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |

### `select_window`

Make one window its session's current window, which is what puts a person in front of a window this client created.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `windowId` **required** | string | the tmux window id to make current |

| Returns | Type |
| --- | --- |
| `windowId` **required** | string |

### `send_keys`

Type into one pane and press Enter.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `command` **required** | string | what to type, read as tmux key names: "C-c" interrupts the pane and "Escape" is a key rather than those letters; use paste_text for text to take literally |
| `paneId` | string | a tmux pane id such as %1; empty types into the active pane |
| `sessionName` | string | which session's active pane to type into when paneId is empty |
| `suppressHistory` | boolean | keep the command out of the shell's history by prefixing a space |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `sent` **required** | string |

### `send_keys_batch`

Send several tmux key names to a pane in order, with no Enter appended.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `keys` **required** | array | tmux key names to send in order, such as ["C-c", "q", "Enter"] |
| `literal` | boolean | send the keys as characters rather than as tmux key names |
| `paneId` | string | the tmux pane id to send to; empty uses the active pane |
| `sessionName` | string | which session's active pane to send to when paneId is empty |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `sent` **required** | integer |

### `set_environment`

Set or remove a variable for processes a session starts from now on.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the variable to set |
| `sessionName` | string | the session to write; empty uses the only session |
| `unset` | boolean | remove the variable instead of setting it |
| `value` | string | the value to set |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `sessionName` **required** | string |
| `unset` **required** | boolean |

### `set_option`

Set one tmux option.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the tmux option name, such as history-limit |
| `value` **required** | string | the value to set |
| `paneId` | string | the pane to set the option on |
| `scope` | string | server, session, window, or pane; empty sets at pane scope |
| `sessionName` | string | the session to set the option on |
| `windowId` | string | the window to set the option on |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `scope` **required** | string |
| `value` **required** | string |

### `set_pane_title`

Set the title tmux draws on a pane's border, which is how to label which pane is which in a layout someone else will read.

Changes tmux to a state. Repeating it is safe.

| Argument | Type | |
| --- | --- | --- |
| `title` **required** | string | the new pane title |
| `paneId` | string | the tmux pane id to title; empty uses the active pane |
| `sessionName` | string | which session's active pane to title when paneId is empty |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `title` **required** | string |

### `show_buffer`

Read back a buffer this server staged.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the buffer to read, as load_buffer returned it |
| `maxBytes` | integer | how many bytes to return at most, keeping the last lines |
| `maxLines` | integer | how many lines to return at most, keeping the last ones |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `truncated` **required** | boolean |
| `lines` | array |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

### `show_environment`

What new processes in a session will inherit.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `maxBytes` | integer | how many bytes of listing to return at most |
| `maxLines` | integer | how many variables to return at most |
| `name` | string | one variable to read, with its value; empty lists every name with its scope and no values. Several values at once: put several of these in call_readonly_tools_batch |
| `sessionName` | string | the session to read; empty uses the only session |

| Returns | Type |
| --- | --- |
| `sessionName` **required** | string |
| `truncated` **required** | boolean |
| `variables` **required** | array |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |
| `valuesWithheld` | boolean |

### `show_hooks`

The commands tmux will run on its own at a given scope.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `name` | string | report only this hook, such as pane-died; empty reports every hook in force |
| `paneId` | string | the pane to read hooks on |
| `scope` | string | server, session, window, or pane; empty reads at pane scope |
| `sessionName` | string | the session to read hooks on |
| `windowId` | string | the window to read hooks on |

| Returns | Type |
| --- | --- |
| `hooks` **required** | array |
| `scope` **required** | string |

### `show_option`

Read one tmux option at server, session, window, or pane scope.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `name` **required** | string | the tmux option name, such as history-limit |
| `paneId` | string | the pane to read the option on |
| `scope` | string | server, session, window, or pane; empty reads at pane scope |
| `sessionName` | string | the session to read the option on |
| `windowId` | string | the window to read the option on |

| Returns | Type |
| --- | --- |
| `name` **required** | string |
| `scope` **required** | string |
| `set` **required** | boolean |
| `value` **required** | string |

### `signal_channel`

Signal a tmux wait-for channel, releasing whoever waits on it.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `channel` **required** | string | the tmux wait-for channel to signal |

| Returns | Type |
| --- | --- |
| `channel` **required** | string |

### `snapshot_pane`

One pane's contents together with what it is, where it sits, and whether its process has exited.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `includeHistory` | boolean | read scrollback as well as the visible screen |
| `maxBytes` | integer | how many bytes to return at most, keeping the last lines |
| `maxLines` | integer | how many lines to return at most, keeping the last ones |
| `paneId` | string | the tmux pane id to describe; empty describes the active pane |
| `sessionName` | string | which session's active pane to describe when paneId is empty |

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

Divide one pane in two, placing the new one below, above, to the right of, or to the left of it, and return the new pane's id.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `command` | string | a command for the new pane to run instead of a shell |
| `direction` | string | where the new pane goes: below, above, right, or left |
| `paneId` | string | the tmux pane id to split; empty splits the active pane |
| `percentage` | integer | the new pane's share of the space, 1 to 100 |
| `sessionName` | string | which session's active pane to split when paneId is empty |
| `startDirectory` | string | the new pane's working directory |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |

### `swap_pane`

Exchange two panes' positions.

Changes tmux by a step. Repeating it compounds.

| Argument | Type | |
| --- | --- | --- |
| `paneId` **required** | string | one of the two panes to exchange |
| `withPaneId` **required** | string | the other pane to exchange it with |
| `keepFocus` | boolean | leave the active pane where it was |

| Returns | Type |
| --- | --- |
| `paneId` **required** | string |
| `withPaneId` **required** | string |

### `wait_for_channel`

Wait until something signals a tmux wait-for channel.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `channel` **required** | string | the tmux wait-for channel to wait on |
| `timeoutSeconds` | integer | how long to wait before giving up |

| Returns | Type |
| --- | --- |
| `signalled` **required** | boolean |

### `wait_for_text`

Wait until a pane writes one of several patterns, reading what the pane produces rather than its screen.

Reads only. Repeating it changes nothing.

| Argument | Type | |
| --- | --- | --- |
| `idleSeconds` | integer | end the wait once the pane has written nothing for this many seconds |
| `matchCase` | boolean | require the capitalisation to match |
| `maxBytes` | integer | how many bytes of output to return at most, keeping the last lines |
| `maxLines` | integer | how many lines of output to return at most, keeping the last ones |
| `paneId` | string | the tmux pane id to watch; empty watches the active pane |
| `patterns` | array | any one of these ends the wait; empty waits for any output |
| `regex` | boolean | read the patterns as regular expressions |
| `sessionName` | string | which session's active pane to watch when paneId is empty |
| `sinceEntry` | boolean | ignore what the pane already shows and match only new output |
| `stop` | array | markers of failure that end the wait early, such as "error:" |
| `timeoutSeconds` | integer | how long to wait before giving up |

| Returns | Type |
| --- | --- |
| `effectiveTimeoutSeconds` **required** | integer |
| `elapsedSeconds` **required** | number |
| `found` **required** | boolean |
| `matchedAtEntry` **required** | boolean |
| `outcome` **required** | string |
| `paneId` **required** | string |
| `truncated` **required** | boolean |
| `lines` | array |
| `matched` | string |
| `timeoutClamped` | boolean |
| `truncatedBytes` | integer |
| `truncatedLines` | integer |

<!-- toolsref:end -->
