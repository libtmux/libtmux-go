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

Ending a window, a session, or the tmux server is asked about when it holds
the pane this server is running in, the same way writing into that pane already
was. The write guard named one pane and nothing containing it, so a client
refused `kill_pane` reached the same outcome with `kill_window` one level up --
and was told nothing, because the answer travelled through the pane that had
just gone. Verified by running the server inside a pane of the server it
drives: `kill_window`, `kill_session`, and `kill_server` all went through and
none of them answered. The Python server of the same name refuses all four.

A subscriber is told about what happened while nothing was watching. A control
connection ends whenever the set of sessions changes, and the whole set is
rebuilt from scratch; in between, tmux reports a pane's output to nobody and
keeps no record to catch up from, so a write in that window was never mentioned
again and a subscriber sat silent while the pane it watched filled. Somebody
else creating a session anywhere on the server was enough to open the window,
and it was a second wide. It is now a hundredth of that, and every subscription
is reported once as soon as the connections are back, because anything may have
changed with nobody to say so.

A `run_command` that times out leaves nothing of its own in the pane. The
command is still running when the call returns, and the directory it records
itself in was removed at that point, so minutes later the wrapper reached its
own bookkeeping and the shell printed four lines naming this package's
temporary paths into somebody's terminal -- long after the call that caused
them, and read as command output by whatever ran next. The wrapper's own writes
now discard their errors. Redirecting one of them is not enough: a shell
applies redirections left to right and has already failed on the first by the
time it reads the second, so each is a brace group with its stderr discarded.
The command keeps its own stderr, which is the output being collected.

A missing pane or window names the listing that would have found one, from
every tool rather than most of them. `kill_pane`, `kill_window`, `move_pane`,
`move_window`, `select_pane`, and `select_window` reached tmux directly and
returned its `snapshot object not found: pane "%9"`, which names the mechanism
and leaves the way out to be guessed at; a model reading that has no reason to
prefer listing over trying another id. Each was a correct-looking call to the
tmux module, which is why nothing said so.

`show_option`, `set_option`, and `show_hooks` refuse a `windowId` at pane
scope, which is what they already did with every other mismatched pair. Pane
scope was the one that let one through: tmux walks pane, then window, then
session, then server from the pane it is handed, so a caller who named a window
was answered about the active pane instead, and nothing in the reply said the
argument had been thrown away. Setting one that way wrote to the pane. Pane is
also the default scope, so naming only a `windowId` hit this.

A resource read on a socket with no tmux server says so, in the same sentence
the listings use. Five of the seven reads answered with tmux's own "command
failed: display-message exited 1: error connecting to ...", which names an
internal command and a socket file rather than the state a caller can act on.
That is what an MCP client whose servers get a curated environment sees on
every read, because the server loses `TMUX_TMPDIR` and looks at the default
socket.

A resource read that names nothing answers with the protocol's own
`-32002 Resource not found` rather than code 0, and keeps the message that
says what to call instead. A client had no way to tell a URI naming nothing
from a server that broke, because a handler's plain error reaches the wire
as code 0.

A pane read through a resource says "no pane %9 on this tmux server;
list_panes reports the panes that exist", which is what the equivalent window
and session reads already said. Two of the six reads went straight to tmux and
answered with tmux's own "snapshot object not found", which names the mechanism
and leaves the way out to be guessed.

Both pane templates say that an id is written without its sigil, as both window
templates already did. Every tool hands a pane back as `%1`, the templates were
the only place a client learns to write it as `1`, and the two that are asked
for most often were the two that did not say.

`list_panes`, `list_windows`, and `list_sessions` say when there is no tmux
server on the socket at all, in a `serverNote` present only then. They answer
rather than failing, which is right — asking what is there is the ordinary
opening move — but tmux exits when its last pane goes, so a listing of nothing
is never a quiet server. A client reading an empty list as an idle machine goes
on to look for a pane that was never going to be there, and that is what an MCP
client which starts its servers with a curated environment produces every time:
the server loses `TMUX_TMPDIR`, looks at the default socket, and reports the
machine as empty. `get_server_info` knew all along and nothing else asked it.

`get_server_info` reports `attachedClients` as an empty array rather than null
when the server is not running. Everywhere else it is an array, and a client
that iterates it should not have to find out that one path is different.

A call inside a batch is checked against the same schema as a call on its own.
The SDK validates only what it dispatches itself, and a batch dispatches its
own calls, so `call_readonly_tools_batch` and the two beside it were the one
way into a handler with no schema in front of it: an argument outside a closed
set, or of the wrong type entirely, reached the handler and was refused or
tolerated by whatever that handler happened to do. Arguments were already
decoded strictly there, which caught a misspelled field; it could not catch a
wrong value in a field spelled right.

