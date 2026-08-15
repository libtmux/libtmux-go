# mcp

[![Go Reference](https://pkg.go.dev/badge/github.com/libtmux/libtmux-go/mcp.svg)](https://pkg.go.dev/github.com/libtmux/libtmux-go/mcp)

> [!WARNING]
> **Alpha.** Releases are tagged `-alpha` and the API is not settled. Pin an
> exact version.

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

## The tools

Thirty-odd tools, each with the arguments a client sends and what comes back,
plus recipes, gotchas, and what the server logs:

**[Tool reference →](TOOLS.md)**

That page is reference material, read by search rather than read through, which
is why it is not here.

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
