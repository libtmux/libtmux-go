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

Without a selector, the server pins the named socket `libtmux-mcp` and uses the
package's shipped minimal configuration when it starts a new daemon there.
Explicitly selected new servers report user-configured provenance; existing
servers remain unknown. Startup writes a random owner marker through that
minimal configuration, then reads it back; only the process whose marker
survived the launch race gets the default `teardown` toolset. The nonce is
removed from tmux's environment.

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
      "env": {"LIBTMUX_TOOLSETS": "inspect,manage,execute"}
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
| `-tools` | the startup-frozen tools, toolsets, and process reach a client sees |
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
command and reports success before anything happened. `run_shell_command` waits for
the command and returns its exit status and output without reading the screen at
all; `wait_for_text` watches what a pane writes; `capture_since` returns what a
pane wrote since the last look rather than its whole screen again. That is the
difference — not more access to tmux, but a better place to put the control
loop, and a smaller bill for keeping it there.

## Not spending the agent's turn

Three things here exist because an agent's context and its turn are the scarce
resources, not tmux:

- **Reads are bounded.** Captures keep the newest requested lines and say what
  they omitted. Pattern input is capped before compilation; search inspects at
  most 200 panes, 20,000 lines, 1,000,000 bytes, and five seconds of matching
  work.
- **One batch keeps typed calls typed.** `call_read_tools_batch` accepts up to
  sixteen inspect operations, validates each operation's own schema, and keeps
  each full nested MCP result envelope that fits. Every row reports its index,
  success or error, and truncation; the complete JSON-RPC response is at most
  1,000,000 bytes. A serialized request ID may use at most 524,288 bytes;
  a larger ID fails before any tool runs.
- **Checking is not capturing.** Listing tools report metadata without reading
  pane content. `snapshot_pane` is the explicit combined metadata-and-content
  operation.

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

Synchronized input protects every configured member, not only the requested
source. The server validates the complete membership before asking, so a dead
or modal peer refuses the operation without prompting or changing tmux.

## Limiting what a client can do

`LIBTMUX_TOOLSETS` selects an unordered subset of `inspect`, `manage`, `execute`,
and `teardown`. The inherited default is the first three; an explicitly empty
value selects no toolset. There is no `none` sentinel.

`LIBTMUX_TOOLS` then includes exact names, and `LIBTMUX_EXCLUDE_TOOLS` removes
exact names after every inclusion. Exclusion also prunes
`call_read_tools_batch`'s native operation schema and nested authority. Unknown
names and leading, trailing, or interior empty list items stop startup before
tmux opens. A present `LIBTMUX_SAFETY`, `LIBTMUX_MCP_CAPABILITIES`, or
`LIBTMUX_MCP_PROMPTS_AS_TOOLS` also stops startup with migration guidance.

### Moving from an earlier alpha

The capability switch changes names and the default socket together. Use this
mapping when updating an existing client entry:

