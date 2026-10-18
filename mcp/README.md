<!-- libtmux-logo -->
<div align="center">
  <picture>
    <source srcset="assets/logo.svg" type="image/svg+xml">
    <img src="assets/logo.png" width="128" height="128" alt="libtmux for Go MCP">
  </picture>
</div>
<!-- /libtmux-logo -->

<div align="center">

# mcp

<!-- mcp-name: io.github.libtmux/tmux-mcp-go -->

Serve one tmux server to Model Context Protocol clients, built on the
[tmux module] and the [Go MCP SDK].

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/mcp.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp)

</div>

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

**Contents** — [Quick start](#quick-start) ·
[What it feels like](#what-it-feels-like) ·
[When it earns its keep](#when-it-earns-its-keep) · [The tools](#the-tools) ·
[Limiting what a client can do](#limiting-what-a-client-can-do) ·
[Settings](#settings) · [The pane it runs in](#the-pane-it-runs-in) ·
[Known limits](#known-limits) · [Troubleshooting](#troubleshooting) ·
[An agent-shaped program](#an-agent-shaped-program) ·
[Embedding it](#embedding-it) · [See also](#see-also)

## Quick start

You need Go 1.26 or newer, and tmux 3.2a or newer on `PATH`. The install
command above puts `libtmux-mcp` in `$(go env GOPATH)/bin`, or in `GOBIN`
when that is set. An MCP client
launches it as a subprocess and speaks to it over stdin and stdout, so there
is nothing to keep running yourself. If your shell cannot find
`libtmux-mcp`, add that directory to `PATH`. The Python server installs a
command of the same name; `command -v libtmux-mcp` shows which one comes
first, and `libtmux-mcp -version` names this one.

### `claude mcp add`: tell a client about it

#### Claude Code

```console
$ claude mcp add tmux -- libtmux-mcp
```

Claude Code starts each server it lists, to check that it connects:

```console
$ claude mcp list
```

#### Codex CLI

```console
$ codex mcp add tmux -- libtmux-mcp
```

#### Gemini CLI

```console
$ gemini mcp add tmux libtmux-mcp
```

#### Claude Desktop

Claude Desktop reads `claude_desktop_config.json`, and starts its servers
without your shell's `PATH`. In place of the path below, give it the one
`command -v libtmux-mcp` prints. Flags go in `args` and the environment in
`env`:

```json
{
  "mcpServers": {
    "tmux": {
      "command": "/path/to/libtmux-mcp",
      "args": ["-socket-name", "my-application"],
      "env": {"LIBTMUX_TOOLSETS": "inspect,manage,execute"}
    }
  }
}
```

### `libtmux-mcp -doctor`: check what it reaches

```console
$ libtmux-mcp -doctor
```

It answers without a client in the way, and prints something like this:

```text
libtmux-mcp doctor
  tmux:    3.6
  socket:  /tmp/tmux-1000/libtmux-mcp (from default dedicated socket)
  holds:   nothing — no tmux server is running on that socket
           (not a fault; tmux starts one when something asks it to)
  caller:  not running inside a tmux pane
```

Then ask the client for something only tmux can do, such as "create a tmux
session called demo and tell me what panes it has".

### `libtmux-mcp -tools`: see what a client would be offered

```console
$ libtmux-mcp -tools
```

It prints the tools the server would advertise: each one's toolset, which
processes it can reach, and a sentence on what it does. Give it the flags and
environment the client gives the server, because they decide the listing.

### `-socket-name`: choose which tmux it drives

With no flag, the server drives a tmux server of its own, on a socket named
`libtmux-mcp`, which it starts with a minimal configuration the package
ships. An agent given that cannot touch the sessions you already have. Once
it has made a session, watch it work from another terminal:

```console
$ tmux -L libtmux-mcp attach
```

To let it drive the tmux you use every day, name that socket. tmux calls its
usual one `default`:

```console
$ claude mcp add tmux -- libtmux-mcp -socket-name default
```

The choice is made once, when the server starts, and a client cannot change
it afterwards. [Settings](#settings) lists the other flags.

A workspace that `tmux-workspace load -L project` built lives on the socket
`project`. To let a client read it and change nothing:

```console
$ claude mcp add tmux \
    -e LIBTMUX_TOOLSETS=inspect \
    -- libtmux-mcp -socket-name project
```

## What it feels like

These are the calls [`examples/agent-workflow`](examples/agent-workflow)
makes to run work in a pane beside its own, and what came back. Cursor tokens
are left out, and each reply shows only the fields that answer the call:

<!-- docs:output mcp/examples/agent-workflow/trace.txt -->

```text
→ split_window {"direction":"right","pane_id":"%0","percent":40}
← {"paneId":"%1"}
→ paste_text {"enter":true,"pane_id":"%1","text":"if sleep 1; then printf 'agent-workflow-%s\\n' ready; else printf 'agent-workflow-%s\\n' failed; fi"}
← {"bytes":97}
→ wait_for_text {"pane_id":"%1","patterns":["agent-workflow-ready"],"stop":["agent-workflow-failed"],"timeout":30}
← {"found":true,"matched":"agent-workflow-ready","outcome":"matched"}
→ run_shell_command {"command":"printf 'bounded work\\n'","pane_id":"%1","timeout":30}
← {"exit_status":0,"output":["bounded work"],"timed_out":false}
```

<!-- docs:end -->

The example prints these with `-trace`; [its page](examples/agent-workflow)
has the command.

The agent drives tmux directly. Nothing is copy-pasted out of a terminal, and
waiting for a server to come up is one call rather than a loop of screenshots.

## When it earns its keep

For one `tmux send-keys`, it does not. Shelling out does that in a line, and
this server is a subprocess in the way.

It earns its keep the moment the agent has to **wait**, **watch**,
**inspect**, or **avoid disturbing the terminal a person is using**. A dev
server printing its port, a test run finishing, a deploy log settling: those
are where a shell-out turns into a polling loop that reads the shell's echo of
its own command and reports success before anything happened.

- [`run_shell_command`][run_shell_command] waits for the command and returns
  its exit status and output, without reading the screen at all.
- [`wait_for_text`][wait_for_text] watches what a pane writes.
- [`capture_since`][capture_since] returns what a pane wrote since the last
  look, rather than its whole screen again.

That is the difference: not more access to tmux, but a better place to put
the control loop, and a smaller bill for keeping it there.

Other parts of the design exist because an agent's context and its turn are
the scarce resources, not tmux:

- **Reads are bounded.** Captures keep the newest lines asked for and say
  what they left out. A search looks at no more than 200 panes, 20,000 lines,
  and 1,000,000 bytes, and matches for no more than five seconds.
- **One call can make several reads.**
  [`call_read_tools_batch`][call_read_tools_batch] takes up to sixteen
  `inspect` calls, checks each against its own tool's schema, and answers with
  one row per call: its index, whether it succeeded, and whether its result
  was cut short. The whole reply is at most 1,000,000 bytes.
- **Checking is not capturing.** The listing tools report what exists without
  reading what any pane shows. [`snapshot_pane`][snapshot_pane] is the one
  call that returns a pane's metadata and its content together.

## The tools

The tools come in toolsets. A toolset is a named group of tools, and it is
what an operator grants or withholds:

- `inspect` reads tmux and what panes show, and changes nothing.
- `manage` rearranges what exists: it renames, resizes, selects, and moves.
- `execute` creates sessions, windows, and panes, and sends input to them.
- `teardown` kills sessions, windows, and panes, and clears scrollback.

<!-- toolsindex -->

| Toolset | Tools |
| --- | --- |
| `inspect` | [`call_read_tools_batch`](https://libtmux.org/en/go/latest/mcp/tools/call_read_tools_batch/), [`capture_pane`](https://libtmux.org/en/go/latest/mcp/tools/capture_pane/), [`capture_since`](https://libtmux.org/en/go/latest/mcp/tools/capture_since/), [`find_pane_by_position`](https://libtmux.org/en/go/latest/mcp/tools/find_pane_by_position/), [`get_pane_info`](https://libtmux.org/en/go/latest/mcp/tools/get_pane_info/), [`get_server_info`](https://libtmux.org/en/go/latest/mcp/tools/get_server_info/), [`get_session_info`](https://libtmux.org/en/go/latest/mcp/tools/get_session_info/), [`get_tmux_variables`](https://libtmux.org/en/go/latest/mcp/tools/get_tmux_variables/), [`get_window_info`](https://libtmux.org/en/go/latest/mcp/tools/get_window_info/), [`list_panes`](https://libtmux.org/en/go/latest/mcp/tools/list_panes/), [`list_sessions`](https://libtmux.org/en/go/latest/mcp/tools/list_sessions/), [`list_windows`](https://libtmux.org/en/go/latest/mcp/tools/list_windows/), [`search_panes`](https://libtmux.org/en/go/latest/mcp/tools/search_panes/), [`show_environment`](https://libtmux.org/en/go/latest/mcp/tools/show_environment/), [`show_hooks`](https://libtmux.org/en/go/latest/mcp/tools/show_hooks/), [`show_option`](https://libtmux.org/en/go/latest/mcp/tools/show_option/), [`snapshot_pane`](https://libtmux.org/en/go/latest/mcp/tools/snapshot_pane/), [`wait_for_text`](https://libtmux.org/en/go/latest/mcp/tools/wait_for_text/) |
| `manage` | [`move_window`](https://libtmux.org/en/go/latest/mcp/tools/move_window/), [`rename_session`](https://libtmux.org/en/go/latest/mcp/tools/rename_session/), [`rename_window`](https://libtmux.org/en/go/latest/mcp/tools/rename_window/), [`resize_pane`](https://libtmux.org/en/go/latest/mcp/tools/resize_pane/), [`resize_window`](https://libtmux.org/en/go/latest/mcp/tools/resize_window/), [`select_layout`](https://libtmux.org/en/go/latest/mcp/tools/select_layout/), [`select_pane`](https://libtmux.org/en/go/latest/mcp/tools/select_pane/), [`select_window`](https://libtmux.org/en/go/latest/mcp/tools/select_window/), [`set_history_limit`](https://libtmux.org/en/go/latest/mcp/tools/set_history_limit/), [`set_mouse_enabled`](https://libtmux.org/en/go/latest/mcp/tools/set_mouse_enabled/), [`set_pane_title`](https://libtmux.org/en/go/latest/mcp/tools/set_pane_title/), [`signal_channel`](https://libtmux.org/en/go/latest/mcp/tools/signal_channel/), [`swap_pane`](https://libtmux.org/en/go/latest/mcp/tools/swap_pane/), [`wait_for_channel`](https://libtmux.org/en/go/latest/mcp/tools/wait_for_channel/) |
| `execute` | [`create_session`](https://libtmux.org/en/go/latest/mcp/tools/create_session/), [`create_window`](https://libtmux.org/en/go/latest/mcp/tools/create_window/), [`paste_text`](https://libtmux.org/en/go/latest/mcp/tools/paste_text/), [`respawn_pane`](https://libtmux.org/en/go/latest/mcp/tools/respawn_pane/), [`run_shell_command`](https://libtmux.org/en/go/latest/mcp/tools/run_shell_command/), [`send_keys_batch`](https://libtmux.org/en/go/latest/mcp/tools/send_keys_batch/), [`send_keys`](https://libtmux.org/en/go/latest/mcp/tools/send_keys/), [`set_synchronize_panes`](https://libtmux.org/en/go/latest/mcp/tools/set_synchronize_panes/), [`split_window`](https://libtmux.org/en/go/latest/mcp/tools/split_window/) |
| `teardown` | [`clear_pane_scrollback`](https://libtmux.org/en/go/latest/mcp/tools/clear_pane_scrollback/), [`kill_pane`](https://libtmux.org/en/go/latest/mcp/tools/kill_pane/), [`kill_session`](https://libtmux.org/en/go/latest/mcp/tools/kill_session/), [`kill_window`](https://libtmux.org/en/go/latest/mcp/tools/kill_window/) |

<!-- toolsindex:end -->

[TOOLS.md](TOOLS.md) has every tool's arguments and what it returns, and the
documentation site gives each tool the page linked above. That is reference
material, read by search rather than read through, which is why it is not
here.

**Input goes where tmux sends it.** With `synchronize-panes` on, tmux types
into every pane of the window, not only the one named.
[`send_keys`][send_keys] reports the panes it found linked that way before it
sent anything. `run_shell_command` refuses a pane linked with others, because
one exit status cannot describe several panes. [`paste_text`][paste_text]
reaches its target only.

**A pane that has just been created is given time to start.** Until a new
pane's shell reaches its prompt, tmux reports whatever its start-up files are
running. Input tools wait up to two seconds for that to settle rather than
refuse, so [`split_window`][split_window] followed at once by
`run_shell_command` works.

**Copy mode is left to the person.** Captures, snapshots, searches, and
cursors read pane output without taking over the view of someone attached, and
[`get_pane_info`][get_pane_info] reports when a pane is already in a mode.

There is also a
[libtmux-mcp server written in Python](https://github.com/tmux-python/libtmux-mcp).
The two serve the same tmux and answer to the same clients, and
[PARITY.md](PARITY.md) sets out where they differ.

## Limiting what a client can do

### [`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable]: choose the toolsets

It takes the names of the toolsets to grant, separated by commas, in any
order. Without it the server grants `inspect`, `manage`, and `execute`.

`teardown` joins that default in one case only: no socket was chosen, and
this process is the one that started the tmux server on the `libtmux-mcp`
socket. There is then nothing of yours for a kill to reach. Everywhere else,
ask for it by name.

A value that is set and empty grants no toolset. There is no `none` keyword.

To see the effect without a client, list what the server offers with one
toolset:

```console
$ LIBTMUX_TOOLSETS=inspect libtmux-mcp -tools
```

A client gets the same selection from the environment it starts the server
with:

```console
$ claude mcp add tmux \
    -e LIBTMUX_TOOLSETS=inspect \
    -- libtmux-mcp -socket-name default
```

### [`LIBTMUX_TOOLS`][mcp-toolsenvironmentvariable] and [`LIBTMUX_EXCLUDE_TOOLS`][mcp-excludetoolsenvironmentvariable]: name exact tools

`LIBTMUX_TOOLS` adds tools by name to what the toolsets grant, and
`LIBTMUX_EXCLUDE_TOOLS` then removes tools by name. Excluding a tool also
removes it from what `call_read_tools_batch` may call. A name that matches no
tool, or an empty item in either list, stops the server at startup, before it
opens tmux.

This lists the default tools without `send_keys` and `send_keys_batch`:

```console
$ LIBTMUX_EXCLUDE_TOOLS=send_keys,send_keys_batch libtmux-mcp -tools
```

`LIBTMUX_SAFETY`, `LIBTMUX_MCP_CAPABILITIES`, and
`LIBTMUX_MCP_PROMPTS_AS_TOOLS` are names from earlier releases. Setting one
stops startup with what to set instead, and [MIGRATING.md](MIGRATING.md) maps
each earlier setting to its current one.

Choosing tools shapes what a client is offered. It is not a sandbox: every
call runs with your user's authority, and a client holding `send_keys` can
run anything you can.

### `_meta`: what each tool says about itself

`_meta` is the field MCP gives a server for data of its own. Every listed
tool carries a description of what it reaches and changes there, under
`_meta["com.git-pull.libtmux-mcp/capability"]`: which processes it can start
or type into, what it does to tmux, what kinds of output it returns, whether
that output may hold secrets or text a pane wrote, and how its arguments are
kept from being read as tmux syntax.
[`tmux://capabilities`][mcp-capabilitiesresourceuri] is the server's one
resource, and reports the same descriptions beside the selection in force.

## Settings

Flags choose the tmux server once, at startup:

| Flag | Meaning |
| --- | --- |
| `-socket-path` | an explicit socket path; when set, it wins over everything else |
| `-socket-name` | a tmux socket name; when set, it wins over both socket variables below |
| `-binary` | the tmux executable; empty uses `LIBTMUX_TMUX_BIN`, then finds `tmux` on `PATH` |

The tmux executable is resolved once, from the environment and directory the
server started in. A bad path fails at startup, and nothing later can point
the server at another tmux. A tmux server that is not running is not an
error: tmux starts one when something asks it to.

Other flags answer a question and exit, which is what a config entry that
will not start needs:

| Flag | Answers |
| --- | --- |
| `-version` | which build this is |
| `-tools` | the tools and toolsets a client would see |
| `-doctor` | which socket it reaches, what is on it, and whether it is running inside that tmux itself |

`-version` needs no tmux. `-tools` asks the socket it would serve whether a
tmux server runs there, because that decides whether `teardown` is in the
default, so it needs the tmux executable. `-tools` and `-doctor` drive the
server through its own protocol in memory, so they report what a client sees
rather than what the code intends.

The environment sets the rest:

| Variable | Does |
| --- | --- |
| [`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable] | selects any unordered subset of the toolsets |
| [`LIBTMUX_TOOLS`][mcp-toolsenvironmentvariable] | includes exact tool names after toolset expansion |
| [`LIBTMUX_EXCLUDE_TOOLS`][mcp-excludetoolsenvironmentvariable] | removes exact names after every inclusion |
| [`LIBTMUX_SOCKET_PATH`][mcp-socketpathenvironmentvariable] | selects an absolute socket path; mutually exclusive with `LIBTMUX_SOCKET` |
| [`LIBTMUX_SOCKET`][mcp-socketenvironmentvariable] | names the tmux socket when no socket flag selects one |
| [`LIBTMUX_TMUX_CONFIG`][mcp-tmuxconfigenvironmentvariable] | selects a nonempty absolute tmux configuration path |
| [`LIBTMUX_TMUX_BIN`][mcp-binaryenvironmentvariable] | selects the tmux executable when `-binary` is empty |
| [`LIBTMUX_MCP_WAIT_MAX_SECONDS`][mcp-waitceilingenvironmentvariable] | the longest any one wait may run; 300 by default |
| [`LIBTMUX_AUDIT`][mcp-auditenvironmentvariable] | `stderr`, or a path, to record every call |

The names match the Python server's, so an operator running both writes one
thing. A flag wins over its variable, and everything is fixed when the server
starts.

A wait longer than the ceiling is shortened rather than refused, and the reply
says so in `effectiveTimeoutSeconds` and `timeoutClamped`. A long wait blocks
nothing else in the server. What it costs is the agent's turn, and MCP gives
an agent no way to change its mind in the middle of a call.

## The pane it runs in

### `isCaller`: knowing its own pane

A pane this server runs in is the terminal the conversation is happening
through. Killing it, clearing it, or typing into it is not like doing those
things to any other pane, so every pane the server reports carries `isCaller`:
true when it is this one, false when it is not, and null when the server is not
in a pane at all. [`get_server_info`][get_server_info] answers the same question
directly.

tmux tells a process in a pane which pane it is through `TMUX_PANE` and `TMUX`,
and that is the first thing checked. It is not enough on its own: an MCP client
starts its servers with an environment it chooses, and most choose one
carrying neither. So when the environment says nothing, the server finds the
pane another way. It descends from whatever tmux started in that pane, so the
pane whose process is one of its own ancestors is its own.

### Asking before it types into your terminal

Typing into the pane with `isCaller` true reaches the terminal you are talking
to the agent through. A note in a reply is something a model with a task does
not always read, so a write to that pane asks you first, through MCP's
elicitation request: a question the client puts to you on the server's
behalf. Declining fails the call, and a client that cannot ask is refused.

A write reached through a batch asks in the same way, and declining stops the
batch there. With `synchronize-panes` on, the question covers every pane the
input would reach, so a write that would arrive in your pane by way of another
still asks. A linked pane that is dead or in a mode refuses the whole write
before anyone is asked.

This protects the pane you are in. It does not make the tools a sandbox.

## Known limits

- **Every tool is annotated as destructive.** Each one, `list_sessions`
  included, carries `destructiveHint=true` and `openWorldHint=true`, and none
  carries `readOnlyHint`. A client that skips approval for read-only tools
  prompts on all of them. Classify by `tmuxEffects` in each tool's `_meta`
  instead.
- **`tools/list` is large.** The reply is about 116 KB with every toolset and
  109 KB without `teardown`. More than half of it is the `_meta` description,
  which repeats each tool's text and schemas.
  A client that wants a smaller reply can ignore `_meta`: each tool's text and
  schemas are also at the top level.
- **Some tools spell their results differently.** `run_shell_command`,
  `send_keys`, `paste_text`, and
  [`clear_pane_scrollback`][clear_pane_scrollback] return fields such as
  `exit_status`; every other tool returns fields such as `paneId`. Read a
  tool's schema rather than guessing, and see [PARITY.md](PARITY.md) for why.
- **One tmux server for the life of the process.** A client cannot name
  another socket in a call.
- **An empty `TMUX` is not an absent one.** Input tools refuse when `TMUX` or
  `TMUX_PANE` is set to nothing, because the server then cannot tell whether
  it is in a pane. Unset them instead.
- **Before tmux 3.6, killing one session can end the server.** The server
  holds a connection attached to the first session it reached, and that
  session's `detach-on-destroy` option decides what happens when it goes.
  tmux 3.6 and later move the connection to a session that remains.
- **Nothing but tools.** There is one resource, and no prompts, resource
  templates, completions, or background job handles.

## Troubleshooting

**Ask the server first.** `-doctor` answers most of what follows without a
client in the way:

```console
$ libtmux-mcp -doctor -socket-name default
```

**The client shows no tmux tools.** The server never started. Run the exact
command from your client's config by hand: a bad `-binary`, or a path that is
not on the client's `PATH`, fails at startup and says so.

**Tools are missing rather than failing.** `LIBTMUX_TOOLSETS`, `LIBTMUX_TOOLS`,
or `LIBTMUX_EXCLUDE_TOOLS` shaped what is offered. `-tools` prints what a
client receives.

**It sees none of your sessions.** Without `-socket-name` it drives a tmux
server of its own. `-doctor` names the socket it reaches.

**It reaches the wrong tmux.** A client's environment is not your shell's:
`TMUX_TMPDIR` set in your profile is not set for a server the client started.

**Sessions you can see are reported as nothing there.** Compare the tmux
version `-doctor` prints with your shell's `tmux -V`. A client starts its
servers with a `PATH` of its own, which can find a different tmux than the one
that started your sessions, and a tmux client cannot talk to a server of
another version. It reports `server exited unexpectedly`, which reads the same
as a server that has gone. Point `-binary` at the tmux your sessions belong to.

**A command works in your shell but not through a tool.** The pane's shell is
not your shell. It has the environment tmux gave it when it started.
[`show_environment`][show_environment] reports what a *new* pane would
inherit, which is not what one already running has.

## An agent-shaped program

[`examples/agent-workflow`](examples/agent-workflow) is the whole loop in one
file. It works out which pane it is running in, splits a pane off, starts work
there and waits for what it prints, runs a command and reads its exit status,
then reads back what it built. It lives in the repository rather than the
module you installed, so run it from a clone:

```console
$ git clone https://github.com/libtmux/libtmux-go.git
```

```console
$ go -C libtmux-go/mcp run ./examples/agent-workflow -socket-name demo
```

On a socket with nothing on it, it starts a session there and prints:

<!-- docs:output mcp/examples/agent-workflow/output.txt -->

```text
no tmux server on that socket yet; starting one
not running inside this tmux server; using active pane %0
split into %1
2 panes in this window while the work runs
the work printed agent-workflow-ready
the command exited 0 and printed:
  | bounded work
the window now holds 2 panes: %0 %1
```

<!-- docs:end -->

The client and server are joined in memory, so it is one program rather than
two. The tool names, schemas, and results are the ones a client sees over
stdin and stdout. Clean up with `tmux -L demo kill-server`.

## Embedding it

### [`mcp.NewServer`][mcp-newserver]: serve one tmux server from your own program

[`mcp.Run`][mcp-run] serves a tmux server over standard input and output,
which is all the `libtmux-mcp` command does. To serve it over another
transport, build an [`Instance`][mcp-instance] and connect it yourself. This
block comes from the package's own example test, which connects one end of
the SDK's in-memory transport pair and gives the other end to a client:

<!-- docs:mcp-embedding -->

```go
// Given: ctx context.Context; target tmux.Server; serverTransport sdk.Transport
instance, err := tmuxmcp.NewServer(target)
if err != nil {
	return nil, nil, err
}
serverSession, err := instance.Connect(
	ctx, tmuxmcp.AssumeResponseCommit(serverTransport), nil,
)
```

<!-- docs:end -->

This package is named `mcp` and so is the SDK's, so a file using both has to
rename one of them. The block imports this one as `tmuxmcp` and the SDK as
`sdk`. `NewServer` checks the target and starts nothing;
[close the instance][mcp-instance-close] when you have finished serving.

The server needs to know that each response reaches the client as soon as
the transport's write returns, rather than waiting in a buffer behind
another. Standard input and output are known to. Any other transport must be
wrapped in [`AssumeResponseCommit`][mcp-assumeresponsecommit] to say so, as
the block does for the in-memory one. The
[reference](https://libtmux.org/en/go/latest/mcp/reference/) has the rest of
what an embedder is held to.

## See also

This is a module of its own, so `go get` on the tmux library never pulls in
the MCP SDK it needs.

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/mcp/reference/) | Every exported identifier, method, and field, one page each |
| [`TOOLS.md`](TOOLS.md) | Every tool's arguments and results |
| [`PARITY.md`](PARITY.md) | Where this server and the Python one differ |
| [`MIGRATING.md`](MIGRATING.md) | Settings from earlier releases, and what replaced each |
| [`cmd/libtmux-mcp`](cmd/libtmux-mcp) | The command a client launches |
| [`examples/agent-workflow`](examples/agent-workflow) | The program above, as a directory you can run |
| [`AGENTS.md`](AGENTS.md) | Testing the server through a real client, and by hand |
| [`../tmux/`](../tmux/) | The library this serves |

[call_read_tools_batch]: https://libtmux.org/en/go/latest/mcp/tools/call_read_tools_batch/
[capture_since]: https://libtmux.org/en/go/latest/mcp/tools/capture_since/
[clear_pane_scrollback]: https://libtmux.org/en/go/latest/mcp/tools/clear_pane_scrollback/
[get_pane_info]: https://libtmux.org/en/go/latest/mcp/tools/get_pane_info/
[get_server_info]: https://libtmux.org/en/go/latest/mcp/tools/get_server_info/
[Go MCP SDK]: https://github.com/modelcontextprotocol/go-sdk
[mcp-assumeresponsecommit]: https://libtmux.org/en/go/latest/mcp/reference/mcp-assumeresponsecommit/
[mcp-auditenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-auditenvironmentvariable/
[mcp-binaryenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-binaryenvironmentvariable/
[mcp-capabilitiesresourceuri]: https://libtmux.org/en/go/latest/mcp/reference/mcp-capabilitiesresourceuri/
[mcp-excludetoolsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-excludetoolsenvironmentvariable/
[mcp-instance-close]: https://libtmux.org/en/go/latest/mcp/reference/mcp-instance-close/
[mcp-instance]: https://libtmux.org/en/go/latest/mcp/reference/mcp-instance/
[mcp-newserver]: https://libtmux.org/en/go/latest/mcp/reference/mcp-newserver/
[mcp-run]: https://libtmux.org/en/go/latest/mcp/reference/mcp-run/
[mcp-socketenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-socketenvironmentvariable/
[mcp-socketpathenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-socketpathenvironmentvariable/
[mcp-tmuxconfigenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-tmuxconfigenvironmentvariable/
[mcp-toolsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-toolsenvironmentvariable/
[mcp-toolsetsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-toolsetsenvironmentvariable/
[mcp-waitceilingenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-waitceilingenvironmentvariable/
[paste_text]: https://libtmux.org/en/go/latest/mcp/tools/paste_text/
[run_shell_command]: https://libtmux.org/en/go/latest/mcp/tools/run_shell_command/
[send_keys]: https://libtmux.org/en/go/latest/mcp/tools/send_keys/
[show_environment]: https://libtmux.org/en/go/latest/mcp/tools/show_environment/
[snapshot_pane]: https://libtmux.org/en/go/latest/mcp/tools/snapshot_pane/
[split_window]: https://libtmux.org/en/go/latest/mcp/tools/split_window/
[tmux module]: ../tmux/
[wait_for_text]: https://libtmux.org/en/go/latest/mcp/tools/wait_for_text/
