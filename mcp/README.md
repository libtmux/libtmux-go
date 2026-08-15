# mcp

> [!WARNING]
> **Alpha.** Releases are tagged `-alpha` and the API is not settled. Pin an
> exact version.

Serve one tmux server to Model Context Protocol clients, built on the
[tmux module] and the [Go MCP SDK].

This is a consumer of the tmux module, not part of it. The tmux module takes no
runtime dependency; speaking MCP needs one, so this lives in its own module and
`go get` on the tmux module never pulls it in.

## Installing it

**Requirements:** Go 1.25+, and `tmux` on `$PATH`. The tmux module itself still
builds with Go 1.23; the MCP SDK this server speaks through is what raises the
floor here.

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@v0.0.1-alpha.1
```

That puts `libtmux-mcp` in `$(go env GOPATH)/bin`. An MCP client launches it as
a subprocess and speaks to it over stdin and stdout.

### Claude Code

```console
$ claude mcp add tmux -- libtmux-mcp
```

### Codex CLI

```console
$ codex mcp add tmux -- libtmux-mcp
```

### Gemini CLI

```console
$ gemini mcp add tmux libtmux-mcp
```

### Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "tmux": {
      "command": "libtmux-mcp",
      "args": ["-socket-name", "my-application"]
    }
  }
}
```

Every client takes the same flags. They select the tmux server once, at
startup, and a client cannot change it afterwards:

| Flag | Meaning |
| --- | --- |
| `-socket-name` | tmux socket name; empty uses tmux's default socket |
| `-socket-path` | explicit socket path; overrides `-socket-name` |
| `-binary` | tmux executable; empty resolves `tmux` through `PATH` |

The command resolves the tmux binary before serving, so a misconfigured path
fails at startup rather than on the first tool call. A tmux server that is not
running is not an error: tmux starts one on demand.

Three more flags answer questions without a client, which is what a
config entry that will not start actually needs:

| Flag | Answers |
| --- | --- |
| `-version` | which build this is |
| `-tools` | what a client would be offered, and how the safety level changed it |
| `-doctor` | which socket it reaches, what is on it, and whether it is running inside that tmux itself |

```console
$ libtmux-mcp -doctor -socket-name my-application
```

```
libtmux-mcp doctor
  tmux:    3.7b
  socket:  /tmp/tmux-1000/my-application
  holds:   1 sessions, 1 windows, 1 panes, 0 clients attached
  safety:  mutating
  caller:  pane %1 of this very server — acting on it acts on
           the terminal this process is running in
```

Both drive the server through its own protocol in memory, so they report what a
client sees rather than what the code intends.

## What it feels like

> **You:** Start the api server in a pane and tell me when it is listening.
>
> **Agent:** Created window `api` and started it. Waiting… it printed
> `listening on :8080` after four seconds. The pane is `%3` if you want to
> watch it.

> **You:** Run the auth tests in a new pane beside it.
>
> **Agent:** Split `%3` to the right and ran `pytest tests/auth`. Exit status
> 1 — two failures in `test_token_refresh`. Want the output?

The agent drives tmux directly. Nothing is copy-pasted out of a terminal, and
waiting for a server to come up is one call rather than a loop of screenshots.

## When it earns its keep

For one `tmux send-keys`, it does not. Shelling out is simpler and this server
is a subprocess in the way.

It earns its keep the moment the agent has to **wait**, **watch**, **inspect**,
or **avoid disturbing the terminal a person is using**. A dev server printing
its port, a test run finishing, a deploy log settling: those are where a
shell-out turns into a polling loop that reads the shell's echo of its own
command and reports success before anything happened. `run_command` waits for
the command and returns its exit status and output without reading the screen at
all; `wait_for_text` watches what a pane writes; `capture_since` returns what a
pane wrote since the last look rather than its whole screen again. That is the
difference — not more access to tmux, but a better place to put the control
loop, and a smaller bill for keeping it there.

## Knowing its own pane

A pane this server runs in is the terminal the conversation is happening
through. Killing it, clearing it, or typing into it is not like doing those
things to any other pane, so every pane the server reports carries `isCaller`:
true when it is this one, false when it is not, and null when the server is not
in a pane at all. `get_server_info` answers the same question directly.

tmux tells a process in a pane which pane it is through `TMUX_PANE` and `TMUX`,
and that is the first thing checked. It is not enough on its own: an MCP client
starts its servers with the environment it chooses, and most choose a curated
one carrying neither. So when the environment says nothing, the server finds
the pane the way it can always be found — it descends from whatever tmux
started in that pane, so the pane whose process is one of its own ancestors is
its own. That answer is the stronger one, because the panes were listed from
the server being addressed and no second socket exists for an id to collide on.

## Limiting what a client can do

`LIBTMUX_SAFETY` bounds the tools this server advertises:

| Value | Offers |
| --- | --- |
| `readonly` | only the tools that read tmux |
| `mutating` | those plus the ones that change it, and is the default |
| `destructive` | those plus the ones that end something: `kill_pane`, `kill_window`, `kill_session`, `kill_server` |

