# Changelog

Releases are tagged `-alpha`. The API is not settled, and any release may change
or remove exported identifiers without a deprecation period — pin an exact
version.

Modules are tagged per directory, so each carries its own version: the core as
`vX.Y.Z`, the consumers as `mcp/vX.Y.Z` and `workspace/vX.Y.Z`.

## Unreleased

### All modules

The Go floor is 1.26, raised from 1.23. It now tracks upstream's support
window — Go supports a release until two newer ones exist — rather than a
version chosen once, so expect it to move as releases age out.

This is a hard requirement, not a suggestion. A consumer on an older Go sees
`module ... requires go >= 1.26.0` and cannot build; `go get` will upgrade the
toolchain and rewrite the consumer's own `go` directive to match, which passes
the same floor on to anything that imports them. A toolchain pinned with
`GOTOOLCHAIN=local`, as distribution packages and air-gapped builds often do,
has no upgrade path but the toolchain itself.

Nothing about the API changed. The syntax the floor unlocks was taken across
the tree, and `golangci-lint` now gates it so the older forms cannot return.

### tmux

A control connection no longer answers the next command with the last one's
reply. tmux writes a guard block for every command it runs on a client's
behalf, not only the ones that client sent, and marks the difference in the
block's flags: 1 for a command that arrived over the control channel, 0 for
anything else. Keys delivered into a pane that is in a mode are the ordinary
way to get the others, because a mode looks each key up as a binding and runs
the command it finds. Reading a stranger's block as a reply shifted every later
reply by one for the life of the connection, which is how a server identity
probe came back holding a session listing and every tool after it failed or
answered the wrong question.

`FormatValues.SessionActive` reports tmux's `session_active`, which says whether
a session is the asking client's current one. tmux added it at 3.6 and the
format catalog did not carry it, so it could not be read typed. The catalog is
checked against tmux's own inventory, but that inventory only lists what has a
value in the asking context and no client was attached, so every client-context
variable was outside what the check could see. It now holds a control-mode
client while it reads, which needs no terminal.

`SelectLayout` accepts `main-horizontal-mirrored` and `main-vertical-mirrored`
on tmux 3.5 or newer, and refuses them below with the version it found. tmux
added the pair at 3.5 and the preset list never grew, so five supported
releases were denied an arrangement they have. The list is strict for a reason
that has not changed — tmux 3.3a exits on a name it does not know and takes
every session on the socket with it — so the pair is gated by version rather
than simply added, and a [Plan] renders it under the same gate.

`HasSession` with `Pattern` false answers for the name itself, and `NewSession`
with `KillExisting` kills the session it found rather than re-resolving the
name. tmux's exact-match marker suppresses only the last two rungs of its
lookup ladder: `=$0` still resolves the identifier and `=/dev/pts/3` still
resolves the client attached at that tty, so both reported a session nothing was
named. Creating a session named after another session's identifier therefore
destroyed that other session — and when it was the last one, the server with it.
The exact question is now answered against the session list, and the replacement
is killed by identifier. `Pattern` true still reaches tmux's full target syntax,
so nothing that worked is gone.

`RefreshClientRequest.RequestClipboard` needs tmux 3.4, lowered from 3.7, so it
now works on 3.4, 3.5, 3.6 and their point releases instead of being refused
there. tmux has carried `refresh-client -l` since before the supported range;
what 3.4 changed is that sending it stops ending the server. 3.2a ends it for
any client and 3.3a for a client with a terminal, which is why the flag is still
withheld below 3.4 rather than gated on the kind of client a caller targets.

A socket directory tmux will not use is classified as `ErrNoServer` rather than
as an unrecognised command failure. tmux keeps its sockets in
`$TMUX_TMPDIR/tmux-<uid>` and refuses that directory if anyone outside the
owner's group can reach it — the case a filesystem that does not keep Unix
permissions leaves you in. tmux 3.2a reported the refusal as `error creating`,
which was recognised; 3.3a split it into three other messages, which were not,
so the same misconfiguration answered "no server" on one supported release and
failed opaquely on the seven others. The reason tmux gave is still carried on
the error either way, because an absent socket and a directory that needs a
`chmod` need different things from the caller.

A session name carrying a control character, a DEL, or malformed UTF-8 is
refused, naming the reason. tmux only started rejecting these at 3.7; before
that it accepted them and stored the name visibility-encoded, so a bell arrived
as a backslash and an `a` and the session ended up under a name nobody asked
for. Refusing makes one name mean one thing on every supported release, which
is why the target delimiters are refused rather than left to a tmux that would
rewrite them.

### tmux/tmuxtest

