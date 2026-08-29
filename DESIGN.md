# Go module design

This document defines the architecture of the Go module.

## Compatibility contract

- The module path is `github.com/libtmux/libtmux-go`; its root package
  name is `tmux`.
- The language baseline is Go 1.26, and tracks upstream's support window rather
  than a version chosen once: Go supports a release until two newer ones exist,
  so the floor is the oldest release still receiving fixes. Raising it is
  routine, and the syntax it unlocks is taken rather than left on the table. No
  runtime dependency is accepted without a measured need and a focused bakeoff;
  the implementation is standard library only.
- The core tmux floor remains 3.2a. Format fields and command flags retain the
  same version gates as the Python library. The MCP consumer has a 3.6 floor:
  its retained control clients require `no-detach-on-destroy` to stay bound to
  the same daemon after their startup session is destroyed.
- Operations that may wait for or execute tmux take `context.Context` first.
  Contexts are never stored in objects.
- Ordinary APIs block. Callers decide whether to start goroutines.
- Public values are concrete and typed. Untyped maps are limited to explicit
  edge decoders and never form the object API. Generator specifications carry
  the Go types and public names used by generated APIs; documentation is not a
  substitute for compiler-visible type information.
- Matching a snapshot never executes tmux.
- Server handles and derived values are safe for concurrent method calls and
  concurrent reads. Optional warning handlers are invoked concurrently and must
  provide their own synchronization.

The nested module uses its own semantic versions and `golang/vX.Y.Z` tags. A
future v2 also adds `/v2` to the module path. The Python release workflow must
accept only root `vX.Y.Z` tags before the first Go tag is published.

## Where each package sits

Placement follows the imports rather than taste, and every rule below was
measured rather than assumed.

The library lives in `tmux/` rather than at the repository root, so the last
element of its import path is the name of the package it names. Go permits a
mismatch and warns that it surprises readers; the large analogues resolve it the
same way, with `go-github` putting package `github` in `github/` and
`client_golang` putting package `prometheus` in `prometheus/`. The import needs
no alias either way — the package clause supplies the identifier — so the reason
is the convention, not a compiler requirement.

`tmuxtest` sits beneath `tmux` because it imports it, which is where the
standard library puts `httptest` relative to `http`. `tmuxq` stays a sibling
because it imports nothing here and nothing here imports it. `internal` sits
beneath `tmux` because nothing outside `tmux` uses any of it, and putting it
there turns that from a habit into a rule the compiler enforces: an internal
package is importable only from beneath the parent of its `internal` directory,
which the consumer modules are not. A root `internal/` would be visible to them,
and is the right home for anything genuinely shared — there is nothing today.

`examples/` is a module of its own. Its examples import nothing outside this
repository now, and a module boundary is what keeps that true: an example
reaching for a dependency cannot put it in the tmux module's `go.mod`, which is
the module whose claim is that it has none.

The parity manifest lives in `tmux/internal/parity` because `//go:embed` cannot
reach outside the directory of the package that embeds it.

## Package shape

| Package | Responsibility |
| --- | --- |
| `tmux` | Server, object hierarchy, snapshots, commands, formats, options, hooks, environment, clients, filters, and model-specific errors |
| `tmuxq` | Model-free generic helpers for slices and `iter.Seq` values |
| `tmuxtest` | Real-tmux server lifecycle, control-client fixture, and environment helpers |
| `internal/tmuxcmd` | Subprocess transport, argv construction, and output splitting |
| `internal/generate` | Checked-in model, option, hook, format, and filter generation |

`tmuxq` imports no model package and owns its cardinality sentinels. Generated
filters live in `tmux`, so relations and predicates create no import cycle.
`tmuxtest` may import `tmux`, but `tmux` never imports `tmuxtest`. Real-tmux API
tests use `package tmux_test`; package-private tests stay process-free or use a
model-free internal harness.

`tmux` is large because tmux's own surface is, and it stays one package because
its parts cannot be separated without cycles. `Session` returns `Window`,
`Window` returns `Pane`, every model returns itself from an option read, and
generated filters name all four. Splitting along those types would require a
shared package holding most of the model anyway, or interfaces standing in for
concrete values the compatibility contract keeps concrete. Navigability is
therefore a naming and view-type problem rather than a packaging one, which is
why format values live behind `Formats` instead of on each receiver.

A dependency analysis over the root package -- asking each used object where it
was declared -- found `control`, `filter` and `search` reachable in one direction
only, and `plan` reachable once one shared argument builder moves. They stay
anyway. Extracting `control` would strand `Server.OpenControl` and
`Server.OpenControlPool`, whose discoverability from `server.` is the point of
the mode table; extracting `search` would strand the eight methods that are its
API; and `filter` is a cycle in practice, because the search signatures name
filter types while the filter predicates name the model. The finding is recorded
so the question does not have to be reopened from scratch.

`Plan` follows that rule for a reason of its own. A recorded operation and the
method that runs the same command share one argv builder, which is what stops a
flag meaning one thing when it runs and another when it is planned. Those
builders are unexported, so an `ops` package would have to export them, or take
the request types with it into a package `tmux` then imports back. Recorded
operations therefore live beside the model in files named for their receiver,
the way `pane_input.go` and `window_layout.go` already are.

## Public object model

`Server` is an immutable configured handle over shared private connection
state. `Session`, `Window`, `Pane`, and `Client` are concrete values containing
typed snapshot state plus that handle for follow-up operations. Snapshot fields
never refresh implicitly; live relationships are context-first methods.

Relationship accessors on a record report whether the record carries relations
at all: `Session.Windows`, `Session.Panes`, `Window.Panes`, and
`Window.LinkedSessions` are comma-ok, matching `Window.Session` and
`Pane.Window`, which always were.

The rejected alternative was returning an empty slice, which is what a record
from a targeted lookup used to answer. It cannot be right: tmux destroys a
window when its last pane closes and a session when its last window closes, so
a materialized window with no panes does not exist, and `Server.Snapshot` reads
the whole server rather than a scope, so an empty relation is never a truthful
answer. A caller ranging over one got a loop that ran zero times with nothing
to say why -- and `Refresh`, which reads as "make this current", returned a
record whose panes had silently gone from two to none.

