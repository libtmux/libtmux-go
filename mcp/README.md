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
[![libtmux.org](https://img.shields.io/badge/docs-libtmux.org-0b7285)](https://libtmux.org/en/go/latest/mcp/)

</div>

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

Releases are numbered `0.1.N`, and each release increments `N`. The API is not
settled: any release may change or remove exported identifiers, and the
changelog marks each break. Pin an exact version to avoid one.

**Contents** — [Quick start](#quick-start) ·
[Connect another client](#connect-another-client) ·
[Choose which tmux it drives](#choose-which-tmux-it-drives) ·
[See an agent work](#see-an-agent-work) ·
[Run work and wait for it](#run-work-and-wait-for-it) ·
[The tools](#the-tools) ·
[Limiting what a client can do](#limiting-what-a-client-can-do) ·
[Settings](#settings) · [The pane it runs in](#the-pane-it-runs-in) ·
[Known limits](#known-limits) · [Troubleshooting](#troubleshooting) ·
[An agent-shaped program](#an-agent-shaped-program) ·
[Embedding it](#embedding-it) · [Packages](#packages) ·
[What it leaves out](#what-it-leaves-out) ·
[Support and status](#support-and-status) · [See also](#see-also)

## Quick start

You need Go 1.26 or newer and tmux 3.3a or newer on `PATH`. The install
command above puts [`libtmux-mcp`][libtmux-mcp-command] in `GOBIN` when that is
set, and in `$(go env GOPATH)/bin` otherwise.

An MCP client launches it as a subprocess and speaks to it over stdin and
stdout, so nothing stays running. If your shell cannot find `libtmux-mcp`, add
that directory to `PATH`.

The Python server installs a command of the same name. Run
`command -v libtmux-mcp` to see which one comes first, and
`libtmux-mcp -version` to name this one.

### `claude mcp add`: tell Claude Code about it

```console
$ claude mcp add tmux -- libtmux-mcp
```

Claude Code starts each server it lists, to check that it connects:

```console
$ claude mcp list
```

### [`libtmux-mcp -doctor`][guide-diagnose-startup]: check what it reaches

```console
$ libtmux-mcp -doctor
```

It answers without a client in the way, and prints something like this:

```text
libtmux-mcp doctor
  tmux:    3.6 (requires 3.3a or newer)
  socket:  /tmp/tmux-1000/default (from tmux-choice)
  holds:   nothing — no tmux server is running on that socket
           (not a fault; tmux starts one when something asks it to)
  caller:  not running inside a tmux pane
```

Then ask the client for something only tmux can do, such as "create a tmux
session called demo and tell me what panes it has".

## Connect another client

Each client takes the same command and the same flags. [Connect an MCP
client][guide-connect-client] on libtmux.org covers startup and shutdown.

### `codex mcp add`: Codex CLI

```console
$ codex mcp add tmux -- libtmux-mcp
```

### `gemini mcp add`: Gemini CLI

```console
$ gemini mcp add tmux libtmux-mcp
```

### `claude_desktop_config.json`: Claude Desktop

Claude Desktop starts its servers without your shell's `PATH`, so give it the
path `command -v libtmux-mcp` prints. Flags go in `args` and the environment in
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

## Choose which tmux it drives

The choice is made once, when the server starts. A client cannot change it
afterwards. [Settings](#settings) lists the other flags, and
[the contract][contract-which-tmux] has the precedence rules.

### [`-socket-name`][guide-choose-a-server]: pick a socket by name

With no flag and neither `LIBTMUX_SOCKET_PATH` nor `LIBTMUX_SOCKET` set, the
server passes tmux no socket, so tmux picks the server. Started inside tmux,
that is the server you are in. Started elsewhere, it is tmux's default socket,
which holds the sessions you already have, and an agent given it can change
them. A named socket keeps an agent apart from them:

```console
$ claude mcp add tmux -- libtmux-mcp -socket-name my-application
```

Once it has made a session, watch it work from another terminal:

```console
$ tmux -L my-application attach
```

To drive the tmux you use every day from outside it, name that socket. tmux
calls its usual one `default`:

```console
$ claude mcp add tmux -- libtmux-mcp -socket-name default
```

A workspace that [`tmux-workspace load -L project`][workspace-cli-load] built
lives on the socket `project`. To let a client read it and change nothing:

```console
$ claude mcp add tmux \
    -e LIBTMUX_TOOLSETS=inspect \
    -- libtmux-mcp -socket-name project
```

### [`-socket-path` and `-binary`][guide-choose-a-server]: an exact socket and tmux

`-socket-path` names the socket by its absolute path, and `-binary` names the
tmux executable. `-doctor` reports what a choice selects:

```console
$ libtmux-mcp \
    -doctor \
    -socket-path /tmp/my-application.sock
```

The environment makes the same choices when no flag is given, which is how a
client's configuration sets them. `LIBTMUX_SOCKET_PATH` names a socket path and
`LIBTMUX_SOCKET` a socket name, and the path wins when both are set:

```console
$ LIBTMUX_SOCKET=my-application libtmux-mcp -doctor
```

## See an agent work

These are the calls [`examples/agent-workflow`](examples/agent-workflow) makes
to run work in a pane beside its own, and what came back. Cursor tokens are
left out, and each reply shows only the fields that answer the call:

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

The example prints these with `-trace`, and [its page](examples/agent-workflow)
has the command. The agent drives tmux directly, so nothing is copy-pasted out
of a terminal, and waiting for a server to come up is one call.

## Run work and wait for it

For one `tmux send-keys`, shelling out is shorter. The server earns its keep
once the agent has to **wait**, **watch**, **inspect**, or **leave your
terminal alone**.

A dev server printing its port, a test run finishing, or a deploy log settling
turns a shell-out into a polling loop. That loop reads the shell's echo of its
own command and reports success before anything happened.

### [`split_window`][split_window]: make room beside your pane

[`split_window`][split_window] adds a pane and returns its id. A new pane is
given time to start, so [`run_shell_command`][run_shell_command] can follow at
once.

<!-- docs:checked mcp/examples/agent-workflow/trace.txt -->
The trace splits `%0` to the right with `"percent":40` and gets back
`{"paneId":"%1"}`.
<!-- docs:end -->

### [`wait_for_text`][wait_for_text]: wait for what a pane prints

[`wait_for_text`][wait_for_text] watches a pane and returns when a pattern
appears, or when a `stop` pattern appears first.
[`capture_since`][capture_since] returns what a pane wrote since the last
look.

<!-- docs:checked mcp/examples/agent-workflow/trace.txt -->
The trace waits for `agent-workflow-ready` and names `agent-workflow-failed` as
the stop pattern. It gets `"outcome":"matched"`.
<!-- docs:end -->

A wait longer than the ceiling is shortened, and the reply says so. The
ceiling is [`LIBTMUX_MCP_WAIT_MAX_SECONDS`][mcp-waitceilingenvironmentvariable].

If the pane goes away mid-wait, the reply's `outcome` is `paneDied` and `lines`
holds what it wrote first. If only the output stream ends and the pane is
alive, it is `streamLost`. [The contract][contract-wait-ends] has the rest.

### [`run_shell_command`][run_shell_command]: run a command and read its exit status

[`run_shell_command`][run_shell_command] waits for the command and returns its
exit status and output, without reading the screen at all.
[`send_keys`][send_keys] and [`paste_text`][paste_text] send input and return.

<!-- docs:checked mcp/examples/agent-workflow/trace.txt -->
The trace runs a command that prints `"bounded work"` and gets back
`"exit_status":0`.
<!-- docs:end -->

[`run_shell_command`][run_shell_command] refuses a pane linked with others by
`synchronize-panes`, because one exit status cannot describe several panes.

### Keep reads small

Other parts of the design exist because an agent's context and its turn are
the scarce resources.

- **Reads are bounded.** Captures keep the newest lines asked for and say what
  they left out. Searches have fixed limits, which
  [the contract][contract-bounded-reads] lists.
- **One call can make several reads.**
  [`call_read_tools_batch`][call_read_tools_batch] answers a list of `inspect`
  calls with one row each.
- **Checking is not capturing.** The listing tools report what exists without
  reading what any pane shows. [`snapshot_pane`][snapshot_pane] returns a pane's
  metadata and content together.

## The tools

The tools come in [toolsets][topic-tool-selection]. A toolset is a named group
of tools, and it is what an operator grants or withholds:

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
documentation site gives each tool the page linked above. That reference is
for searching, which is why it is not here.

Three behaviours matter before you grant `execute`:

- **Input goes where tmux sends it.** With `synchronize-panes` on, tmux types
  into every pane of the window. [`send_keys`][send_keys] reports the linked
  panes before it sends.
- **A new pane is given time to start.** Input tools wait for its shell to
  reach the prompt, so [`split_window`][split_window] then
  [`run_shell_command`][run_shell_command] works.
- **Copy mode is left to the person.** Reads never take over an attached
  view, and [`get_pane_info`][get_pane_info] reports when a pane is in a mode.

[The contract][contract-input] has the rest of each.

## Limiting what a client can do

### [`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable]: choose the toolsets

It takes toolset names separated by commas, in any order. Without it the
server grants `inspect`, `manage`, and `execute`.

- `teardown` is never in that default. Ask for it by name.
- A value that is set and empty grants no toolset, and there is no `none`
  keyword.

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

[`LIBTMUX_TOOLS`][mcp-toolsenvironmentvariable] adds tools by name to what the
toolsets grant, and
[`LIBTMUX_EXCLUDE_TOOLS`][mcp-excludetoolsenvironmentvariable] then removes
tools by name. Excluding a tool also removes it from what
[`call_read_tools_batch`][call_read_tools_batch] may call.

This lists the default tools without [`send_keys`][send_keys] and
[`send_keys_batch`][send_keys_batch]:

```console
$ LIBTMUX_EXCLUDE_TOOLS=send_keys,send_keys_batch libtmux-mcp -tools
```

A name that matches no tool, or an empty item in either list, stops the
server at startup, before it opens tmux.

Choosing tools shapes what a client is offered. Every call runs with your
user's authority. [The contract][contract-exact-tools] covers the variables
from earlier releases.

### [`libtmux-mcp -tools`][guide-inspect-the-selection]: see what a client would be offered

```console
$ libtmux-mcp -tools
```

It prints the tools the server would advertise: each one's toolset, which
processes it can reach, and a sentence on what it does. Give it the flags and
environment the client gives the server, because they decide the listing.

### [`tmux://capabilities`][mcp-capabilitiesresourceuri]: what each tool says about itself

Each tool describes what it reaches and changes, under
`_meta["com.git-pull.libtmux-mcp/capability"]`. The server's one resource,
`tmux://capabilities`, reports the same descriptions beside the selection in
force. [The contract][contract-capability-disclosure] lists them.

## Settings

Flags choose the tmux server once, at startup:

| Flag | Meaning |
| --- | --- |
| `-socket-path` | an explicit socket path; when set, it wins over everything else |
| `-socket-name` | a tmux socket name; when set, it wins over both socket variables below |
| `-binary` | the tmux executable; empty uses [`LIBTMUX_TMUX_BIN`][mcp-binaryenvironmentvariable], then finds `tmux` on `PATH` |

Other flags answer a question and exit, which is what a config entry that
will not start needs:

| Flag | Answers |
| --- | --- |
| `-version` | which build this is |
| `-tools` | the tools and toolsets a client would see |
| `-doctor` | which socket it reaches, what is on it, and whether it is running inside that tmux itself |

The environment sets the rest:

| Variable | Does |
| --- | --- |
| [`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable] | selects any unordered subset of the toolsets |
| [`LIBTMUX_TOOLS`][mcp-toolsenvironmentvariable] | includes exact tool names after toolset expansion |
| [`LIBTMUX_EXCLUDE_TOOLS`][mcp-excludetoolsenvironmentvariable] | removes exact names after every inclusion |
| [`LIBTMUX_SOCKET_PATH`][mcp-socketpathenvironmentvariable] | selects the socket path when no socket flag selects one; wins over [`LIBTMUX_SOCKET`][mcp-socketenvironmentvariable] |
| [`LIBTMUX_SOCKET`][mcp-socketenvironmentvariable] | names the tmux socket when no socket flag and no `LIBTMUX_SOCKET_PATH` select one |
| [`LIBTMUX_TMUX_CONFIG`][mcp-tmuxconfigenvironmentvariable] | selects a nonempty absolute tmux configuration path |
| [`LIBTMUX_TMUX_BIN`][mcp-binaryenvironmentvariable] | selects the tmux executable when `-binary` is empty |
| [`LIBTMUX_MCP_WAIT_MAX_SECONDS`][mcp-waitceilingenvironmentvariable] | the longest any one wait may run; 300 by default |
| [`LIBTMUX_AUDIT`][mcp-auditenvironmentvariable] | `stderr`, or a path, to record every call |

- **A flag wins over its variable,** and everything is fixed when the server
  starts. The names match the Python server's.
- **The tmux executable is resolved once.** A bad path fails at startup. A tmux
  server that is not running is fine, because tmux starts one on demand.
- **A long wait is shortened.** The reply says so in `effectiveTimeoutSeconds`
  and `timeoutClamped`.

[The contract][contract-settings] has the full text.

## The pane it runs in

### `isCaller`: knowing its own pane

Every pane the server reports carries `isCaller`: true for the pane it runs
in, false for any other, and null when the server is not in a pane at all.
[`get_server_info`][get_server_info] answers the same question directly.

The server reads `TMUX_PANE` and `TMUX` first. When they say nothing, it finds
the pane whose process is one of its own ancestors.

### Asking before it types into your terminal

A write to the pane with `isCaller` true asks you first, through MCP's
elicitation request: a question the client puts to you on the server's behalf.

- Declining fails the call, and a client that cannot ask is refused.
- A batch asks the same way, and declining stops the batch there.
- With `synchronize-panes` on, the question covers every pane the input would
  reach.

[The contract][contract-caller-pane] has the rest.

## Known limits

- **Some tools require a newer tmux.** Startup and ordinary tools support
  3.3a, including `capture_since` and `run_shell_command`. Non-empty
  `paste_text` requires 3.4 before staging any buffer; `wait_for_text` polls
  with `capture-pane` below 3.7 and follows control output from 3.7. Restricted
  tools return an explicit version error on older supported daemons. The
  runtime uses subprocess commands below 3.7 and a retained control connection
  from 3.7.
- **Every tool is annotated as destructive.** [`list_sessions`][list_sessions]
  included, each carries `destructiveHint=true` and no `readOnlyHint`. Classify
  by `tmuxEffects` in each tool's `_meta`.
- **`tools/list` is large.** More than half of it is the `_meta`
  description. A client that wants a smaller reply can ignore `_meta`.
- **Some tools spell results differently.**
  [`run_shell_command`][run_shell_command], [`send_keys`][send_keys],
  [`paste_text`][paste_text], and
  [`clear_pane_scrollback`][clear_pane_scrollback] return fields such as
  `exit_status`; the rest return fields such as `paneId`.
- **An empty `TMUX` counts as set.** Input tools refuse when `TMUX` or
  `TMUX_PANE` is set to nothing. Unset them instead.

[The contract][contract-known-limits] gives the reason for each, and
[PARITY.md](PARITY.md) covers the result spellings.

## Troubleshooting

Ask the server first. `-doctor` answers most of what follows without a client
in the way:

```console
$ libtmux-mcp -doctor -socket-name default
```

- **The client shows no tmux tools.** The server never started. Run the
  command from your client's config by hand.
- **Tools are missing.** [`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable],
  [`LIBTMUX_TOOLS`][mcp-toolsenvironmentvariable], or
  [`LIBTMUX_EXCLUDE_TOOLS`][mcp-excludetoolsenvironmentvariable] shaped the
  listing. `-tools` prints what a client receives.
- **It sees none of your sessions.** Without `-socket-name` it drives a tmux
  server of its own. `-doctor` names the socket.
- **It reaches the wrong tmux.** A client's environment is not your shell's,
  so `TMUX_TMPDIR` from your profile may be unset for it.
- **Sessions you can see are reported as nothing there.** Compare the tmux
  version `-doctor` prints with `tmux -V`, and point `-binary` at the right one.
- **A command works in your shell but not through a tool.**
  [`show_environment`][show_environment] reports what a new pane would inherit.

[The contract][contract-troubleshooting] explains each.

## An agent-shaped program

[`examples/agent-workflow`](examples/agent-workflow) is the whole loop in one
file. It finds its own pane, splits one off, starts work there and waits for
what it prints, runs a command and reads its exit status, then reads back what
it built.

It lives in the repository, outside the module you installed, so run it from a
clone:

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

The client and server are joined in memory, so it is one program. The tool
names, schemas, and results are the ones a client sees over stdin and stdout.
Clean up with `tmux -L demo kill-server`.

## Embedding it

### [`mcp.NewServer`][mcp-newserver]: serve one tmux server from your own program

[`mcp.Run`][mcp-run] serves a tmux server over standard input and output,
which is all the `libtmux-mcp` command does. For another transport, build an
[`Instance`][mcp-instance] and connect it yourself.

This block comes from the package's own example test. It connects one end of
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

This package is named `mcp` and so is the SDK's, so the block imports this one
as `tmuxmcp` and the SDK as `sdk`. [`NewServer`][mcp-newserver] checks the
target and starts nothing.

### [`mcp.AssumeResponseCommit`][mcp-assumeresponsecommit]: say a transport writes each response at once

Standard input and output are known to deliver each response as soon as the
write returns. Wrap any other transport in
[`AssumeResponseCommit`][mcp-assumeresponsecommit], as the block does for the
in-memory one.

[Close the instance][mcp-instance-close] when you have finished serving. The
[reference][mcp-reference] has the rest of what an embedder is held to, and
[the contract][contract-embedding] has the full text.

## Packages

### [`mcp`](https://github.com/libtmux/libtmux-go/tree/master/mcp): the server as a library

[`mcp.Run`][mcp-run] and [`mcp.NewServer`][mcp-newserver] serve a tmux server
to a client. Reference: [pkg.go.dev](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp)
and [libtmux.org][mcp-reference].

### [`libtmux-mcp`](https://github.com/libtmux/libtmux-go/tree/master/mcp/cmd/libtmux-mcp): the command a client launches

It serves one tmux server over stdin and stdout. Reference:
[pkg.go.dev][libtmux-mcp-command] and the
[command README](cmd/libtmux-mcp/README.md).

## What it leaves out

- **A second tmux server.** The server serves one tmux server for the life of
  the process, and a client cannot name another socket in a call.
- **Anything but tools.** There is one resource, and no prompts, resource
  templates, completions, or background job handles.
- **Confinement.** Toolsets shape what a client is offered. Every call runs
  with your user's authority, so a client holding [`send_keys`][send_keys] can
  run anything you can.
- **The Python server's per-call socket.** The socket is chosen once, at
  launch. [PARITY.md](PARITY.md) lists the other differences.

## Support and status

- **Go 1.26+ and tmux 3.3a or newer.** The server refuses an older tmux before
  the MCP transport starts. CI runs tmux 3.3a and 3.8 on each push, and tmux
  3.3a through 3.8 for each release.
- **Linux, including WSL, and macOS.** CI runs this module's tests on Linux
  against the same releases. macOS is supported on a best-effort basis, and CI
  coverage for it is highly limited.
- **A module of its own.** `go get` on the tmux library never pulls in the MCP
  SDK.
- **Tagged as `mcp/vN`.** Pin an exact version in your own `go.mod`; the
  commands here fetch the newest.

## See also

| | |
| --- | --- |
| [Reference](https://libtmux.org/en/go/latest/mcp/reference/) | Every exported identifier, method, and field, one page each |
| [`docs/contracts.md`](docs/contracts.md) | What the server promises beyond this page |
| [`TOOLS.md`](TOOLS.md) | Every tool's arguments and results |
| [`PARITY.md`](PARITY.md) | Where this server and the Python one differ |
| [`MIGRATING.md`](MIGRATING.md) | Settings from earlier releases, and what replaced each |
| [`cmd/libtmux-mcp`](cmd/libtmux-mcp) | The command a client launches |
| [`examples/agent-workflow`](examples/agent-workflow) | The program above, as a directory you can run |
| [`AGENTS.md`](AGENTS.md) | Testing the server through a real client, and by hand |
| [`../tmux/`](../tmux/) | The library this serves |
| [libtmux-mcp in Python](https://github.com/tmux-python/libtmux-mcp) | A server for the same tmux and the same clients |

[call_read_tools_batch]: https://libtmux.org/en/go/latest/mcp/tools/call_read_tools_batch/
[capture_since]: https://libtmux.org/en/go/latest/mcp/tools/capture_since/
[clear_pane_scrollback]: https://libtmux.org/en/go/latest/mcp/tools/clear_pane_scrollback/
[contract-bounded-reads]: docs/contracts.md#bounded-reads-and-batches
[contract-capability-disclosure]: docs/contracts.md#capability-disclosure
[contract-caller-pane]: docs/contracts.md#the-pane-the-server-runs-in
[contract-embedding]: docs/contracts.md#embedding-the-server
[contract-exact-tools]: docs/contracts.md#exact-tools-and-exclusions
[contract-input]: docs/contracts.md#input-and-synchronized-panes
[contract-known-limits]: docs/contracts.md#known-limits
[contract-settings]: docs/contracts.md#settings-and-precedence
[contract-troubleshooting]: docs/contracts.md#troubleshooting
[contract-wait-ends]: docs/contracts.md#how-a-wait-ends
[contract-which-tmux]: docs/contracts.md#which-tmux-it-serves
[get_pane_info]: https://libtmux.org/en/go/latest/mcp/tools/get_pane_info/
[get_server_info]: https://libtmux.org/en/go/latest/mcp/tools/get_server_info/
[Go MCP SDK]: https://github.com/modelcontextprotocol/go-sdk
[guide-choose-a-server]: https://libtmux.org/en/go/latest/mcp/guides/connect-client/#choose-a-server
[guide-connect-client]: https://libtmux.org/en/go/latest/mcp/guides/connect-client/
[guide-diagnose-startup]: https://libtmux.org/en/go/latest/mcp/guides/connect-client/#diagnose-startup
[guide-inspect-the-selection]: https://libtmux.org/en/go/latest/mcp/guides/connect-client/#inspect-the-selection
[libtmux-mcp-command]: https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp
[list_sessions]: https://libtmux.org/en/go/latest/mcp/tools/list_sessions/
[mcp-assumeresponsecommit]: https://libtmux.org/en/go/latest/mcp/reference/mcp-assumeresponsecommit/
[mcp-auditenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-auditenvironmentvariable/
[mcp-binaryenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-binaryenvironmentvariable/
[mcp-capabilitiesresourceuri]: https://libtmux.org/en/go/latest/mcp/reference/mcp-capabilitiesresourceuri/
[mcp-excludetoolsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-excludetoolsenvironmentvariable/
[mcp-instance-close]: https://libtmux.org/en/go/latest/mcp/reference/mcp-instance-close/
[mcp-instance]: https://libtmux.org/en/go/latest/mcp/reference/mcp-instance/
[mcp-newserver]: https://libtmux.org/en/go/latest/mcp/reference/mcp-newserver/
[mcp-reference]: https://libtmux.org/en/go/latest/mcp/reference/
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
[send_keys_batch]: https://libtmux.org/en/go/latest/mcp/tools/send_keys_batch/
[show_environment]: https://libtmux.org/en/go/latest/mcp/tools/show_environment/
[snapshot_pane]: https://libtmux.org/en/go/latest/mcp/tools/snapshot_pane/
[split_window]: https://libtmux.org/en/go/latest/mcp/tools/split_window/
[tmux module]: ../tmux/
[topic-tool-selection]: https://libtmux.org/en/go/latest/mcp/topics/tool-selection/
[wait_for_text]: https://libtmux.org/en/go/latest/mcp/tools/wait_for_text/
[workspace-cli-load]: https://libtmux.org/en/go/latest/workspace/cli/load/