`SuiteRootTagVariable` names the environment variable that tags a suite's
temporary root. go test runs packages in parallel and every suite among them
creates a root beside the others, so a test that spawns a child suite could not
tell its child's root from a sibling binary's. Setting it separates them.

### mcp

`build_workspace` says what a partial build left behind. Building is not atomic
and cannot be, because tmux has no transaction, so a document that fails part
way leaves the session and the panes made before the failure. The reply named
the pane it died on and nothing else, so a caller read it as nothing having
happened and sent the same document again — which fails on a name that already
exists, for a reason the first reply never gave. The failure now names the
session, carries its name beside its identifier, and says the two ways on.

`respawn_pane` says why it will not respawn a live pane. tmux refuses without
`-k` and reports only `respawn-pane exited 1`; the refusal now names what the
pane is running and the `kill` argument that replaces it.

Every tool that delivers keystrokes refuses a pane that is in a mode:
`send_keys`, `send_keys_batch`, `paste_text`, `paste_buffer` and `run_command`.
Copy mode reads keys as its own bindings, so the text never reached the program
and something else happened instead — and one of the bindings waits for a
further key, after which the client that sent it never gets a reply and
supplying the awaited key does not release it. It is the sender that blocks
rather than control clients in particular, so another client doing it, or a
person doing it at a keyboard, costs this server nothing: the only way to lose
the connection was to send such a key over it. `run_command` was the worst of
them, hanging past the `timeoutSeconds` its own caller set and taking the
connection with it.

The refusal names both ways on, because a caller who entered the mode
deliberately wants to read rather than to undo it: `capture_pane` with
`includeHistory` and `startLine` reads scrollback without leaving the mode or
sending anything, and `exit_copy_mode` returns the pane to the program.

`get_server_info` reports a failure as a failure, and says separately when it
could not read the message log it was asked for. tmux keeps that log per client
and before 3.6 refuses the command outright when nothing is attached, so the
answer there is neither an empty log nor a broken server: `messagesUnavailable`
carries the reason, the way `run_command` names why output is missing, and the
rest of the reply still arrives. It answered a tmux it could
not read with `alive: false`, no socket and zero of everything, which is also
what a healthy empty server looks like, so a caller could not tell "there is
nothing there" from "I could not tell". The liveness probe already reports a
server that is not running as a plain no, so an error from it means something
else and is now returned; the same goes for the snapshot behind the counts.

`select_layout` offers the mirrored presets on tmux 3.5 or newer, matching the
tmux module.

`load_buffer` refuses a name holding a backslash, naming the reason. tmux 3.7
cleans a buffer name for display before storing it and doubles a backslash
doing so, while lookup does not repeat the cleaning, so the buffer answered to a
spelling the caller was never told: the handle `load_buffer` returned reached
nothing, and the buffer could be neither read nor deleted through it. Below 3.7
the same name round-trips, which is why it is refused rather than left to the
tmux underneath.

A subscription is told about the pane it named. Every tool hands a pane back as
`%1`, so a client watching one subscribes to `tmux://panes/%1/content` — which
was accepted and then never delivered, because updates were addressed by the
sigil-less form alone and the SDK routes an update only to the sessions that
subscribed with that exact string. Nothing distinguished the silence from a pane
that never wrote. Both spellings now arrive, each in the spelling its subscriber
used, and the spellings of one pane share a coalescing interval.

Tools keep working after tmux restarts. The control connection is opened once
at startup and every command goes through it; when tmux went away the
connection died with it and nothing reopened one, so every later call answered
`control client is closed` for the life of the process and `get_server_info`
reported a running tmux as dead. Restarting tmux meant restarting every agent
attached to it. A call that finds the connection dead now retires it, so the calls after it
run on a plain one -- which is what this did before the pool existed, costing a
process per command rather than the tool. The call that finds it still reports
the restart: a wait opens a control connection of its own and ends with the
same error, so nothing here can tell a dead pool from a connection that did its
job.

One frame that will not parse no longer ends the server. MCP frames a stdio
connection with newlines and forbids a newline inside a message, so a bad frame
is one line and the next is a fresh one -- but the SDK decodes the stream
rather than the line, and a syntax error left its decoder with nothing to
resync on and was returned up through the read loop, exiting the process. A
client bug or a stray write to the pipe took every tmux tool the conversation
had with it. Lines that do not parse are now dropped before the decoder sees
them, which is what keeps it in sync, and each drop is reported on stderr.

`wait_for_text` says when `sinceEntry` ignored a match that was already there.
A caller cannot start a program and wait for it in one request, so a fast
program has already printed by the time the wait begins; `sinceEntry` then
refuses that text and the wait runs to its deadline, reporting only that
nothing was found. `matchedAtEntry` is now set beside the timeout, which says
the same call without `sinceEntry` would have matched at once.