Which kinds a snapshot listed is recorded on it rather than inferred, because a
point lookup builds a real snapshot holding its own row: a window looked up by
ID has a snapshot with no pane index, which is indistinguishable from a window
whose panes were listed and found to be none. `Server.Snapshot` lists
everything; a point lookup and a search list one kind; the pane-from-environment
lookup projects one row into three.

A generated filter naming a relation does not match a record that cannot answer
it, which is what the to-one branch always did with its found result.

Holding one record keeps its whole snapshot reachable, because that graph is
what the record navigates. `Refresh` returns a record materialized on its own,
which is the way to keep an identity without the graph, and is why its relations
report false.

Blocking list and refresh methods materialize a new `Snapshot`. A snapshot
contains complete indexes for sessions, winlinks, windows, panes, and clients.
It emits one window view per winlink. Linked windows can therefore share a
`WindowID` while retaining distinct `SessionID`, index, and active state. Pane
views follow those winlinks, matching the Python library's observable
resolution behavior.

Stable tmux identifiers use distinct string types: `SessionID`, `WindowID`,
`PaneID`, and `ClientName`. Materialized identity state is private and exposed
through non-stuttering read-only methods: `Session.ID`, `Window.ID`,
`Window.Index`, `Pane.ID`, `Pane.Index`, and `Client.Name`. Parent identities
retain their tmux vocabulary where needed, such as `Pane.WindowID`. Callers
obtain materialized values from server queries and lifecycle operations rather
than constructing partially initialized public structs.

Every format token is stored as its exact raw expansion and exposed through
`Raw(name) (string, bool)` on materialized models. The format specification
also assigns a closed value kind and the generator emits typed comma-ok
accessors. Examples include `Pane.Active() (bool, bool)`,
`Pane.Width() (int, bool)`, and `Client.Created() (time.Time, bool)`. Boolean
formats accept only tmux's `0` and `1`; integer and Unix-time formats use
strict decimal parsing. The typed accessor reports `ok == false` for an absent,
empty, or malformed expansion, while `Raw` preserves the distinction between
absence and a materialized empty value. Ambiguous composite formats remain
strings. Stable identifiers and tmux versions use their existing nominal Go
types.

An accessor drops its receiver's tmux prefix: `session_name` becomes
`Session.Name`, `window_name` becomes `Window.Name`, and
`pane_current_command` becomes `Pane.CurrentCommand`. Cross-scope projections
retain their scope prefix. The remaining words are capitalized by
`internal/goname`, the module's single implementation of the
`lower_snake_case`-to-Go convention: known initialisms take their fully
capitalized form and unseparated compounds are split, so `pane_id` becomes
`Pane.ID`, `client_utf8` becomes `Client.UTF8`, and `client_readonly` becomes
`Client.ReadOnly`. Generated accessors and the parity omission guard both call
it, so a name the generator produces and a name the guard reserves cannot
diverge. The format specification records explicit names for
semantic collisions, including count formats that would otherwise collide
with hierarchy traversal. Generated comments begin with the Go identifier and
include the exact `#{tmux_format}` spelling, scope, and version floor. No
optional format value is represented by a shared pointer. Rows retain
field-availability and producing-version metadata.
Session, window, and client queries preserve Python's cross-scope active or
attached hierarchy projection. This consolidates the useful typed state from
`neo.py` into the canonical objects instead of creating a second object model.

Snapshots are immutable shared references assembled from one or more tmux
commands; they are observationally consistent, not atomic transactions.
Each build probes the connected server's PID, start time, socket, and version
before and after its listings and checks that identity on every nonempty row.
A server restart during collection is an invariant error, while ordinary
dangling relationships remain observable.
Each materialized record keeps that four-part daemon provenance. Its follow-up
commands wrap the requested operation in one tmux-side `if-shell`, comparing
the current PID, start time, and socket before execution. A replacement daemon
therefore rejects a stale record atomically instead of receiving its command
after a separate liveness probe. Record equality and record-derived plan refs
include the provenance; raw ID refs remain relative to the server selected at
execution. A plan containing refs from different daemons is invalid before it
sends anything.
Snapshot slice accessors return fresh slices, and iterators range over
materialized state only. Duplicate point lookup reports ambiguity. Live point
lookups (`Server.Session`, `Server.Window`, `Server.Pane`, and `Server.Client`)
and `Refresh` issue targeted tmux queries so tmux chooses the canonical winlink.
Live relationship resolvers materialize current state, retain explicit winlink
context, and report missing or ambiguous exact views. `LinkedSessions`
deduplicates session holders while preserving disappearance and error behavior.

## Operation naming

A method that wraps a tmux command drops the receiver's noun from its Go name.
`kill-pane` becomes `Pane.Kill`, `rename-session` becomes `Session.Rename`,
`link-window` becomes `Window.Link`, and `start-server` becomes `Server.Start`.
The receiver already carries the noun at every call site, so repeating it in
the method name adds no information.

Naming a new wrapper takes three questions, in order.

First, does the tmux command's name contain the receiver's noun? If it does
not, nothing repeats: spell the command through `internal/goname` and stop.
`Server.RefreshClient` wraps `refresh-client` and `Session.NewWindow` wraps
`new-window`; neither repeats its own receiver.

Second, does the command create the object that noun names? A creation command
is named for its product, and a product that coincides with the receiver is not
a repetition of it. `Pane.NewPane` and `Window.NewWindow` keep the noun for the
reason `Window.NewPane` and `Session.NewWindow` carry it: the noun says what the
call returns, and `Pane.New` would not.

Third, is the noun a plural of the receiver's noun? Then the command ranges over
every such object rather than over the receiver. `Pane.DisplayPanes` wraps
`display-panes`, which numbers panes for tmux's current client and takes no pane
target at all, so `Pane.Display` would promise the receiver. A final word that
merely ends in `s` is not a plural of the receiver: `server-access` names one
feature, not a set of servers.

Otherwise drop the noun. What remains is the verb, and a word the Go signature
carries better than the tmux name does replaces that noun rather than joins it:
`split-window` on a window is `Window.SplitPane`, which names the pane it
returns. A method that wraps one flag of a general command is named for that
flag's effect rather than for the command's verb, as `Pane.SetTitle` is for
`select-pane -T` and `Pane.KillOthers` for `kill-pane -a`.

