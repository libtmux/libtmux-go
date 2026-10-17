# MCP server contracts

What `libtmux-mcp` and the `mcp` package promise beyond what the
[README](../README.md) needs to get a client running. Each section is the
full text behind a short bullet there. Tool arguments and results are in
[`TOOLS.md`](../TOOLS.md), and the differences from the Python server are in
[`PARITY.md`](../PARITY.md).

## Which tmux it serves

Which tmux server it serves is chosen by flags at startup and cannot be changed
by a client, so a client reaches only the socket the operator selected. The
choice is made once, when the server starts, and a client cannot change it
afterwards.

Without a selector, it uses the dedicated socket `libtmux-mcp` and the shipped
minimal configuration. A random launch marker proves which process created the
daemon before default teardown tools are exposed, then disappears from tmux's
environment.

Otherwise set exactly one of [`LIBTMUX_SOCKET`][mcp-socketenvironmentvariable]
and the absolute [`LIBTMUX_SOCKET_PATH`][mcp-socketpathenvironmentvariable].
[`LIBTMUX_TMUX_CONFIG`][mcp-tmuxconfigenvironmentvariable], when present, must
be a nonempty absolute path. Explicit or existing targets never gain teardown
by default.

The tmux executable is resolved once, from the environment and directory the
server started in. A bad path fails at startup, and nothing later can point
the server at another tmux. A tmux server that is not running is not an
error: tmux starts one when something asks it to.

## The tmux version

The server requires tmux 3.3a or newer and refuses an older binary before the
MCP transport starts.

A tool that meets an older daemon later answers with an error result naming the
floor and the fix: restart the tmux server on that socket from a newer tmux, or
point [`LIBTMUX_TMUX_BIN`][mcp-binaryenvironmentvariable] at one and restart
the server. [`get_server_info`][get_server_info] reports `minimumVersion`
beside `version`, and `-doctor` prints both.

## Settings and precedence

Flags choose the tmux server once, at startup. A flag wins over its variable,
and everything is fixed when the server starts. The names match the Python
server's, so an operator running both writes one thing.

Other flags answer a question and exit, which is what a config entry that will
not start needs. `-version` needs no tmux. `-tools` asks the socket it would
serve whether a tmux server runs there, because that decides whether `teardown`
is in the default, so it needs the tmux executable.

`-tools` and `-doctor` drive the server through its own protocol in memory, so
they report what a client sees rather than what the code intends.

## Toolsets and the default

[`LIBTMUX_TOOLSETS`][mcp-toolsetsenvironmentvariable] takes the names of the
toolsets to grant, separated by commas, in any order. Without it the server
grants `inspect`, `manage`, and `execute`.

`teardown` joins that default in one case only: no socket was chosen, and
this process is the one that started the tmux server on the `libtmux-mcp`
socket. There is then nothing of yours for a kill to reach. Everywhere else,
ask for it by name.

A value that is set and empty grants no toolset. There is no `none` keyword.

## Exact tools and exclusions

[`LIBTMUX_TOOLS`][mcp-toolsenvironmentvariable] adds tools by name to what the
toolsets grant, and
[`LIBTMUX_EXCLUDE_TOOLS`][mcp-excludetoolsenvironmentvariable] then removes
tools by name. Excluding a tool also removes it from what
[`call_read_tools_batch`][call_read_tools_batch] may call. A name that matches
no tool, or an empty item in either list, stops the server at startup, before
it opens tmux.

`LIBTMUX_SAFETY`, `LIBTMUX_MCP_CAPABILITIES`, and
`LIBTMUX_MCP_PROMPTS_AS_TOOLS` are names from earlier releases. Setting one
stops startup with what to set instead, and [MIGRATING.md](../MIGRATING.md)
maps each earlier setting to its current one.

Choosing tools shapes what a client is offered. Every call runs with your
user's authority, and a client holding [`send_keys`][send_keys] can run
anything you can.

## Capability disclosure

`_meta` is the field MCP gives a server for data of its own.

Every listed tool carries a description of what it reaches and changes there,
under `_meta["com.git-pull.libtmux-mcp/capability"]`: which processes it can
start or type into, what it does to tmux, what kinds of output it returns,
whether that output may hold secrets or text a pane wrote, and how its
arguments are kept from being read as tmux syntax.

[`tmux://capabilities`][mcp-capabilitiesresourceuri] is the server's one
resource, and reports the same descriptions beside the selection in force.

