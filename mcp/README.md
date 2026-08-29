<!-- mcp-name: io.github.libtmux/tmux-mcp-go -->

# mcp

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/mcp.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp)

Alpha software. Releases carry an -alpha prerelease tag and the API is not
settled. Pin an exact version.

Serve one tmux server to Model Context Protocol clients, built on the
[tmux module] and the [Go MCP SDK].

This is a consumer of the tmux module, not part of it. The tmux module takes no
runtime dependency; speaking MCP needs one, so this lives in its own module and
`go get` on the tmux module never pulls it in.

**Contents** — [Installing it](#installing-it) · [What it feels like](#what-it-feels-like)
· [When it earns its keep](#when-it-earns-its-keep) ·
[Knowing its own pane](#knowing-its-own-pane) ·
[Limiting what a client can do](#limiting-what-a-client-can-do) ·
[The tools](#the-tools) · [Troubleshooting](#troubleshooting) ·
[Embedding it](#embedding-it) · [Developing on it](#developing-on-it)

## Installing it

**Requirements:** Go 1.26+, and tmux 3.2a or newer on `$PATH`.

On supported releases before 3.6, the initially attached session's
`detach-on-destroy` setting applies. Destroying that session can end the MCP
runtime; tmux 3.6 and later can move its control client to another remaining
session.

```console
$ go install github.com/libtmux/libtmux-go/mcp/cmd/libtmux-mcp@latest
```

That puts `libtmux-mcp` in `$(go env GOPATH)/bin`. An MCP client launches it as
a subprocess and speaks to it over stdin and stdout.

The default server exposes topology metadata only. Set
`LIBTMUX_MCP_CAPABILITIES=operate` in the server's environment to enable the
ordinary workspace, pane, content, layout, and settings tools. Destruction
also requires `LIBTMUX_SAFETY=destructive` and the `tmux-destroy` capability.

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
      "args": ["-socket-name", "my-application"],
      "env": {"LIBTMUX_MCP_CAPABILITIES": "operate"}
    }
  }
}
```

Every client takes the same flags. They select the tmux server once, at
startup, and a client cannot change it afterwards:

| Flag | Meaning |
| --- | --- |
| `-socket-path` | explicit socket path; nonempty has highest precedence |
| `-socket-name` | tmux socket name; nonempty precedes both socket environment variables |
| `-binary` | tmux executable; empty uses `LIBTMUX_TMUX_BIN`, then resolves `tmux` through `PATH` |

Before serving or running `-doctor`, the command resolves the tmux binary once
from its startup environment and working directory. A bad path fails at
startup, and later environment or directory changes cannot retarget it. A tmux
server that is not running is not an error: tmux starts one on demand.

Three more flags answer questions without serving MCP over stdio, which is
what a config entry that will not start actually needs:

| Flag | Answers |
| --- | --- |
| `-version` | which build this is |
| `-tools` | what a client would be offered, and how safety and capabilities changed it |
| `-doctor` | which socket it reaches, what is on it, and whether it is running inside that tmux itself |

`-version` and `-tools` do not resolve or contact tmux.

```console
$ libtmux-mcp -doctor -socket-name my-application
```

```
libtmux-mcp doctor
  tmux:    3.7b
  socket:  /tmp/tmux-1000/my-application (from -socket-name)
  holds:   1 sessions, 1 windows, 1 panes, 0 clients attached
  safety:  mutating
  access:  metadata-read
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

## Not spending the agent's turn

Three things here exist because an agent's context and its turn are the scarce
resources, not tmux:

- **A listing narrows.** `list_panes` takes `sessionName`, `windowId`,
  `command`, `pathUnder`, `dead`, and `active`, and reports the `total` it
  selected from. On a real 18-pane server, asking which pane runs `vim` is 535
  bytes where the whole listing is 7.3 kB.
- **A command need not be waited for.** `run_command` with `detach` returns a
  `jobId` at once; `get_job` collects the exit status and output later. A build
  costs what typing it costs.
- **Checking is not capturing.** `list_panes` with `detail: full` adds every
  matching pane's exit status, path, title, and history size from the snapshot
  it already took — one tmux command for eight panes, and no pane's contents
  read at all.

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

## Asking before it types into your terminal

A pane reported with `isCaller` true is the one this server runs in, and typing
into it reaches the terminal you are talking to it through. A note in a reply is
something a model with a task does not always read, so a write to that pane asks
first, through MCP elicitation, and a decline fails the call.

A client that did not declare the elicitation capability is refused. This
protects the caller pane rather than making the tools a sandbox: a caller with
`send_keys` can still run anything you can in another pane.

A write reached through a batch asks in the same way a direct one does. The
question goes to the client that sent the batch, and declining fails that call
and stops the batch there.

## Limiting what a client can do

Two independent checks bound what the server advertises. A tool must pass both.

`LIBTMUX_MCP_CAPABILITIES` selects the kinds of access granted. Empty or unset
is `metadata-read` only. It accepts a comma-separated list:

| Capability | Grants |
| --- | --- |
| `metadata-read` | identities, topology, process state, and geometry, but no pane contents or configuration values |
| `content-read` | pane output, buffers, option values, hooks, environment values, jobs, and tmux messages |
| `pane-control` | pane input and tmux features that may run shell commands, including arbitrary format expansion |
| `workspace-create` | session, window, pane, and workspace creation, including programs they start |
| `tmux-layout` | selection, movement, resizing, layouts, and names |
| `tmux-settings` | buffers, environment variables, and options |
| `tmux-destroy` | ending panes, windows, sessions, or the server |

Three profiles save spelling: `inspect` is both read capabilities, `operate`
is every capability except `tmux-destroy`, and `all` is every capability. An
unknown value is reported and grants nothing; if no value is recognized, the
server falls back to `metadata-read`.

`LIBTMUX_SAFETY` is the independent operation ceiling:

| Value | Offers |
| --- | --- |
| `readonly` | only the tools that read tmux |
| `mutating` | those plus the ones that change it, and is the default |
| `destructive` | those plus the ones that end something: `kill_pane`, `kill_window`, `kill_session`, `kill_server` |

An unset or empty variable takes the default. A value naming no level takes
`readonly`, because setting the variable at all is asking for a bound and a
typo in it must not widen one; `-tools` reports the level in force rather than
the string that was rejected.

A tool above the level or outside the capability allowlist is never advertised,
so no prompt reaches it, and a batch cannot reach around either bound. Pane
content resources and subscriptions require `content-read`; metadata resources
require `metadata-read`. The active bounds are stated in the server
instructions, so a shorter tool list is explainable. Safety is derived from
each tool's annotations, while its capability is declared beside its
registration.

## Everything else an operator can set

| Variable | Does |
| --- | --- |
| `LIBTMUX_SAFETY` | bounds which tools are advertised, as above |
| `LIBTMUX_MCP_CAPABILITIES` | allowlists independent access classes; defaults to `metadata-read` |
| `LIBTMUX_SOCKET_PATH` | selects an explicit socket path when both socket flags are empty; it precedes `LIBTMUX_SOCKET` |
| `LIBTMUX_SOCKET` | names the tmux socket when no path or `-socket-name` selects one |
| `LIBTMUX_TMUX_BIN` | selects the tmux executable when `-binary` is empty |
| `LIBTMUX_MCP_WAIT_MAX_SECONDS` | the longest any one wait may run; 300 by default |
| `LIBTMUX_MCP_PROMPTS_AS_TOOLS` | `1` also offers the recipes as a `get_recipe` tool, for clients that do not read MCP prompts |
| `LIBTMUX_AUDIT` | `stderr`, or a path, to record every call |

The names match the Python server, so an operator running both writes one
thing. Flags override their corresponding variables; both are resolved once
when the server starts, and a client cannot change the target afterwards.

A wait longer than the ceiling is shortened rather than refused, and the reply
says so in `effectiveTimeoutSeconds` and `timeoutClamped`. The ceiling bounds
the caller rather than the transport: these tools await throughout, so a long
wait blocks nothing else. What an unbounded one costs is the agent's turn, and
MCP gives it no way to change its mind mid-call.

## Publishing it to the MCP registry

`server.json` is this server's entry for the [MCP registry], where clients look
for servers by name rather than by import path. It carries no `packages` block:
the registry knows npm, PyPI, NuGet, Cargo, OCI and prebuilt binaries, and `go
install` is none of them, so the entry points at this repository instead.

The name is `io.github.libtmux/tmux-mcp-go`, which the registry ties to the
GitHub organisation of the same name, and the `mcp-name:` comment at the top of
this file is the marker it looks for. The language rather than the project,
because every server in that namespace drives tmux: `libtmux-mcp` would repeat
what `io.github.libtmux` already says and leave the one distinguishing thing
unsaid.

The publisher is a Go program, so the toolchain this repository already needs
builds it. It installs as `publisher`, though its own help calls it
`mcp-publisher`:

```console
$ go install github.com/modelcontextprotocol/registry/cmd/publisher@latest
```

Homebrew ships it under the second name, as does the release tarball the
[registry quickstart] links:

```console
$ brew install mcp-publisher
```

Checking the entry against the live registry needs no credentials, and is worth
doing before a release rather than after:

```console
$ publisher validate
```

Publishing needs the organisation's:

```console
$ publisher login github
```

```console
$ publisher publish
```

[registry quickstart]: https://modelcontextprotocol.io/registry/quickstart

[MCP registry]: https://registry.modelcontextprotocol.io/

## The tools

Fifty-odd tools, each with the arguments a client sends and what comes back,
plus recipes, gotchas, and what the server logs:

**[Tool reference →](TOOLS.md)**

That page is reference material, read by search rather than read through, which
is why it is not here.

There is a second MCP server for tmux under the same name, written in Python.
The two serve the same tmux and answer to the same clients, and where they
differ is set out separately:

**[Against the Python server →](PARITY.md)**

## Troubleshooting

**Ask the server first.** `-doctor` answers most of what follows without a
client in the way:

```console
$ libtmux-mcp -doctor -socket-name my-application
```

**The client shows no tmux tools.** The server never started. Run the exact
command from your client's config by hand: a bad `-binary`, or a path that is
not on the client's `PATH`, fails at startup and says so.

**Tools are missing rather than failing.** `LIBTMUX_SAFETY` or
`LIBTMUX_MCP_CAPABILITIES` withheld them. `-tools` prints the surface and both
bounds; both are also stated in the server instructions the client received.

**It reaches the wrong tmux.** `-doctor` names the socket it addresses and
lists the others on the machine. A client's environment is not your shell's:
`TMUX_TMPDIR` set in your profile is not set for a server the client spawned.

**Sessions you can see are reported as nothing there.** Compare the tmux
version `-doctor` prints against your shell's `tmux -V`. A client starts its
servers with a curated `PATH`, which can resolve a different tmux than the one
that started your sessions, and a tmux client cannot talk to a server built
from another protocol version — it reports `server exited unexpectedly`, which
is indistinguishable from a server that has gone. Point `-binary` at the tmux
your sessions belong to.

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
    tmuxmcp "github.com/libtmux/libtmux-go/mcp"
    "github.com/libtmux/libtmux-go/tmux"
    sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)
```

```go
target, err := tmux.NewServer(tmux.ServerOptions{SocketName: "app"})
if err != nil {
    return err
}
instance, err := tmuxmcp.NewServer(target)
if err != nil {
    return err
}
defer instance.Close()
session, err := instance.Connect(
    ctx,
    tmuxmcp.AssumeResponseCommit(transport),
    nil,
)
```

`tmuxmcp.NewServer` returns a managed `Instance`; close it after serving. A
custom transport must commit one response per successful write and use
`AssumeResponseCommit`, as above. See the [package documentation] for lifecycle,
capacity, isolation, and transport contracts.

This package is named `mcp` and so is the SDK's, so a file using both has to
rename one of them.

[package documentation]: https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp

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

To try a build in one agent while the others keep whatever they run, name it:

```console
$ go run ./cmd/mcp-swap use-local \
    --client claude \
    --mode build
```

It points the agent CLIs on this machine at a build of this server and puts
them back. It writes only the `tmux` entry, only in global config, and without
`--client` it writes every client it knows:

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
entry's environment, because `LIBTMUX_SAFETY` and
`LIBTMUX_MCP_CAPABILITIES` are configuration rather than a choice of build.

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

Before writing anything, every distinct process the selected clients would run
is started and asked to complete an MCP handshake. Identical entries share one
check, while preserved client environment is checked separately. A build error,
a missing binary, or a version the module proxy has never heard of otherwise
lands in every config at once and shows up later as a server that will not
start, separately, in each client. Pass `--no-preflight` to skip it when
offline.

`released` needs the Go module to be published under a tag the proxy can
resolve. When one cannot be resolved — an unpublished version, or a proxy that
has not seen it yet — the preflight says so rather than writing an entry that
cannot start.

Driving it by hand still works:

```console
$ go run ./cmd/libtmux-mcp -socket-name my-application
```

It reads JSON-RPC from stdin, so a pipe that closes immediately ends the server
before it answers. Hold stdin open while waiting for replies. See
[`AGENTS.md`](AGENTS.md) for what else is worth knowing before
testing this by hand.