A tool above the level is never advertised, so no prompt reaches it, and a
batch cannot reach around the level either. The active level is stated in the
server instructions, so a client meeting a shorter tool list knows tools were
withheld rather than missing. The level is derived from each tool's own
annotations, so a tool declaring itself destructive is governed by having said
so.

## Prompts

Four, invoked by name from a client's own menu:

| Prompt | Is |
| --- | --- |
| `diagnose_pane` | work out what a pane is doing and why it is stuck |
| `watch_pane` | follow a pane across turns without re-reading it |
| `recover_pane` | get back a pane that has stopped answering |
| `set_up_workspace` | lay out a session for a piece of work |

Tools are verbs and resources are nouns; neither says what a person wants done.
A prompt is the job with its method attached, so someone who has never read the
tool list can ask for the thing they want. There are deliberately few: a prompt
per tool would be a second, worse tool list. `set_up_workspace` is withheld on
a read-only server, where it would be advice the server cannot carry out.

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
| `list_sessions` | Every session with its window count and attached clients |
| `list_windows` | Every window with its session, name, index, pane count, and active flag |
| `list_panes` | Every pane with its session, window, index, current command, active flag, and position |
| `list_servers` | Every tmux socket on this machine, with the addressed one marked |
| `get_server_info` | Which socket these tools address, and whether this server runs in one of its panes |
| `get_session_info` | One session's windows, working directory, and creation time |
| `get_window_info` | One window's size, layout string, and panes |
| `get_pane_info` | One pane's process, exit status, scrollback size, and mode, without its contents |
| `find_pane_by_position` | Report the pane bordering one side of another |
| `display_message` | Expand a tmux format string, for anything no tool here answers |

### Reading what a pane holds

| Tool | Does |
| --- | --- |
| `capture_pane` | What a pane holds: its screen, or its scrollback too |
| `capture_since` | Only what a pane wrote since the cursor a previous call returned |
| `snapshot_pane` | One pane's contents with its state, in one call |
| `search_panes` | Which panes show some text, and the lines that showed it |
| `clear_pane` | Clear a pane's screen, and its scrollback when asked |
| `pipe_pane` | Send everything a pane writes to a shell command as well |

### Waiting instead of polling

| Tool | Does |
| --- | --- |
| `run_command` | Run a command in one pane, wait for it, and report its exit status and output |
| `wait_for_text` | Wait until a pane writes one of several patterns, or one of the failure markers you named |
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
| `rename_session` / `rename_window` / `set_pane_title` | Label what was built |
| `respawn_pane` | Restart what a pane runs, keeping the pane and its place |

### Settings, batches, and ending things

| Tool | Does |
| --- | --- |
| `show_option` / `set_option` | Read or set a tmux option at server, session, window, or pane scope |
| `show_environment` / `set_environment` | What new processes in a session will inherit |
| `show_hooks` | The commands tmux will run on its own; reading only |
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

### Waiting rather than looking

Prefer `run_command` to `send_keys` followed by `capture_pane`. A shell echoes
the command it was given, so a screen read finds the request rather than the
result and reports success before the command has run. `run_command` reads no
screen to decide the command is done: the command signals a tmux channel when it
ends and records its status. It also returns what the command printed, read from
marks the wrapper records inside the pane, so a caller does not have to guess
where in the scrollback its command began.

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

`send_keys` and `run_command` both take `suppressHistory`, which prefixes the
command with a space so a shell told to ignore such lines keeps it out of its
history. An agent typing into a person's pane otherwise fills their history with
commands they never ran.

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
which call failed.

## Recipes

Four jobs, in the order the calls actually go. The tool list says what exists;
these say what to reach for and what goes wrong.

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

### Recover a pane that stopped answering

**Situation.** A `run_command` timed out. Every later one in that pane times out
too.

**Discover.** `get_pane_info`. Look at `currentCommand` and `inMode`.

**Decide.** A shell in `currentCommand` means the command is still going and you
were impatient. Anything else means the pane is busy and read your command as
that program's input. `inMode` true means the pane is in copy mode and never
saw your keys at all.

**Act.** For a busy pane, `send_keys` with `C-c`. For copy mode,
`exit_copy_mode`. Then re-run.

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
indexes and positions. They never report what a pane is *showing* —
`search_panes` and `capture_pane` do that.

## Troubleshooting

**Ask the server first.** `-doctor` answers most of what follows without a
client in the way:

```console
$ libtmux-mcp -doctor -socket-name my-application
```

**The client shows no tmux tools.** The server never started. Run the exact
command from your client's config by hand: a bad `-binary`, or a path that is
not on the client's `PATH`, fails at startup and says so.

**Tools are missing rather than failing.** `LIBTMUX_SAFETY` withheld them.
`-tools` prints what is actually offered and at which level; the level is also
stated in the server instructions the client already received.