Every argument whose set of values is closed now publishes that set as a JSON
Schema `enum`: `scope` on `show_option`, `set_option`, and `show_hooks`,
`direction` on `split_window`, `move_pane`, and `find_pane_by_position`,
`detail` on `list_panes`, and `name` on `get_recipe`. The sets were closed in
the handlers all along and stated in prose in the descriptions, which put the
whole burden on the model: nothing validated a value before the call, and a
word read out of a sentence is a word that can be got wrong. The empty string
is a member wherever a tool documents one as its default. The Python server of
the same name has had these as `Literal` types since it was written.

This narrows one thing. The resolvers behind `scope` and `direction` lowercase
and trim what they are given, so `SERVER` and `RIGHT` were taken; the schema
publishes the canonical spellings and nothing else, and a case-variant is now
refused before the call runs. That tolerance was never documented or tested,
and a client sending `RIGHT` was reading the description rather than the
schema.

A session and a window can be read as resources of their own, at
`tmux://sessions/{session}` and `tmux://windows/{window}`. The hierarchy
offered the list at the top and the pane at the bottom and nothing in between,
so a client browsing it could reach a pane's detail but not the window holding
it. The Python server of the same name has carried both for a while.

Two window templates said an id was written "such as @1". That is the one
spelling a read cannot take: a template is matched by a regexp built from it,
so a raw sigil is answered by the SDK before this package sees it. They now
name the form that works, and a test reads every advertised template rather
than trusting the description.

The socket path and the tmux executable can be given in the environment, as
`LIBTMUX_SOCKET_PATH` and `LIBTMUX_TMUX_BIN`, which is what the Python server
of the same name calls them. Both were flags and nothing else, and a client
starts this server with an environment rather than an argument vector -- so a
socket outside the directory tmux keeps its own in was unreachable from a
client configuration. Naming that socket in `LIBTMUX_SOCKET`, which takes a
name, joins it to that directory and addresses nothing. A flag still wins over
either variable.

`run_command` finds the end of a redrawn prompt line even when no whole copy of
it reached the grid. It looked for the line it had typed and, not finding one,
returned every row it had read -- the prompt, its fragments, and the PREVIOUS
command's output, all as this command's. Two grids produce that: one where the
first draw is gone and the second's opening row was overwritten, and one where
the opening row is there but corrupted, carrying a doubled character the shell
never wrote. What survives both is the line's tail, so the tail is what the
reply now starts after, whether or not a whole copy was found.

`run_command` drops the whole of a redrawn prompt line, not just a complete one.
An interactive shell draws the line it read and redraws it beneath the prompt,
and that second draw can be cut short: its start overwritten, the prompt row
left without its marker, and only the tail of the path surviving as a row of its
own. Stopping at the last complete draw returned that wreckage as the command's
first output line -- a prompt fragment and an orphaned path tail, which a caller
has no way to tell from output. Any later row that is wholly a tail of that
line is now taken with it -- wholly, because the wrapper's file is always named
the same, so a row that merely ends the way the echo ends is something the
command printed.

Neither this nor the cleared-screen fix above is covered by a test, and the
version matrix says nothing about either. The pane's width decides how many rows
the prompt occupies, which decides whether the marks cancel and where a redraw
breaks, and the same width passes and fails across runs -- so no fixed width
reaches them. Reproducing the redraw at all needs a shell that redraws its
prompt rather than the plain one a harness can pin, which is a developer's
environment and not something to build into a test. What holds this path is a
hand sweep across widths with the pane captured beside every reply.

`run_command` no longer answers with nothing when the command cleared the
screen. Its marks are `history_size` plus `cursor_y` added together, a sum that
holds steady while a pane only scrolls, because a row leaving the screen for the
scrollback adds one to the first and takes one from the second. Clearing the
screen moves rows the same way without the cursor advancing, so the sum came
back identical and the reply said the command printed nothing while its output
sat on the screen. Whether it happened depended on the pane's width, which
decides how many rows the prompt occupies and so whether the two changes cancel
exactly: at 41 columns against a two-row prompt they did, at 38 and 44 they did
not. Any shift of rows between screen and scrollback that leaves the cursor
where it was is now recognised, in both directions. The reply reports lines
missed only when the shift destroyed scrollback rather than moving it, because a
screen clear puts what was displayed into the history, where this still reads
it.

`wait_for_text` says why it waited out its deadline when `sinceEntry` ignored a
match that was already on the pane. The reply carried both halves already --
`matchedAtEntry` true beside a timeout -- and left the caller to reason from
the pair to the cause, which is the one shape here that reads as a hang rather
than as an answer. It now carries `entryNote` saying the text was there before
the wait began and that the same call without `sinceEntry` returns at once. The
note appears only on that pairing.