The selected tools and the static [`tmux://capabilities`][tmux-capabilities]
resource come from one native manifest, so each tool carries the same
capability row the resource reports.

The resource also reports its schema version, frozen surface, effective names,
connection provenance, and common trust boundary.

## Bounded reads and batches

Captures keep the newest lines asked for and say what they left out. A search
looks at no more than 200 panes, 20,000 lines, and 1,000,000 bytes, and
matches for no more than five seconds.

[`call_read_tools_batch`][call_read_tools_batch] takes up to sixteen `inspect`
calls, checks each against its own tool's schema, and answers with one row per
call: its index, whether it succeeded, and whether its result was cut short.
The whole reply is at most 1,000,000 bytes.

The listing tools report what exists without reading what any pane shows.
[`snapshot_pane`][snapshot_pane] is the one call that returns a pane's metadata
and its content together.

## Waits and the ceiling

A wait longer than the ceiling is shortened rather than refused, and the reply
says so in `effectiveTimeoutSeconds` and `timeoutClamped`. A long wait blocks
nothing else in the server. What it costs is the agent's turn, and MCP gives an
agent no way to change its mind in the middle of a call.

The ceiling is
[`LIBTMUX_MCP_WAIT_MAX_SECONDS`][mcp-waitceilingenvironmentvariable].

## Input and synchronized panes

**Input goes where tmux sends it.** With `synchronize-panes` on, tmux types
into every pane of the window, not only the one named.
[`send_keys`][send_keys] reports the panes it found linked that way before it
sent anything. [`run_shell_command`][run_shell_command] refuses a pane linked
with others, because one exit status cannot describe several panes.
[`paste_text`][paste_text] reaches its target only.

**A pane that has just been created is given time to start.** Until a new
pane's shell reaches its prompt, tmux reports whatever its start-up files are
running. Input tools wait up to two seconds for that to settle rather than
refuse, so [`split_window`][split_window] followed at once by
`run_shell_command` works.

**Copy mode is left to the person.** Captures, snapshots, searches, and
cursors read pane output without taking over the view of someone attached, and
[`get_pane_info`][get_pane_info] reports when a pane is already in a mode.

## The pane the server runs in

A pane this server runs in is the terminal the conversation is happening
through.

Killing it, clearing it, or typing into it is not like doing those things to
any other pane, so every pane the server reports carries `isCaller`: true when
it is this one, false when it is not, and null when the server is not in a pane
at all. [`get_server_info`][get_server_info] answers the same question
directly.

tmux tells a process in a pane which pane it is through `TMUX_PANE` and `TMUX`,
and that is the first thing checked. It is not enough on its own: an MCP client
starts its servers with an environment it chooses, and most choose one carrying
neither.

So when the environment says nothing, the server finds the pane another way. It
descends from whatever tmux started in that pane, so the pane whose process is
one of its own ancestors is its own.

Typing into the pane with `isCaller` true reaches the terminal you are talking
to the agent through.

A note in a reply is something a model with a task does not always read, so a
write to that pane asks you first, through MCP's elicitation request: a
question the client puts to you on the server's behalf. Declining fails the
call, and a client that cannot ask is refused.

A write reached through a batch asks in the same way, and declining stops the
batch there. With `synchronize-panes` on, the question covers every pane the
input would reach, so a write that would arrive in your pane by way of another
still asks.

A linked pane that is dead or in a mode refuses the whole write before anyone
is asked.

This protects the pane you are in. Every other call still runs with your
user's authority.

## Known limits

- **Some tools require a newer tmux.** Startup and ordinary tools support
  3.3a, including `capture_since` and `run_shell_command`. Non-empty
  `paste_text` requires 3.4 before staging any buffer; `wait_for_text` requires
  the qualified 3.7 control engine. Restricted tools return an explicit version
  error on older supported daemons. The runtime uses subprocess commands below
  3.7 and a retained control connection from 3.7.
- **Every tool is annotated as destructive.** Each one,
  [`list_sessions`][list_sessions] included, carries `destructiveHint=true` and
  `openWorldHint=true`, and none carries `readOnlyHint`. A client that skips
  approval for read-only tools prompts on all of them. Classify by
  `tmuxEffects` in each tool's `_meta` instead.
- **`tools/list` is large.** More than half of the reply is the `_meta`
  description, which repeats each tool's text and schemas. A client that wants
  a smaller reply can ignore `_meta`: each tool's text and schemas are also at
  the top level.