`show_environment` reports both layers a pane inherits. tmux keeps a
server-wide environment and a per-session one and hands a new process both,
the session's overriding the server's; only the session's was read, so asking
for `PATH` reported nothing while a pane made a moment later had one. Each
entry now names the layer it came from.

`run_command` says why there is no output. A pane running something that is not
a shell takes the keys as that program's input, so the wrapper never runs;
`outputUnavailable` carried the failed open of this server's own mark file,
naming a temporary path and reading as a filesystem fault. It now says the pane
never ran the command, what it is running instead, and that `respawn_pane`
gives it a shell again.

`LIBTMUX_SOCKET` names the tmux socket when no flag does. The Python server
reads it, so a configuration written for that one reached whatever sat on tmux's
default socket here, with nothing said about it. A flag still wins, only an
operator sets either, and `-doctor` now names which of the two was taken.

Every changing tool says whether repeating it is safe. There was one annotation
for all of them, so renaming a window read as being as risky to retry as
splitting a pane; a client uses that hint to decide whether a call it never saw
the answer to can be retried. The twelve that set a state rather than take a
step now say so, and the ones with a reversing argument -- zoom toggles, a
copy-mode scroll goes further -- deliberately do not.

A batched write to the caller's own pane asks first, as a direct one already
did. The three batch tools discarded the request they were called with, so the
guard had no session to put the question to and let the write through: the same
client, declining the same `send_keys` to the same pane, had it refused directly
and delivered inside a batch, with no prompt shown. The safety tier was already
enforced through a batch; this guard was not.

A resource URI is decoded before it is used. A tmux name is not limited to what
a URI may carry, so a session called `my session` has no spelling but
`my%20session`, and a pane copied from a tool result encodes as `%250`. Both
were compared against tmux still encoded, so such an object could not be
addressed as a resource by any spelling.

Completion answers in the dialect of whatever asked. A resource slot is pasted
into a path, where an id carries no sigil and a name has to be escaped; a prompt
argument is read back by a model and handed to `paneId`, where tmux's own
spelling is the only one a tool accepts. Both were answered in the URI dialect,
so completing `diagnose_pane` produced a prompt naming a pane that every tool
rejects, and a session name holding a space built a URI that does not parse.

An unrecognised `LIBTMUX_SAFETY` now reads as `readonly` rather than `mutating`.
Someone who sets that variable is bounding what a model may do, and a typo in it
must not widen the bound; only an absent or empty variable means no preference
and keeps the default. This matches the Python server, which the comment here
had always claimed. `-tools` reports the level in force rather than the string
that was rejected.

`select_layout` refuses a layout and a spread in one call. tmux rejects the
pair, so its parser answered — naming modes this tool does not offer, and
reading as a malformed request rather than as two arguments that conflict.

The server names the signal that ended it. A client tearing the transport down
sends SIGTERM, and the exit line reported the cancellation that produced rather
than the signal itself, so an ordinary disconnect read as `context canceled` —
the mechanism, not the reason, and impossible to tell from a fault. It now
reads `terminated signal received`, or `interrupt signal received` for SIGINT.

`run_command` returns what the command actually printed. Three things could make
the reply disagree with the command. A tab anywhere in a command reached the
shell's line editor as a request to complete a filename, so a different command
ran and reported success; commands are now written to a file and sourced, and
nothing a caller sends crosses that line editor. A detached run returned the
shell's next prompt as a line of output, because the row the closing mark points
at was read whether or not the command's last line ended there; the cursor's
column is now recorded beside its row and settles that. Output was read one
entry per screen row, so a line longer than the pane arrived split — wrapped
rows are rejoined. The pane also stays readable: what it shows is one short
line rather than the whole wrapper echoed across the screen.

`run_command` refuses a pane whose program has exited, naming `respawn_pane`,
instead of waiting out its timeout on a pane that reads no keys and then
reporting the exited shell as though it were busy.

When output cannot be read, `outputUnavailable` says why. A caller could not
previously tell a command that printed nothing from a pane it failed to read.

`list_servers` reports the servers that are running. tmux leaves a socket file
behind when a server exits, so the directory only grows: on a machine that has
run test suites this was hundreds of entries, nearly all of them dead, and the
running servers were the hardest thing to find in the reply. `includeDead`
brings them back, `name` and `maxServers` narrow further, and `total` and
`skipped` say what was left out.

The batch tools name the calls they skipped. All three stop at the first
failure, and `skipped` now lists what never ran — which the mutating and
destructive batches need, since tmux has no transaction and what already ran
stays. A batch asked to call a batch says that it cannot be called from inside
one, rather than that the server does not serve it.