Typing into a pane whose program has exited is refused rather than reported as
sent. `send_keys` answered with the keys it had sent and `send_keys_batch` with
a count, for keystrokes delivered to a pane with no process to read them, and
an agent reading that waits for output which cannot come. `run_command` had
always refused and named `respawn_pane`; the check lived inside it rather than
on the path its neighbours share, so the other four never looked.

Delivery tools now obtain their pane through a resolver that asks the whole
question -- in a mode, or no process -- so a tool added later is guarded by the
way it finds its target rather than by remembering. Tools that change a pane
without typing into it are unaffected, and deliberately: a dead pane is a
reasonable thing to clear, to respawn, and to enter a mode on, because a person
attached to that session scrolls a corpse by hand and no capture does it for
them. `paste_text` against a dead pane also stops surfacing tmux's
"paste-buffer exited 1", which named a command the caller never invoked.

`run_command` no longer hands back its own sourcing line as output. The rows
its marks pick out can begin above the command's own output whenever the grid
moved under them -- reading from the top of the grid to recover an erase is one
way, and marks that no longer locate what they measured is another -- and what
sits above is the prompt and the line that sourced the wrapper. The whole reason
to prefer this over `send_keys` and a capture is that the shell's echo cannot be
mistaken for the result, and a contaminated reply is worse than an empty one:
silence is obviously wrong and gets retried, a plausible first line gets
believed. An interactive shell draws that line twice, once plainly and once
redrawn beneath the prompt, so what the reply starts after is the last of
them.

`run_command` no longer answers with nothing when the command erased the
scrollback. It locates output by a mark taken on either side, each of them
`history_size` plus `cursor_y` added into one number. An erase drops a line of
history while the cursor moves down one, so the two changes cancel and the sum
is identical across a command that wiped the grid -- which read as a command
that printed nothing. Clearing the screen first is what lines them up, so
`clear` twice, or a screen-clearing program followed by anything that erases,
was enough. The count is now compared on its own, and only at an unchanged pane
size: tmux rewraps the scrollback when a pane's width changes and moves rows
between screen and history when its height does, so the count falls on a resize
with nothing erased. This stops a resize being read as an erase; it does not
make the row arithmetic reliable across one.

`list_panes`, `list_windows` and `list_sessions` report `skipped`, the count
their criteria left out. Each already reported `total`, which counts what the
server held before the criteria ran, so a filtered call answered with a short
list under a larger number and nothing to reconcile them. Every tool here that
returns pane text does shorten its reply and says so, which left "the reply was
truncated, ask again for the rest" as the available and wrong reading of a
filtered listing. `list_servers` has always carried the field; these now match
it.

`respawn_pane` says what a command that exits costs. Keeping the pane and its
place in the layout holds while the command runs; a command that exits leaves
tmux nothing to keep, so the pane goes and its window with it. Setting
`remain-on-exit` on the window first holds it open as a dead pane, which is
where `list_panes` reports the exit status. The behaviour is tmux's and
unchanged — the description no longer promises the half of it that is not true.

A write to the pane this server runs in is refused when the client cannot be
asked about it, rather than allowed. The guard asks the person first, and
letting the write through when there was nobody to ask made it advisory in the
clients least able to warn anyone — which is where it matters: a session
identifying its own server ran a command against the caller pane and put the
text in its user's prompt box. Every other pane is unaffected, and the refusal
names the ways on: another pane, a new one, or the listing that says which is
which.

`show_environment` lists names without values. An environment is where people
keep credentials, and a no-argument call returned every value — on the machine
this was found on, eleven live API tokens, straight into a model's context and
unrecallable. Naming a variable still returns its value, and each entry keeps
its scope, which is the one thing a name cannot be reasoned back to. Several
values at once are one `call_readonly_tools_batch`. The listing also takes
`maxLines` and `maxBytes` like every other reply here.

`show_hooks` and `get_server_info` return an empty array rather than omitting
the key. A scope with no hooks answered `{"scope":"server"}` and nothing else,
so a consumer had to test for a missing key instead of iterating an empty list;
the same applied to the attached clients. The text fields elsewhere keep their
absence, because `run_command` uses a missing `output` to distinguish a command
that printed nothing from one whose output could not be read.

`resize_pane` reports the pane it resized. `paneId` is optional and resolves the
active pane, so a caller that left it out was told a width and a height with
nothing saying whose.

`joinWrapped` says what it does and does not do, and points at the better road.
In a narrow pane with a shell prompt of several rows it can join the prompt's
last row to the command typed after it and orphan that command's wrapped tail —
which reads as a defect here and is tmux's: it is tmux that decides which rows
were wrapped, and `capture-pane -J` produces the identical output. For output of
a command the caller ran, `run_command` collects between two marks and never
reads rows, so wrapping does not arise; `joinWrapped` is for a pane the caller
did not author.

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