`TestGoSurfaceDropsReceiverNounFromOperationNames` enforces the rule over every
exported handwritten method of `Server`, `Session`, `Window`, `Pane`, and
`Client`, so a new command wrapper cannot adopt a repeated noun without a
recorded decision. Generated option, hook, and format members are outside the
rule: their names come from the tmux option and format specifications, where
`window-status-style` stays distinguishable from the session-scope
`status-style`.

Three shipped operations keep a noun this procedure drops. The list is closed,
each entry records why the compressed spelling is worse than the repetition, and
the guard rejects an entry that stops naming an operation the rule convicts, so
the map cannot outlive the names it excuses.

| Method | tmux command | Why the noun stays |
| --- | --- | --- |
| `Pane.BreakPane` | `break-pane` | The bare verb loses the object of the break. `Break` reads as ending something rather than as moving the pane into a window of its own, and `break-pane` is the spelling in tmux's manual and in its default `!` binding. |
| `Server.LockServer` | `lock-server` | A bare `Lock` would sit beside `Server.LockClient` and lose the scope that distinguishes them: locking every attached client, not one. `Lock` is also Go's established name for acquiring a mutex, and `Server` is documented as safe for concurrent method calls. |
| `Server.ServerAccess` | `server-access` | The receiver's noun is not the object of a verb here. `server-access` names tmux's access-control feature, so `Server.Access` would name no operation while reading like an accessor. |

Each entry is a lexical accident rather than a category: a verb that cannot
stand alone, a collision with an established Go name, and a command name that is
not verb-plus-object. A command that hits one of these joins the table with its
reason; the procedure itself does not acquire a clause.

Part of this surface is guarded twice. The parity omission guard converts each
omitted Python name with `internal/goname` and reserves that spelling, which
already rejects `Pane.ResizePane`, `Pane.SelectPane`, `Server.KillServer`,
`Session.AttachSession`, `Session.KillSession`, `Window.KillWindow`,
`Window.SelectWindow`, and `Window.SplitWindow`. That coverage is a side effect
of Python's deprecations rather than a policy, so it says nothing about a
command with no Python history, such as tmux 3.7's `new-pane`. The receiver-noun
guard is what covers those.

## Operation parameters

An operation takes one of four parameter shapes, chosen by what the tmux
command requires rather than by how many arguments it happens to have.

| Shape | Count | Used when |
| --- | --- | --- |
| `(ctx)` | 57 | The receiver already names everything the command needs |
| `(ctx, values...)` | 70 | Every value is required, and there are few enough to read positionally |
| `(ctx, request)` | 65 | Any field is optional, so a zero field means "let tmux choose" |
| `(ctx, values..., options)` | 25 | Some values are required and the rest are optional flags |

`Window.Kill` needs nothing beyond its receiver. `Session.Rename` needs exactly
one required name. `Pane.Capture` takes a request because almost every capture
field is optional. `Session.SetOption` keeps the option name and value
positional, because omitting either is meaningless, and groups the flags that
modify the write into `SetOptionOptions`.

The fourth shape is why a caller writes `SetOptionOptions{}` at a call site
that sets no flags. The alternative, folding name and value into the request,
would let a caller omit them and turn a compile error into a runtime one. The
zero value of every options and request type is documented as the behavior a
caller gets by leaving it empty, so the empty literal is a statement that no
flag applies rather than a placeholder.

## Operation result shape

A wrapper that changes tmux state either hands back a freshly materialized
model or returns only `error`. Records never refresh in place, so this choice
decides whether the caller's next line can keep using the record it already
holds.

A wrapper returns a model when the tmux command changes the record's
**identity**, **placement**, **extent**, **selection**, or **process**.
Identity is a name, a pane title, and the fact of existing. Placement is the
session and index the view lives at. Extent is the record's own width and
height, not its children's. Selection is which sibling is current or active.
Process is what `respawn-window` and `respawn-pane` replace. Every other change
returns `error`.

The facets are not a list of interesting fields; they are the fields that
decide which tmux object the caller is holding and what it looks like. A caller
who changes one of them almost always wants to read it back, so the wrapper
fetches it once instead of making every caller write the same follow-up query.

That convenience is not free, and the rule is a judgement rather than a
necessity. tmux prints nothing on a successful mutation, so a returned model is
always an extra materialization: measured against tmux 3.7b, a point lookup
costs three tmux invocations, `Window.SelectLayout` adds one more, while
`Window.Rename` adds four and `Pane.Select` adds seven. A caller who wanted the
fresh record would pay the same to call `Refresh`, and a caller who did not
pays anyway. Nothing in tmux forces the choice either way: a stale view still
resolves, because tmux matches a `%pane` or `@window` identifier ahead of the
session and index that precede it.

Everything else returns `error`, including the materialized fields that
describe what a record is *doing*: `pane_in_mode` and `pane_mode`, `pane_pipe`
and `pane_pipe_pid`, `history_size`, `window_layout`, `window_linked`,
`session_attached`. Each of those moves without the library when a client
detaches, the terminal user leaves copy mode, or the piped child exits, so a
record refreshed for one of them reports a value the caller does not own. This
is why `Session.Attach`, `Pane.CopyMode`, and `Pane.Pipe` return `error`
despite changing fields their receivers materialize, and why
`Window.SelectLayout` does while `Window.Rotate` does not: rotation reassigns
the active pane and every `pane_index`, and layout selection changes only the
panes' geometry.

Three consequences follow from the rule rather than extending it. Destruction
returns `error` because no record is left to materialize; `Kill`, `KillOthers`,
`KillWith`, `KillWindow`, and `Unlink` document that their receiver is no
longer live. Creation returns the created record only when tmux reports its
identity, which is why `new-session`, `new-window`, `split-window`, `new-pane`,
and `break-pane` carry `-P -F` and return their product while `Window.Link`,
whose `link-window` prints nothing, returns `error`. And the model that comes
back is the one the operation redefines rather than always the receiver:
creations return the new record, `Window.Swap` and `Pane.Swap` return both
endpoints in a result value, and a parent-scoped selection returns the child it
selected, so `Session.SelectWindow` yields a `Window` and `Window.SelectPane` a
`Pane`.