- **Some tools spell their results differently.** `run_shell_command`,
  `send_keys`, `paste_text`, and
  [`clear_pane_scrollback`][clear_pane_scrollback] return fields such as
  `exit_status`; every other tool returns fields such as `paneId`. Read a
  tool's schema before relying on a field, and see [PARITY.md](../PARITY.md)
  for why.
- **One tmux server for the life of the process.** A client cannot name
  another socket in a call.
- **An empty `TMUX` counts as set.** Input tools refuse when `TMUX` or
  `TMUX_PANE` is set to nothing, because the server then cannot tell whether
  it is in a pane. Unset them instead.
- **Nothing but tools.** There is one resource, and no prompts, resource
  templates, completions, or background job handles.

## Troubleshooting

**Ask the server first.** `-doctor` answers most of what follows without a
client in the way.

**The client shows no tmux tools.** The server never started. Run the exact
command from your client's config by hand: a bad `-binary`, or a path that is
not on the client's `PATH`, fails at startup and says so.

**Tools are missing.** `LIBTMUX_TOOLSETS`, `LIBTMUX_TOOLS`, or
`LIBTMUX_EXCLUDE_TOOLS` shaped what is offered. `-tools` prints what a client
receives.

**It sees none of your sessions.** Without `-socket-name` it drives a tmux
server of its own. `-doctor` names the socket it reaches.

**It reaches the wrong tmux.** A client's environment is not your shell's:
`TMUX_TMPDIR` set in your profile is not set for a server the client started.

**Sessions you can see are reported as nothing there.** Compare the tmux
version `-doctor` prints with your shell's `tmux -V`. A client starts its
servers with a `PATH` of its own, which can find a different tmux than the one
that started your sessions, and a tmux client cannot talk to a server of
another version.

It reports `server exited unexpectedly`, which reads the same as a server that
has gone. Point `-binary` at the tmux your sessions belong to.

**A command works in your shell but not through a tool.** The pane's shell is
not your shell. It has the environment tmux gave it when it started.
[`show_environment`][show_environment] reports what a *new* pane would
inherit, which is not what one already running has.

## Embedding the server

This package is named `mcp` and so is the SDK's, so a file using both has to
rename one of them. The block in the README imports this one as `tmuxmcp` and
the SDK as `sdk`. `NewServer` checks the target and starts nothing;
[close the instance][mcp-instance-close] when you have finished serving.

The server needs to know that each response reaches the client as soon as the
transport's write returns, rather than waiting in a buffer behind another.
Standard input and output are known to.

Any other transport must be wrapped in
[`AssumeResponseCommit`][mcp-assumeresponsecommit] to say so, as the block does
for the in-memory one. The
[reference](https://libtmux.org/en/go/latest/mcp/reference/) has the rest of
what an embedder is held to.

## Running the command

A client starts `libtmux-mcp` as a subprocess; you rarely run it yourself. It
waits on stdin, and nothing is printed, because stdout is the protocol.

**`libtmux-mcp: terminated signal received` is routine shutdown.** It is the
server handling the SIGTERM it gets when a client tears the transport down,
including when a client's connect timeout fires.

**Give it the environment a client would.** A client starts its servers with a
curated environment, which differs from your shell's. Running it from an
inherited shell environment hides the bugs that only appear under a curated
one.

[call_read_tools_batch]: https://libtmux.org/en/go/latest/mcp/tools/call_read_tools_batch/
[clear_pane_scrollback]: https://libtmux.org/en/go/latest/mcp/tools/clear_pane_scrollback/
[get_pane_info]: https://libtmux.org/en/go/latest/mcp/tools/get_pane_info/
[get_server_info]: https://libtmux.org/en/go/latest/mcp/tools/get_server_info/
[list_sessions]: https://libtmux.org/en/go/latest/mcp/tools/list_sessions/
[mcp-assumeresponsecommit]: https://libtmux.org/en/go/latest/mcp/reference/mcp-assumeresponsecommit/
[mcp-binaryenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-binaryenvironmentvariable/
[mcp-capabilitiesresourceuri]: https://libtmux.org/en/go/latest/mcp/reference/mcp-capabilitiesresourceuri/
[mcp-excludetoolsenvironmentvariable]: https://libtmux.org/en/go/latest/mcp/reference/mcp-excludetoolsenvironmentvariable/
[mcp-instance-close]: https://libtmux.org/en/go/latest/mcp/reference/mcp-instance-close/
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
[tmux-capabilities]: https://libtmux.org/en/go/latest/mcp/resources/tmux-capabilities/