**It reaches the wrong tmux.** `-doctor` names the socket it addresses and
lists the others on the machine. A client's environment is not your shell's:
`TMUX_TMPDIR` set in your profile is not set for a server the client spawned.

**A command works in your shell but not through a tool.** The pane's shell is
not your shell — it has the environment tmux gave it when it started.
`show_environment` reports what a *new* pane would inherit, which is not what
an already-running one has.

## An agent-shaped program

`examples/agent-workflow` is the whole loop in one file: it works out which
pane it is running in, splits it, runs a command in the new pane and waits for
the exit status, then reports the layout.

```console
$ go run ./examples/agent-workflow -socket-name my-application
```

Run from inside the tmux server it drives, it finds its own pane:

```
tmux 3.7b on /tmp/tmux-1000/my-application
running in pane %1
split into %2
exit 0, 2 lines of output
  | tmux 3.7b
  | ready
window 120x40, layout 87f2,120x40,0,0{71x40,0,0,1,48x40,72,0,2}
  %1 at 0,0 71x40  <- this program's pane
  %2 at 72,0 48x40
```

The client and server are joined in memory, so it is one program rather than
two. The tool names and arguments are the same ones a client sees over stdin
and stdout.

## Embedding it

```go
import (
    sdk "github.com/modelcontextprotocol/go-sdk/mcp"
    tmuxmcp "github.com/libtmux/libtmux-go/mcp"
)
```

```go
server := tmuxmcp.NewServer(tmux.NewServer(tmux.ServerOptions{SocketName: "app"}))
session, err := server.Connect(ctx, transport, nil)
```

`NewServer` returns the SDK's `*sdk.Server`, so the usual SDK options,
middleware, and transports apply. This package is named `mcp` and so is the
SDK's, so a file using both has to rename one of them.

## Developing on it

Run the server by hand to drive it yourself or to point the MCP Inspector at
it:

```console
$ go run ./cmd/libtmux-mcp -socket-name my-application
```

Testing it in a real client is better than driving it by hand, and
`mcp-swap` does the rewiring:

```console
$ go run ./cmd/mcp-swap status
```

```console
$ go run ./cmd/mcp-swap use-local --dry-run
```

```console
$ go run ./cmd/mcp-swap use-local
```

```console
$ go run ./cmd/mcp-swap revert
```

It points the agent CLIs on this machine at a build of this server and puts
them back. It writes only the `tmux` entry, only in global config, and it
writes every client it knows:

| Client | Config | Format |
| --- | --- | --- |
| claude | `~/.claude.json` | JSON |
| cursor | `~/.cursor/mcp.json` | JSON |
| gemini | `~/.gemini/settings.json` | JSON |
| antigravity | `~/.gemini/config/mcp_config.json` | JSON |
| codex | `~/.codex/config.toml` | TOML |
| grok | `~/.grok/config.toml` | TOML |
| opencode | `$XDG_CONFIG_HOME/opencode/opencode.jsonc` | JSONC |

All of them, not the JSON ones only. The entry has one name across every
client, so swapping some of them leaves two different servers answering to
`tmux` and nothing saying which client got which.

The TOML and JSONC files are edited in place rather than parsed and rewritten.
They hold other servers, other settings, and comments explaining why something
is set the way it is; a decode-and-write reformats all of that. So the entry's
bytes are located and replaced, and every other byte is left alone. Keys this
tool does not write survive — grok's `enabled`, for instance — and so does the
entry's environment, because `LIBTMUX_SAFETY` is configuration rather than a
choice of build.

Each config is copied beside itself before the first change. The first copy is
kept rather than the latest, so `revert` lands on what was there before any
swapping started, however many times you have swapped since.

`--mode` chooses which build:

| Mode | Runs | Good for |
| --- | --- | --- |
| `dev` (default) | `go -C <module> run ./cmd/libtmux-mcp` | an edit is live for the next call, with nothing to rebuild |
| `build` | a binary compiled once into your cache directory | a plain exec, pinned to the code it was built from |
| `installed` | `libtmux-mcp` from `PATH` | whatever `go install` put there |
| `released` | `go run <module>/cmd/libtmux-mcp@<ref>` | a published version; `--ref` picks one, default `latest` |

Before writing anything, the chosen build is started once and asked to complete
an MCP handshake. A build error, a missing binary, or a version the module
proxy has never heard of otherwise lands in every config at once and shows up
later as a server that will not start, separately, in each client. Pass
`--no-preflight` to skip it when offline.

`released` needs the Go module to be published under a tag the proxy can
resolve. This repository has no such tag yet, so `@latest` resolves nothing and
the preflight says so rather than writing an entry that cannot start.

Driving it by hand still works:

```console
$ go run ./cmd/libtmux-mcp -socket-name my-application
```

It reads JSON-RPC from stdin, so a pipe that closes immediately ends the server
before it answers. Hold stdin open while waiting for replies. See
[`AGENTS.md`](../AGENTS.md) for what else is worth knowing before
testing this by hand.

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