Options, hooks, environment variables, buffers, key bindings, and prompt
history return `error` for the same reason as a group: tmux stores them outside
the four records, and they are read back through `Options`, `Hooks`,
`ShowEnvironment`, `ListBuffers`, `RawOption`, and `RawHook`.

Naming a new wrapper's result takes one question. After this command returns,
is the caller's record wrong about which object it is, where it lives, how big
it is, whether it is the selected one, or what process it runs? Return a model
if it is, and `error` if it is not.

## Command boundary

Every dispatched tmux command crosses one unexported executor method. Direct
control-mode processes retain their documented lifecycle. The request value and
argv builders remain private; `CommandResult` is public and contains argv,
decoded stdout lines, exact stdout bytes, decoded stderr lines, and exit status.

`Server.Cmd(ctx, args...)` preserves raw tmux behavior:

- a nonzero tmux exit is result data, not a Go transport error;
- cancellation, deadline expiry, and process-start failures are Go errors;
- no command is executed through a shell;
- decoded `Stdout` preserves the line-oriented compatibility API, while
  `RawStdout` preserves tmux's emitted bytes including delimiters and trailing
  newlines;
- a failed high-level operation becomes a `CommandError`.

## Unsupported capabilities

A request naming a flag the running tmux does not have is refused, with a
`VersionTooLowError` carrying the subcommand and the capability alongside both
versions. `ServerOptions.Unsupported` chooses the other behavior, which omits
the flag, runs the reduced command, and reports it to
`ServerOptions.WarningHandler`.

Refusing is the default because dropping a flag changes what the command does
and says nothing about it. A split asked to leave a pane empty starts a shell
in it; on tmux 3.6 raw `split-window -E` creates no pane at all, so degrading
delivers a running process the caller asked not to have. `run-shell` asked for
arguments runs without them, and `kill-session` asked for a session group takes
one session instead of the group. Each returned success.

Python signals the same decision with `warnings.warn`, which prints to stderr
and is therefore seen. Go's nearest equivalent is a handler that is nil until a
caller installs one, so translating the behavior directly turned a visible
degradation into a silent one. The switch keeps the behavior available and
makes choosing it deliberate.

A `Plan` applies the policy of the server it runs on, so a recorded step
refuses exactly what the same request refuses when issued directly.
`Plan.Preview` renders without a server and so refuses, which is what a preview
that exists to catch what tmux would reject is for.

`ServerOptions.WarningHandler` receives concrete warning values. The default
handler is silent. A configured `log/slog` logger may receive structured
diagnostics, but library code never installs handlers or logs environment
values.

`CommandError` implements `error` and is discoverable with `errors.As`.
Library-created high-level errors retain an owned copy of the completed
`CommandResult`, including argv, decoded stdout, exact stdout bytes, and stderr.
Its `Error` text includes stderr so ordinary operation failures preserve tmux's
diagnostic without a second raw command. Operations whose primary payload may
contain secrets opt into a redacted error that retains only the exit code. This
applies to buffer contents, environment values and output, command-bearing
creation and interactive requests, server execution and key commands, respawn
commands, pipe commands, popups, and interactive attachment.
Callers must treat generic `CommandError.Result` diagnostics as potentially
sensitive when their own command operands contain secrets.
`VersionQueryError` follows the same exit-only rule, while
`SnapshotDecodeError` identifies the object, record, and field but redacts the
malformed field value.

`Engine` is the seam that decides whether a process happens at all; see
"Engines" below. `ServerOptions.Runner` replaces dispatched process execution
after construction and remains underneath engine fallback. It neither skips
executable resolution nor starts `OpenControl`'s direct process. Pure private
request builders can also seed a future deferred operation graph. An exported
operation hierarchy is deferred until a real downstream consumer fixes its
required identity, batching, and error semantics.

## Engines

An `Engine` is a transport plus the set of request kinds it can carry:

```go
type Engine interface {
	Supports(kind CommandKind) bool
	Run(ctx context.Context, kind CommandKind, request CommandRequest) (CommandResult, error)
}
```

A record materialized before a connection keeps the handle it was made on and
pays for a tmux process per command. Measured: three reads on a stale record
cost nine processes and on the same record using `WithEngine`, none. That
is reported as a `WarningControlPoolUnused`, completing `WarningControlPoolClosed`
which reported the same symptom for a pool that had been closed; covering one
and not the other left the commoner case to be measured rather than told. It
warns rather than refuses because the results are unchanged and only the cost
differs.

Reporting it needs the pool count to be reachable from the handle that is
paying, which is why `serverState` separates the handle's configuration from
the tmux server's coordination. `NewSession` runs on a handle with TMUX removed
from its environment, so a program started inside a pane does not create a
session against the server it is running in, and the session it returns keeps
that handle. Those are different options and so a different `serverState`, but
the same tmux: sharing the coordination also stops each created session
re-probing a version the creating handle already held, which was one tmux
process per session.

`Server.WithEngine(Engine) Server` selects one, as an immutable handle copy.
Selection is a method rather than a
`ServerOptions` field because the control-mode engine cannot exist before its
server does: `OpenControl` needs a materialized session to attach to.
`ControlClient.Engine()` adapts a live client, and `Server.SubprocessEngine()`
returns the process transport as a value so a derived handle can go back.

`CommandKind` is a closed enum with two members. `CommandServer` is a command
addressed to the running server; its argv carries the tmux command alone,
because a transport that serves this kind is already connected and the
configured `-L`/`-S`/`-f`/`-2` selectors would be a parse error on the wire.
`CommandProcess` needs a tmux process of its own; its argv is complete and its
`Stdio` may stream. Interactive attachment and the `tmux -V` probe are the two
operations that declare it.

The `Server`, not the engine, routes around what an engine cannot carry. An
engine author writes one transport and gets the fallback; the fallback honors
`ServerOptions.Runner`, so a substituted transport intercepts every fallback
request; and every engine gets identical behavior for the kinds it declines.
The cost is that an engine cannot implement a smarter fallback of its own.