`send_keys` describes its `command` argument, which carries the warning that
tmux key names are read there, so `C-c` interrupts and `Escape` is a key.
`exit_copy_mode` no longer advertises `scrollUp`, which it shared with entering
copy mode and never read. `list_panes` reports `panes` as an array when nothing
matches, as `list_sessions` and `list_windows` already did.

`show_buffer` says which buffer it looked for and that only buffers this server
staged can be read, rather than reporting that a tmux command exited non-zero.

The `capture_since` cursor is half the size. It carries a fingerprint per row
and travels in every reply, so for a mostly blank screen it cost more than
sending that screen would have.

`mcp-swap` takes `--client`, so a build can be tried in one agent while the
others keep whatever they run.

`mcp-swap revert` no longer discards a configuration edited after a previous
revert. A backup is written only when none exists, which is what lets a second
swap still revert to the original; leaving that backup in place afterwards
turned the same rule into data loss, because a later revert restored the copy
from before the edit. It is removed once it has been used, so the next swap
copies the file as it is then.

The server this module builds is built against the current core. `mcp/go.mod`
required `v0.0.1-alpha.1` for the whole life of `v0.0.1-alpha.2`, and it is the
one module here with no `replace` directive, so that requirement is what a
`go install` resolves and what its own CI compiled — nineteen core commits
behind, including both Go floor raises.

The registry entry is named for the language rather than the project, since
every server under this namespace drives tmux and the namespace already says
whose it is.

Documentation no longer names a version to install. Two READMEs had told a
reader to fetch `v0.0.1-alpha.1` for the whole life of `v0.0.1-alpha.4`, and
that version is retracted, so the command they gave refused to run.

### workspace

Two windows claiming one `window_index` is refused by the document check,
with the line and both windows named. tmux refuses the second claim itself, but
its refusal is that `new-window` exited non-zero -- naming neither the index nor
which window lost, and describing an unnamed window as `""`.

A rejection that has no line to point at no longer claims "line 0". An empty or
unparseable document reported a line that cannot exist, sending a reader looking
there when the parser's own message named the real one.

## mcp/v0.0.1-alpha.4

Adds the MCP registry entry and the marker it is verified against.

## v0.0.1-alpha.2, workspace/v0.0.1-alpha.2

Documentation. The alpha notice no longer uses GitHub's alert extension, which
is literal text in every other renderer. No change to either package.

## mcp/v0.0.1-alpha.3

Listings take criteria and report the total they selected from, so asking which
pane runs a command costs one answer rather than forty. `detail: full` adds each
matching pane's process state from the snapshot the listing already takes.

Waiting is bounded by a ceiling that clamps rather than refuses, and optional:
`run_command` detaches and `get_job` collects. `wait_for_text` gains
`idleSeconds` for a program whose finishing cannot be predicted.

Writes to the pane the server itself runs in ask the person first, through MCP
elicitation. `move_pane`, styled captures, per-hook reads, and the attached
clients and message log on `get_server_info` reach tmux the tools could not.

## mcp/v0.0.1-alpha.2

Retracts `mcp/v0.0.1-alpha.1`, whose go.mod carried replace directives that
`go install` refuses.

## v0.0.1-alpha.1

First release.

### tmux

Sessions, windows, panes, and clients as typed values that never refresh behind
you: a record is what tmux said when you asked. Every tmux option and hook has a
typed accessor reporting whether a value was set here or inherited. Filters
compile to a predicate applied in Go, or push down to tmux's own format
evaluation. Errors are classified by what tmux refused, with `ErrNoServer`
separating "nothing is running" from "the question could not be answered". The
module imports only the standard library.

Commands reach tmux by subprocess or by a control-mode connection, chosen per
server, so the cost of a call is a decision rather than a default.
`BENCHMARKS.md` reports what each mode costs.

Supports Go 1.23 and newer, and tmux 3.2a through 3.7b, checked against every
release in that range.

### tmux/tmuxtest

A real tmux server for a test, isolated down to its socket, configuration, and
environment, cleaned up with the test that created it — verified dead rather
than assumed. Screen assertions poll rather than sleep, so a quick program costs
milliseconds and a slow one is still waited for, and a failure reports the
screen the pane last held.

### tmuxq

Query helpers over slices of records, free of the tmux model.

### workspace

Loads tmuxp-style YAML workspaces and builds them.

### mcp

Serves one tmux server to Model Context Protocol clients. Reads panes
incrementally through a cursor, runs a command and reports how it ended rather
than leaving a client to scrape the screen, and hides tools above a chosen
safety tier.