| Earlier setting | Current setting |
| --- | --- |
| `LIBTMUX_SAFETY=readonly` | `LIBTMUX_TOOLSETS=inspect` |
| `LIBTMUX_SAFETY=mutating` | `LIBTMUX_TOOLSETS=inspect,manage,execute` |
| `LIBTMUX_SAFETY=destructive` | all four toolsets on a process-created minimal daemon |
| `LIBTMUX_MCP_CAPABILITIES=` | Start with `LIBTMUX_TOOLSETS=inspect`, then exclude content and configuration readers to retain the earlier metadata-only default |
| `LIBTMUX_MCP_CAPABILITIES=inspect` | `LIBTMUX_TOOLSETS=inspect` |
| `LIBTMUX_MCP_CAPABILITIES=operate` | `LIBTMUX_TOOLSETS=inspect,manage,execute` |
| `LIBTMUX_MCP_CAPABILITIES=all` | all four toolsets |
| `LIBTMUX_MCP_CAPABILITIES=metadata-read` or `LIBTMUX_MCP_CAPABILITIES=content-read` | `inspect`; use exact exclusions when the earlier metadata/content split matters |
| `LIBTMUX_MCP_CAPABILITIES=pane-control` | `execute`; arbitrary tmux-format expansion has no replacement |
| `LIBTMUX_MCP_CAPABILITIES=workspace-create` | `execute`; current creation tools accept no program, so start bounded work with `run_shell_command` |
| `LIBTMUX_MCP_CAPABILITIES=tmux-layout` | `manage`; use exact inclusions for the earlier narrower group |
| `LIBTMUX_MCP_CAPABILITIES=tmux-settings` | Use exact constrained setters from `manage` and `execute`; generic buffer, environment, and option mutation is removed |
| `LIBTMUX_MCP_CAPABILITIES=tmux-destroy` | `teardown`; server-wide teardown has no replacement |
| `LIBTMUX_MCP_TOOLS=a,b` | `LIBTMUX_TOOLS=a,b`, with exclusions available separately |
| `LIBTMUX_MCP_PROMPTS_AS_TOOLS=1` | removed; use the [typed workflow mapping](TOOLS.md#retired-prompt-workflow-mapping) |
| implicit ordinary tmux socket | explicit `LIBTMUX_SOCKET=default` or another chosen name |

An unchanged launch now opens the dedicated `libtmux-mcp` world, so it may look
empty until the agent creates a session. The startup line prints the exact
attach command. Retired tool names and their workflow replacements are listed
in the [generated tool reference](TOOLS.md#retired-mcp-surface).

Tool filtering shapes the interface; it is not an operating-system sandbox.
All calls run with the tmux user's authority. Every listed tool carries its full
capability row under `_meta["com.git-pull.libtmux-mcp/capability"]`: process
reach, tmux effects, output classes, secret and untrusted-output flags,
input literalization, native schemas, nested authority, and conservative MCP
annotations. The manifest's complete input-sink table stays internal and is
validated against every native schema.

## Everything else an operator can set

| Variable | Does |
| --- | --- |
| `LIBTMUX_TOOLSETS` | selects any unordered subset of the four toolsets |
| `LIBTMUX_TOOLS` | includes exact tool names after toolset expansion |
| `LIBTMUX_EXCLUDE_TOOLS` | removes exact names after every inclusion |
| `LIBTMUX_SOCKET_PATH` | selects an absolute socket path; mutually exclusive with `LIBTMUX_SOCKET` |
| `LIBTMUX_SOCKET` | names the tmux socket when no socket flag selects one |
| `LIBTMUX_TMUX_CONFIG` | selects a nonempty absolute tmux configuration path |
| `LIBTMUX_TMUX_BIN` | selects the tmux executable when `-binary` is empty |
| `LIBTMUX_MCP_WAIT_MAX_SECONDS` | the longest any one wait may run; 300 by default |
| `LIBTMUX_AUDIT` | `stderr`, or a path, to record every call |

The names match the Python server, so an operator running both writes one thing.
Flags override their corresponding socket variables; every selection is frozen
when the server starts, and a client cannot change the target afterwards.

A wait longer than the ceiling is shortened rather than refused, and the reply
says so in `effectiveTimeoutSeconds` and `timeoutClamped`. The ceiling bounds
the caller rather than the transport: these tools await throughout, so a long
wait blocks nothing else. What an unbounded one costs is the agent's turn, and
MCP gives it no way to change its mind mid-call.

`tmux://capabilities` is the only resource. Its static payload reports the
schema version, frozen state, startup-pinned connection and configuration
provenance, effective tool names, selection, common trust boundaries, and the
same capability rows carried by tool discovery. There are no dynamic resources,
templates, prompts, completion routes, or background job handles.

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

Forty-five tools, each with the arguments a client sends and what comes back,
plus gotchas and what the server logs:

**[Tool reference →](TOOLS.md)**

That page is reference material, read by search rather than read through, which
is why it is not here.

Copy mode stays outside the MCP surface. Captures, snapshots, searches, and
cursors read pane output without taking over an attached person's modal view;
`get_pane_info` reports when a mode already owns input. The Go tmux module
retains its copy-mode API for applications that own that interaction.

Input tools read effective pane synchronization from a fresh snapshot. Direct
sends and batch rows report sorted configured membership, while
`run_shell_command` requires a configured singleton before setup and again
before dispatch. `paste_text` is target-only; optional Enter has its own
configured membership. These IDs describe observed configuration, not proven
delivery, because tmux state can change after the check.

A second [libtmux-mcp server](https://github.com/tmux-python/libtmux-mcp) for
tmux is written in Python.
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

**Tools are missing rather than failing.** `LIBTMUX_TOOLSETS`, `LIBTMUX_TOOLS`,
or `LIBTMUX_EXCLUDE_TOOLS` shaped the startup surface. `-tools` prints the same
listed surface a client receives.

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
two. The tool names, native schemas, and results are the same ones a client sees
over stdin and stdout.

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
| codex | `~/.codex/config.toml` | TOML |
| cursor | `~/.cursor/mcp.json` | JSON |
| gemini | `~/.gemini/settings.json` | JSON |
| grok | `~/.grok/config.toml` | TOML |
| agy (`antigravity` alias) | `~/.gemini/config/mcp_config.json` | JSON |
| opencode | `$XDG_CONFIG_HOME/opencode/opencode.jsonc` | JSONC |
| pi | `~/.pi/agent/mcp.json` | JSONC |

Pi itself has no MCP client. Its configuration is read by the third-party
`pi-mcp-adapter` extension, and `status` reports when that adapter is absent.

All of them, not the JSON ones only. The entry has one name across every
client, so swapping some of them leaves two different servers answering to
`tmux` and nothing saying which client got which.

The TOML and JSONC files are edited in place rather than parsed and rewritten.
They hold other servers, other settings, and comments explaining why something
is set the way it is; a decode-and-write reformats all of that. So the entry's
bytes are located and replaced, and every other byte is left alone. Keys this
tool does not write survive — grok's `enabled`, for instance — and so does the
entry's environment, because toolsets, named filters, and socket selection are
configuration rather than a choice of build.

The complete selected set is parsed and rendered before any file changes. Each
new backup destination is checked at the same time, so one malformed config or
unusable backup leaves every selected client unchanged.

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