Argv is classified, never re-parsed. The library already knows, at each of its
call sites, which selectors it added and what the request needs; deriving that
back out of a flat argv would mean emulating tmux's client-level `getopt`
grammar in a second place.

The library also declares when an operation's own result contract requires a
process, independently of any engine's capabilities. A printed `capture-pane`
and `show-buffer` return tmux's exact stdout bytes through `Pane.CaptureBytes`
and `Server.ShowBufferBytes`, while a control frame carries tmux's control
rendering of a reply, which this package does not normalize. Those reads drop
to the process transport on any handle. A capture into a tmux buffer prints
nothing and keeps using the engine.

Errors cross the seam unchanged. An engine reports a completed tmux command
failure as a nonzero `ExitCode` with tmux's message in `Stderr`, which is what
the same failure looks like through a process, so `CommandError`, its
missing-target classification, and list leniency behave identically through
either transport. Only transport failures are Go errors.

An engine does not own its own shutdown. A `Server` is an immutable handle that
callers copy, so it cannot be the value that closes a transport; whoever created
the transport closes it, and `ControlClient.Close` stops the process behind
`ControlClient.Engine`. `Engine` therefore has no `Close`.

Engines are values from constructors, not a name-keyed registry. A registry
would be a mutable package-level map behind a mutex that turns an unknown engine
into a runtime error, retains every engine in every binary, and still could not
construct the control engine, whose inputs are a context, a server, and a
session rather than keyword arguments. A configuration edge that selects a
transport by string writes a switch. Concrete engines implement `fmt.Stringer`
for diagnostics; `Engine` does not require it.

`Engine.Run` is one blocking call per command, so concurrency is the caller's
goroutines, which is also why there is no async engine type: `ctx` plus
goroutines is Go's async story. A pipelined engine that keeps several commands
in flight and matches replies by the command number `ControlCommandResult.Number`
already carries is a change inside `ControlClient` and needs no interface
change. Batching several requests in one call is the one extension the
interface does not express; an engine that wants it can declare an optional
interface and a caller can discover it with a type assertion, the way
`io.WriterTo` is discovered. Nothing in the object API produces a batch today,
so that interface is not declared.

An engine costs a caller who wants none of it nothing: `Engine` is nil on every
handle returned by a successful `NewServer`, and routing adds one nil check
before the same request the runner always received.

## Core API signatures

`NewServer(ServerOptions) (Server, error)` validates and freezes one immutable
binding without starting tmux. It snapshots the effective environment and
working directory, resolves one absolute executable with them, and shares that
path and environment across process and control transports. It also freezes the
effective socket path; named and default selectors receive a canonical
`TMUX_TMPDIR`, while an inherited `TMUX` path becomes explicit before lifecycle
code scrubs that variable. Invalid options, an unavailable executable, or a
failed working-directory snapshot return an error. The zero `Server` is invalid;
operations return `ErrInvalidServer`. Policy copies share private coordination
and copy only immutable handle fields. Representative signatures are:

```go
func NewServer(options ServerOptions) (Server, error)
func (s Server) Cmd(ctx context.Context, args ...string) (CommandResult, error)
func (s Server) Sessions(ctx context.Context) ([]Session, error)
func (s Server) IsAlive(ctx context.Context) (bool, error)
func (f PaneFilter) Predicate() (func(*Pane) bool, error)
```

## List error policy

A list-shaped accessor returns a failure rather than an empty collection. There
is no switch: an empty result means tmux answered and had nothing to report.
Invalid arguments, context errors, decode failures, unsupported schema, and
violated snapshot invariants remain errors of their own.

The rejected alternative was Python's, where a failed accessor yields an empty
result. Its cost is not that a caller is misinformed but that it cannot tell: a
socket path with a typo, a socket this process may not read, and a path that is
not a socket all produce the same empty answer as a server holding nothing. The
pattern that consumes it is ordinary — create the environment when none is
found — and under leniency a wrong socket path makes it build a second
environment beside the one it was pointed away from.

Leniency also could not be narrowed to the safe case. Every lenient branch
caught any nonzero exit or transport failure without classifying it, so
inverting the default would have kept the same undifferentiated catch behind a
switch rather than removing it.

`ErrNoServer` classifies the one failure a caller routinely acts on. A tmux
server holding no sessions exits, so an absent server and an empty one are the
same state, and a program that starts what it does not find needs to recognize
it. It classifies rather than replaces: the error is returned either way, so a
socket that cannot be used is never mistaken for one with nothing on it.

It does not separate an absent server from an unreachable one, because tmux
does not. `client.c` treats `ECONNREFUSED` and `ENOENT` alike, prints a
constant message only for the first, and renders every other errno through
`strerror`, whose text follows the process locale. Matching that text would
make the classification locale-dependent, which is a worse contract than
declining to draw the line. Acting on the sentinel stays safe because creating
what was not found reports tmux's own refusal.

The policy decision a caller does want belongs to the caller. The MCP server
answers "what panes are there" on an unstarted server with none, because a
client orienting itself before starting anything is its ordinary opening move;
it says so in one place rather than inheriting it from the library.

`IsAlive` draws the same line: only `ErrNoServer` reports false without an
error, so a probe cannot report a server as absent when the socket is merely
unusable. `Server.Kill` depends on that — it confirms a kill by observing the
daemon, and a probe that merely failed no longer reads as one that proved
death.


## Filters and queries

Each model has a generated JSON criteria struct. Pointer scalars express
tri-state fields; non-nil empty membership and composition slices are invalid
rather than silent match-nothing filters. `Ptr` returns a pointer to a copied
value for the uncommon case where a meaningful zero must remain distinct from
absence. The filter generator emits value-returning exact-match constructors,
such as `PaneCommandIs`, `PaneIndexIs`, and `WindowNameIs`, so common criteria
do not require pointer temporaries. Fields in one struct combine with AND.
`AnyOf`, `Not`, to-one relations, and `Some`, `Every`, and `None` to-many
relations follow the supplied schema. Empty relations satisfy `Every`.

`Validate` rejects invalid regular expressions, malformed relation nodes,
cycles, excessive recursive composition, invalid stable IDs and indices, and
criteria whose scalar intersections or identical positive and negative nodes
prove they cannot match. Validation does not attempt general regular-expression
intersection or boolean satisfiability. Cycle and depth checks occur before
recursive compilation, including across model relations. `Predicate()
(func(*T) bool, error)` validates and compiles strings and sets once. Compiled
state never enters JSON.

`tmuxq.Where` returns a fresh slice and passes its predicate a pointer to a
shallow scratch copy, never a pointer into the input slice. Top-level predicate
mutations are discarded, but referenced state still aliases the caller; generic
predicates are for inspection and must not mutate or retain the value. The tmux
model values contain immutable private state. `First` uses comma-ok;
`ExactlyOne` returns errors compatible with `ErrNoMatch` and
`ErrMultipleMatches`. Separate helpers accept `iter.Seq[T]`.

The struct is the wire form. The generated `ParseSessionLookup`,
`ParseWindowLookup`, `ParsePaneLookup`, and `ParseClientLookup` edge functions
accept stable JSON field and relation names separated by `__`. Scalar operators
take one string value; `in` and `nin` take one or more. To-many paths lower to
`Some`, or `None` for `nin`; other legacy string operators lower to exact,
contains, membership, negation, or regex fields. Unsupported Python-only
callable or dynamic lookups return `ErrInvalidFilter`. Local closures do not
serialize.

Existing raw tmux filtering remains a separate live API. Server, session, and
window search methods accept a named `TmuxFilter` string and pass it through to
tmux `-f`; buffer listing retains its raw filter. Malformed raw filters preserve
tmux behavior. Typed criteria never masquerade as raw tmux expressions. Their
inspectable shape leaves future typed pushdown open while snapshot filtering
stays purely local.

Generated filter JSON decoders reject unknown and duplicate fields. Missing or
`null` pointer fields remain unset; explicit empty slices remain present and are
validated as impossible where required. New schema fields are additive but are
not silently accepted by older decoders. Schema versions are published outside
the wire object, so the object itself remains the shared wire form.

## Options, hooks, and environment

Raw option, hook, and environment entries retain their exact tmux strings.
Generated typed accessors and setters cover known options with the same Go
value shape. Flags use `bool`, numbers use `int64`, and each tmux choice option
has a distinct generated string type with prefixed constants and `Valid`.
Choice reads preserve unknown future values as invalid named values; typed
setters reject values unavailable to the connected tmux version. Raw setters
remain the escape hatch for unknown names, future values, append behavior, and
`set-option` flags.

Sparse array setters replace the local array. They first establish an explicit
empty local array, then apply caller-owned entries in ascending index order.
The empty sparse value therefore remains present rather than unsetting to an
inherited value. The returned result reports replacement and confirmed applied
indices; a failure stops without rollback. Raw unset remains the operation for
absence and inheritance.

Command aliases, terminal features, and terminal overrides also expose
immutable parsed projections. Malformed subentries return redacted decode
errors alongside the valid partial projection; the library does not log them.

Server, session, window, and pane scopes share private request builders but
expose scope-specific concrete APIs. Session, window, and pane typed setters
are methods on those receivers. `Server.GlobalSessionScope` and
`Server.GlobalWindowScope` return concrete value handles that own the global
option and hook operations; this keeps the scope in the type system and avoids
prefixing a global operation matrix onto `Server`. Server-scope option setters
remain direct server methods. Option and hook setters return `error` under the
rule in "Operation result shape": tmux keeps this state outside the four
records, so a setter has no record to refresh and `Options`, `Hooks`,
`RawOption`, and `RawHook` are the reads.

## Request optionality

Request fields use plain values when the zero value can unambiguously mean
"unset". This includes positive-only dimensions, counts, adjustments, and
optional nominal targets whose zero value is not a valid tmux target. Pointers
remain only where zero or an empty string is meaningful to tmux, and for
recursive filter structure. Requests copy pointer and map inputs before any
version query or subprocess call that could let caller mutation change a
validated request. Contexts are never stored.

## Control-mode client and operation engines

`Session.OpenControl` is the ownership boundary for ordinary object operations.
It opens one or more control-mode command lanes under the materialized
session's daemon predicate and returns a `Connection`. Its `Server` and
`Session` values retain a private pointer to that owner through every derived
record. Terminal connections require tmux 3.6 and set
`no-detach-on-destroy`, so destroying the initial session moves their clients
to another session when one exists without changing the retained session
record. The binding is terminal: close makes later operations return
`ErrControlClosed`, and selecting another engine, retargeting the socket, or
requesting an operation that needs a separate process cannot detach it.
Exact-byte reads and interactive attachment return
`ErrConnectionRequiresProcess`; no fallback is attempted.

The initial `tmux -C` process executes `if-shell -F` as its first command. Only
the matching PID, start time, and socket branch schedules `attach-session`; a
replacement daemon therefore never observes even a transient attached client.
The startup reader consumes the outer predicate and inner attach frames before
calibrating request boundaries. `Connection.CloseContext` always begins
shutdown and uses its context only to bound the join.

`Server.OpenControl` starts an attached `tmux -C` process and returns a
production `ControlClient`. It validates `%begin`/`%end`/`%error` framing,
serializes concurrent commands, correlates each reply by command number, and
buffers ordered notifications in a bounded in-memory queue. On overflow it
keeps draining tmux's stdout, preserves the queued prefix, and then reports a
typed error. Command-only pools ask tmux not to send pane output. `%output`
payloads are decoded to pane IDs and exact bytes.

Command arguments are encoded without a shell. Single-quoted spans preserve
printable bytes, adjacent quoted spans preserve literal single quotes, and
tmux octal syntax carries control and non-ASCII bytes. NUL is rejected because
tmux cannot represent it in an argument.

The startup context bounds process start, attach framing, and registration; it
does not own the returned client's lifetime. A command canceled after its write
returns promptly, but the request loop drains that reply before writing the next
command. Natural exit preserves queued notifications through `io.EOF`.
Reader failures surface after earlier notifications drain. `CloseContext`
rejects unaccepted requests, gives an accepted command a bounded frame-drain
window, and is idempotent and retryable. `Reconnect` creates a new identity and
never replays commands.

`ControlCommandResult.RawStdout` preserves the control frame payload exactly.
That payload is tmux's version-specific control rendering and is not normalized
into the byte-exact pane and buffer semantics of `Pane.CaptureBytes` and
`Server.ShowBufferBytes`.

The `tmuxtest.ControlMode` raw-stream fixture remains available for parser and
transport tests. `ControlClient.Engine` connects the client to the object API.
`ControlClient.Notifications` is `NextNotification` as an `iter.Seq2`: it ends
at `io.EOF` without an error because the end of a stream is not a failure, and
continues past a record it could not parse because a tmux sending a
notification kind this package does not know must not end a watcher.

## Recorded operations

`Plan` records tmux commands as `Op` values and runs them together. It is
independent of the transport: the same plan means the same thing over a tmux
process and over a control connection, and the switch matrix asserts that on
every supported tmux rather than claiming it.

Identity is a `Ref`, addressing either an object that exists or the one a
recorded step will create. A step's ref carries its one-based index, so the
zero `Ref` is not silently a reference to the first step, and a plan holding
one is refused rather than guessed at. This is what lets a build be written in
one pass: a split is recorded and keys sent to its pane before tmux has been
asked for anything.

Batching is a `Planner`, and planners are values rather than names in a
registry, so selecting one is a compiler-checked expression and a caller can
supply their own. `Sequential` sends one command per invocation, `Folding`
groups each run of operations that neither answer nor create. Successful
results are identical through both; only the invocation count differs.

`Plan.Preview` renders without a server, and separates the two reasons an
operation cannot render there. A step naming an object an earlier step has yet
to create is nil, because that ID does not exist until the plan runs. Everything
else is an error naming the step: a plan is not atomic, so an argument tmux
would refuse at the last step is refused after every step before it has changed
something, and a preview reporting both the same way would hide the one that is
a defect.

Result identity is the part tmux does not provide. A command list returns one
exit status and one merged stdout with no boundary between its commands, so an
operation whose result is its output cannot be told apart from its neighbours.
`Op.Chainable` reports that, and rendering re-checks it rather than trusting a
`Planner` to have honoured it: a planner that groups a capturing or creating
operation is refused with a `PlanError`. A single-operation refusal is
`OpFailed`. Every operation in a refused command list is `OpIndeterminate`,
because tmux does not identify which command failed or which earlier commands
ran. Later dispatches are `OpSkipped`. `Plan.Explain` exposes that attribution
boundary before execution.

The consumer modules here stayed on the direct API, which is a finding rather
than a gap. `workspace.Build` wants a materialized record after each step and
already opens a control connection, so its cost is transport rather than
batching; the MCP batch tool dispatches advertised tools whose structured
results are the point, so none of its calls are foldable. Transport and
batching are independent switches, and code needing a record per step declines
this one correctly.

## Real-tmux test contract

Tests use one explicit `-S` socket per test. Every real-tmux test package has a
one-line `TestMain` wrapper that calls `os.Exit(tmuxtest.Main(m))`.
`tmuxtest.Main(*testing.M) int` performs all cleanup and restoration before it
returns the exit code. It gives the suite a short
temporary root and sets both `TMPDIR` and `GOTMPDIR`. Each server uses a private
directory beneath that suite root so its socket and config remain available for
fallback cleanup after a test returns. Socket paths are checked against the
portable 103-byte Unix socket-path ceiling and registered for suite cleanup.

Every tmux child removes inherited `TMUX`, `TMUX_PANE`, and `TMUX_TMPDIR` unless
a test explicitly opts in, and starts with an exact generated minimal `-f`
configuration. Server and control-client startup take a caller-owned context
first and apply an internal ceiling. `t.Cleanup` uses a fresh bounded context,
requests `kill-server`, probes the daemon currently answering on the owned
socket, and verifies that daemon's death before removing the socket. A daemon
that restarted on the same socket is therefore cleaned by its current PID
rather than confused with the PID recorded at startup. Cleanup never signals a
numeric PID because it may be reused after tmux exits. Unverifiable or failed
cleanup retains the server record and artifacts. Per-test cleanup retries once
and runs while Go unwinds a test panic. The suite registry retries retained
failures when `m.Run` returns; an unhandled test panic terminates the process
before that fallback can run, so no in-process harness can promise cleanup in
that case or after a forced kill. After verified daemon death, retries resume
artifact removal without issuing another tmux command through files an earlier
attempt may already have removed. The final probe and removal rely on the
harness's private socket namespace: no test may start a replacement daemon
after it has returned control to cleanup. Tests never share a server. Parser
and generator units may be process-free; behavior tests use real tmux.

The reusable harness is built on supported Unix Go platforms where tmux offers
filesystem `-S` sockets. Other platforms compile a clear unsupported-platform
path; they do not pretend to run real-tmux tests.

The compatibility matrix covers the same supported tmux releases as Python.
The race lane stresses parallel server creation, global options, hooks,
environment, cancellation, cleanup, and control-client teardown.

## Releasing

This is alpha. Releases carry an `-alpha` prerelease tag, and the lowest clean
one is `v0.0.1-alpha.1`: semver permits `v0.0.0-alpha.1`, but `v0.0.0-` is the
namespace the go command generates pseudo-versions in, so a real tag there reads
as one. Under semver a prerelease sorts below its own release, and `v0.x` already
carries no compatibility promise, so the tag says what the version number alone
would leave implied.

Each module is tagged in its own directory, which is how the go command finds a
module that does not sit at the repository root:

| Module | Tag |
| --- | --- |
| the tmux module | `vX.Y.Z` |
| `mcp` | `mcp/vX.Y.Z` |
| `workspace` | `workspace/vX.Y.Z` |
| `benchmarks`, `examples` | not published |

The shape rather than the numbers: a table naming the current tags is a copy of
`git tag` that nothing updates.

A consumer module's `require` on the core has to name a version the proxy can
resolve, so the core is tagged before the modules that depend on it. The local
`replace` directives are what make the tree build before any of that exists;
they are not a substitute for the requirement.

## One tmux server, chosen once

The MCP server addresses the socket it was started with and nothing in a call
can retarget it: `-socket-name`, `-socket-path`, `LIBTMUX_SOCKET`, or
`LIBTMUX_SOCKET_PATH`, resolved at launch, with `-doctor` naming which was
taken. The Python server of the same name takes `socket_name` on 48 of its
tools instead, so a call chooses.

This is a decision rather than an omission, and the reasoning is not
ergonomics. An MCP server runs with the operator's authority and is driven by a
model reading text it did not write. A per-call socket makes "which tmux" part
of that text, so a model that is talked into a socket path reaches a tmux the
operator never granted — every other server on the machine, including one
holding a session someone is working in. Fixing the target at launch makes that
unreachable by construction rather than by a check that has to be right every
time.

Two alternatives were weighed. Taking a socket per call and validating it
against an operator-declared allowlist keeps the reach and adds a list to get
wrong, and the failure is silent: an allowlist with one entry too many is
indistinguishable from a correct one until it matters. Running one server per
tmux socket is what this design already supports, costs a process, and is what
`list_servers` exists to make discoverable — it reports the other servers on
the machine precisely so a person can point a second instance at one, while
nothing a model says can reach them from this one.

The cost is real and is not hidden: a client that wants two tmux servers runs
two of these. That is the trade, taken deliberately.

## Bakeoff decisions

| Problem | Selected approach | Rejected approaches |
| --- | --- | --- |
| Execution | Private one-method runner plus concrete request/result values | Subprocess calls spread through objects; an exported operation hierarchy |
| Transport selection | `Engine` with a two-member `CommandKind`, selected per handle, routed by the `Server` | A bare `CommandRunner` with no capability report; a name-keyed engine registry; argv re-parsed by each engine |
| Queries | Generated criteria with explicit validation and predicate compilation | Regex work inside every match; forcing filters through a generic matcher interface |
| Test isolation | Per-test explicit `-S` socket with two-layer cleanup | Named `-L` sockets; a shared suite server |
| Batching | Recorded `Op` values, a forward-reference `Ref`, and planners as values | A name-keyed planner registry; attributing a merged stdout to grouped operations by position; a `Result` interface or a type parameter per operation kind |
| MCP listing criteria | Typed criteria matched in Go against the snapshot the tool already takes | A caller-supplied tmux `-f` expression; typed criteria compiled into one |
| MCP detached commands | An in-process handle table, bounded, whose entries keep their answer once read | A handle encoding the paths it needs; one-shot collection |
| MCP per-pane state | A field on the listing's own row type | A field on the shared pane summary; a separate digest tool |
| MCP command output | Reading the pane's grid between two cursor marks | Teeing the command's bytes to a file; copying the pane's byte stream with `pipe-pane` |

The execution bakeoff showed that some apparently missing tmux targets still
return success for `display-message`; failure tests use commands with stable
failure semantics. The query bakeoff showed that compilation belongs before
iteration and that fresh-slice ownership plus isolated top-level value copies
can be preserved with one allocation.
The harness bakeoff exposed inherited pane targeting, stale named sockets,
shared global-state leakage, and the need to control both Go temporary-root
variables.
The listing bakeoff was settled by a security result rather than by ergonomics.
tmux's `-f` filter is a format, and a format containing `#(...)` reaches
`format_job_get` and runs it as a shell job. It does not reproduce from a
one-shot tmux client, because the job is filed under that client's job tree and
`server_client_lost` frees it microseconds later; it reproduces first try over a
control-mode client, which is what the MCP server holds open whenever the tmux
server has a session. Passing a caller's filter through would therefore have
made every listing tool an execution vector while it still reported
`readOnlyHint: true`, and would have done so only on the transport the server
prefers. Compiling typed criteria into `-f` was rejected for the same reason one
level removed: the pushdown it buys is a local pipe carrying a few kilobytes,
and it puts a format assembler on the boundary for good.

The detached-command bakeoff settled two things. A handle that carried the paths
it needs would be a caller-supplied path this server later reads, so the state
stays in process. One-shot collection was rejected after the annotation gate
caught it: a handle that stops answering once read is not idempotent, and asking
twice is how a caller checks on something, so the first read that finds a status
keeps it and every later read is answered from that.

The per-pane state bakeoff was settled by measuring the tool list. Hanging the
state off the shared pane summary added its schema to the four other tools that
report a pane and never fill it in -- 304 bytes each, advertised to every client
on every session. A row type belonging to the listing costs it once.

The command-output bakeoff was run because the grid arithmetic had accumulated
five compensations -- an echo to remove, a grid that moved, a scrollback that
was erased, a screen that was cleared, rows that wrapped -- and each one looked
like a reason to replace the approach rather than patch it again. Twenty-five
shapes were put to all three: the marks answered twenty-four, and both
byte-capture approaches answered twenty.

The four they lost are the same four, and they are the point. A byte capture
returns what the program wrote; the grid returns what a terminal shows. So
`clear` arrives as `ESC[H ESC[J ESC[3J`, a progress bar written with carriage
returns arrives as every frame it drew rather than as the one it left, and a
coloured word arrives wrapped in escapes. tmux has already done that rendering,
and redoing it outside tmux is a terminal emulator.

Teeing lost a second time, decisively: a command whose stdout is a pipe is not
a command running in a terminal. `[ -t 1 ]` reports a pipe, so colour is off,
paging is off, and any program that branches on `isatty` takes the other
branch -- which is the opposite of what a tool called `run_command` on a tmux
pane is for. `pipe-pane` keeps the tty, and is the approach to reach for if the
requirement ever becomes bytes rather than what the pane shows.

The compensations are therefore the price of the answer, not evidence against
the approach. What the bakeoff did find was a case none of them covered: a
command whose whole output is blank lines was reported as having printed
nothing, because a capture that is nothing but empty lines arrives as no lines
at all. Both losers got it right for free, and the marks already count the rows,
so the fix is theirs grafted onto the winner.

The batching bakeoff settled the result shape. Distinguishing what an operation
produced by its Go type reaches for either an interface with one implementation
or a type parameter that every plan method would have to thread, and both buy
less than they cost: an operation produces at most an ID, at most a stdout, and
a status. One `OpResult` carrying all three, with the fields an operation does
not produce left zero, is what the caller reads.
