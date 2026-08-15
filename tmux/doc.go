// Package tmux provides a typed, context-aware API with tmux 3.2a as its
// minimum supported version, tested through tmux 3.7b.
//
// The import takes a name, because the module path ends in the repository name
// while the package is called tmux:
//
//	import "github.com/libtmux/libtmux-go/tmux"
//
// # Where to start
//
//   - Configure a server: [NewServer]. It records a binary, socket, and process
//     configuration without starting tmux.
//   - Create objects: [Server.NewSession], [Session.NewWindow],
//     [Window.SplitPane].
//   - Read what a pane shows now: [Pane.Capture] for lines or
//     [Pane.CaptureBytes] for exact bytes. Run something first with
//     [Pane.SendKeys]. Both start a tmux process even on a handle that selected
//     an engine; [Pane.CaptureToFile] is the pane read that does not.
//   - Wait for a pane to write something: [ControlClient.NextNotification],
//     which delivers a pane's output as tmux writes it. Prefer it to reading
//     the pane in a loop; see "Waiting for a pane" below for why.
//   - Wait for a command to finish rather than for what it printed:
//     [Server.WaitFor], tmux's own wait-for channel.
//   - Read the whole hierarchy at once: [Server.Snapshot].
//   - Find one live object: [Server.Session], [Server.Window], [Server.Pane],
//     [Server.Client]. To list what a record contains right now, use the search
//     methods such as [Window.SearchPanes]; a record's own [Window.Panes] and
//     its counterparts never query tmux.
//   - Watch tmux as it changes: [Server.OpenControl].
//   - Stop forking a tmux process per command: [Server.OpenControlPool], which
//     returns the handle, the session on it, and the pool that owns the
//     connections. Use the session it returns rather than the one passed in.
//     [Server.WithEngine] with [ControlClient.Engine] is the same thing
//     assembled by hand. Windows and panes already in hand move across with
//     [Pane.WithServer] and its counterparts.
//   - Read and set options and hooks: [Session.Options] and
//     [Session.SetBellAction] show the pair; "Finding an option or hook" below
//     gives the rule that maps every tmux name to its Go name, and
//     [Server.RawOption] and [Server.SetOption] cover names outside the catalog.
//   - Run any tmux command: [Server.Cmd].
//   - Test code that drives tmux: the tmuxtest package for a real server, or
//     [ServerOptions.Runner] to substitute the transport entirely.
//
// # Guessing a method
//
// The surface follows three rules, so a tmux command usually leads to its Go
// method without a search.
//
// The name drops the receiver's noun, because the receiver already carries it:
// kill-pane is [Pane.Kill], rename-session is [Session.Rename], link-window is
// [Window.Link]. The noun stays when the command creates an object of the
// receiver's kind, as in [Window.NewWindow], or acts on the receiver's siblings
// as a group, as in [Pane.DisplayPanes]. A noun naming a different object is
// not a repetition and always stays, so [Session.NewWindow] and
// [Window.SplitPane] keep theirs.
//
// The parameters say what is required. A method takes only a context when the
// receiver names everything, as [Window.Kill] does; typed positional values
// when every value is required, as [Session.Rename] does; a single request
// value when any field is optional, as [Pane.Capture] does; and required
// positional values plus a trailing options value when it has both, as
// [Session.SetOption] does. That last shape is why a call that sets no flags
// still writes an empty options literal: folding the required values into the
// request would let a caller omit them.
//
// The result says what changed. A method hands back a freshly materialized
// record when the command changes which object the caller is holding or what it
// looks like: its identity, its placement, its own extent, whether it is
// selected, or the process inside it. [Session.Rename], [Window.Resize], and
// [Pane.Select] do; the last takes a request where [Window.Select] takes only a
// context, because selecting a pane has optional direction and marking to
// express and selecting a window has none. Everything else returns only an error, including commands
// that change fields describing what a record is doing rather than which record
// it is, such as [Pane.CopyMode] and [Pane.Pipe]. Records never refresh in
// place, so [Session.Refresh] and its counterparts re-read one when a method
// returned no record.
//
// # Reading the type list
//
// Most of the names in this package belong to a few families, so a reader
// looking for one type can skip the rest rather than read an alphabetical
// list. A name ending in Request or Options is a parameter type, reached from
// a method that takes it rather than looked up on its own; one may be shared,
// as the option scopes share theirs. A name ending in Values is a generated reader for tmux's
// formats, options, or hooks, reached through an accessor such as
// [Pane.Formats] or [Session.Options]. A name ending in Error is a failure
// type, and the sentinels that classify one are package-level variables whose
// names begin with Err. A name ending in Filter describes a search, and one
// ending in ID, Kind, or Mode is a typed string or enumeration.
//
// What is left is the object model below, which is where to start.
//
// # Object model and lifecycle
//
// A [Server] is an immutable configuration handle. [NewServer] records a tmux
// binary, socket, and process configuration without starting tmux; its zero
// value targets tmux's default configuration. A [Session] contains [Window]
// winlink views, and each window view contains [Pane] views. A [Client] can
// point at one of those views. Calls that query or change tmux accept a
// context. Canceling the context stops this library's wait for the command;
// it does not establish whether a mutation reached tmux.
//
// Returned sessions, windows, panes, and clients are materialized records,
// not live handles. Changing a local record changes only that copy. Use
// [Server.Snapshot] to materialize a hierarchy, [Server.Session] and its
// counterparts for canonical live point lookups, or [Session.Refresh] and its
// counterparts to obtain a new record.
//
// # Identity and snapshots
//
// [WindowID] and [PaneID] are stable tmux identifiers, but linked sessions can
// expose more than one view with the same ID. An exact window view includes its
// session and index; an exact pane view adds its pane ID. [Window.Equal] and
// [Pane.Equal] deliberately collapse those linked views. ID-only snapshot
// lookups can therefore report [ErrSnapshotAmbiguous]; use [Snapshot.WindowsByID]
// or [Snapshot.PanesByID] to inspect every view, and methods such as
// [Session.ResolveActiveWindow] and [Pane.ResolveWindow] for exact
// linked-view relationships.
//
// A [Snapshot] is observational rather than transactional. Its relationship
// accessors never query tmux. They return newly allocated slices containing
// shallow copies, so changing a returned slice cannot change the snapshot.
//
// # Errors and context
//
// [Server.Cmd] exposes raw tmux results: [CommandResult.Stdout] provides decoded
// lines and [CommandResult.RawStdout] preserves exact tmux stdout bytes. A
// completed nonzero exit is returned as result data, while execution and
// validation failures are errors. [Pane.CaptureBytes] and
// [Server.ShowBufferBytes] provide byte-preserving high-level output. These
// bytes are tmux's output after tmux interprets pane terminal contents.
// Higher-level operations return classified errors such as [CommandError].
// Hierarchy collection reads are lenient by default and normalize completed
// command and transport failures to empty collections;
// [Server.WithStrictErrors] preserves those failures. Context, decode,
// server-identity, malformed successful version-output, and executable
// resolution failures are never normalized. A configured binary that cannot be
// resolved is caller configuration rather than server state, so it is reported
// in both modes as an os/exec.Error recoverable with errors.As; a tmux server
// that is merely not running remains an empty collection. Check sentinels with
// errors.Is and concrete error values with errors.As.
//
// Absence is not an error. A read that can legitimately find nothing reports
// that through a bool rather than through err, so the two outcomes stay
// distinguishable: ok reports whether the value exists, and err reports whether
// the question could be asked at all. Reads of materialized state return
// (T, bool) because they never query tmux, as in [Pane.Width] and
// [Snapshot.SessionByID]. Reads that do query tmux return (T, bool, error), as
// in [Server.RawOption], [Session.GetEnvironment], and
// [Session.ResolveActivePane]: an unset option, an unset variable, and a
// session with no active pane are all ok == false with a nil error.
//
// Operations can make partial progress. A returned error describes the failed
// operation only; callers must account for delivery ambiguity and should not
// assume a package-wide rollback rule.
//
// # Control mode
//
// [Server.OpenControl] starts a persistent attached control-mode process. Its
// startup context bounds process start, attach framing, and registration but
// does not own the returned [ControlClient]. [ControlClient.Cmd] safely encodes
// arguments without a shell and serializes concurrent requests. A tmux %error
// frame remains [ControlCommandResult] data through its Failed field; local,
// transport, protocol, and context failures are Go errors.
//
// [ControlCommandResult.RawStdout] contains the exact control frame payload.
// tmux versions may render escapes in that payload, so use [Pane.CaptureBytes]
// or [Server.ShowBufferBytes] when the high-level pane or buffer bytes are the
// required contract. [ControlClient.Notifications] ranges over what tmux says
// on its own, and [ControlClient.NextNotification] reads one; both preserve
// notification order and decode %output and %extended-output through
// [ControlNotification.Output]. Exactly one goroutine may read notifications
// at a time.
//
// Canceling a command after it is written returns the context error; the client
// drains that command's reply before sending another. [ControlClient.Wait]
// leaves queued notifications available through io.EOF; a terminal reader
// error follows any earlier queued records. [ControlClient.CloseContext] and
// [ControlClient.Close] reject unaccepted requests and give an accepted command
// a bounded frame-drain window before stopping the process and releasing the
// queue.
// [ControlClient.Reconnect] creates a new identity and does not replay commands.
//
// # Waiting for a pane
//
// Three ways to wait, in the order worth reaching for them.
//
// [ControlClient.NextNotification] delivers what a pane writes as tmux writes
// it. Nothing is read back, so nothing starts a tmux process per round, and
// events arrive in order, which is what makes the shell's echo something to
// skip past rather than something to tell apart. This is the one to reach for
// when a program's output is the thing being waited on.
//
// [Server.WaitFor] waits on tmux's own channel. Append a wait-for signal to a
// command and block on the channel: the wait ends when the work ends, and
// nothing is matched against anything. Reach for it when what the command
// printed does not matter.
//
// Reading the pane in a loop, with [Pane.Capture] wrapped in [Poll], is the
// fallback. It costs a tmux process per round even on a handle that selected
// an engine, because a printed capture cannot run over a control connection.
// It also searches a screen rather than a stream: a shell echoes what
// [Pane.SendKeys] typed, so the command text is on screen before the command
// runs, and searching that screen for a substring matches the request rather
// than the result. Sending "sleep 1; printf 'ready\n'" and searching for
// "ready" succeeds within milliseconds while the program answers a second
// later. Comparing whole lines survives that, because the echoed line carries
// the surrounding command, but only while the wanted line stands alone: output
// that arrives with a timestamp or a log level in front of it defeats the
// comparison and sends a caller back to the substring search that does not
// work.
//
// [Pane.CaptureToFile] answers the first of those two costs and not the second.
// It routes the capture through a tmux buffer and a file instead of tmux's
// stdout, so a loop on a connected handle starts no process per round; it reads
// the same screen, so everything above about the echo still holds.
//
// Whichever is used, a pane given its program directly through
// [Session.NewWindow] or [Window.SplitPane] has no shell in it, and so no echo
// to account for at all.
//
// # Choosing a mode
//
// Every command starts a tmux process unless something is turned on. Each of
// these is one line to turn on, one line to turn off, and independent of the
// others:
//
//	mode        turn it on            cost              reach for it
//	---------------------------------------------------------------------------
//	process     nothing, the default  a process each    one-shot commands
//	control     OpenControlPool       one tmux client   more than a few commands
//	concurrent  Connections: N        N tmux clients    parallel readers
//	chained     NewPlan then Run      no records back   builds and layouts
//	streaming   Notifications         a connection      watching what a pane does
//
// They combine. A plan run on a connected handle is chained and carried over
// the connection, which is the cheapest of them and still means exactly what
// the others mean.
//
// Concurrency is a size rather than a transport. [ControlPoolRequest] takes a
// number of connections and a caller's own goroutines decide what runs at once;
// each connection carries one tmux command at a time, so N is the number that
// can be in flight, and each counts as one more tmux client. Raise it for
// concurrent readers and treat the number as a cost rather than a dial.
//
// Streaming is the one that reads rather than writes. [ControlClient.Cmd]
// carries commands while [ControlClient.Notifications] ranges over what tmux
// says on its own -- pane output, and the events behind [ControlNotification]
// -- so a watcher does not poll:
//
//	for notification, err := range client.Notifications(ctx) {
//		if err != nil {
//			return err
//		}
//		if pane, output, ok := notification.Output(); ok {
//			handle(pane, output)
//		}
//	}
//
// The choice is only ever about cost, never about behavior, and the benchmarks
// module is what keeps that true: "go -C benchmarks run ." builds the same
// window every way and prints what each spent, and its test fails if any of
// them answers the same query differently.
//
// # What a transport costs
//
// Three ways exist to reach tmux, and they differ in what they cost outside
// this program as well as inside it. Speed is the obvious axis and the less
// important one: the other is what tmux tells everybody else about the
// session, because a tmux configuration can react to that.
//
// A tmux process per command is the default and is invisible. Nothing about
// the session changes because this package is driving it, so a configuration
// keyed on who is attached behaves as though nobody is. It is the slowest
// option and the only one with no footprint.
//
// A control connection, from [Server.OpenControlPool] or [Server.OpenControl],
// carries commands without starting processes, and is a tmux client for as
// long as it is open. It appears in list-clients, counts toward
// session_attached, fires a client-attached hook, and keeps destroy-unattached
// from reclaiming the session it attached to. A pool of several connections
// counts as several clients. This is why the fast path is chosen rather than
// automatic: a program that connected by default would make session_attached
// report a person watching a session that nobody is watching.
//
// [Pane.CaptureToFile] reads a pane without starting a process, by staging
// through a tmux buffer and a file rather than through the connection, since a
// printed capture cannot cross one. It costs a path that both tmux and this
// program can reach, and leaves the file behind.
//
// Code handed a [Server] can ask which of these its caller chose with
// [Server.Engine], and should leave that choice alone rather than connecting
// over it.
//
// # Sending several commands at once
//
// A [Plan] is the other axis. It records commands instead of running them, and
// sends the ones that need no answer to tmux together, as a tmux command list:
//
//	plan := tmux.NewPlan()
//	pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{})
//	plan.SendKeys(pane, tmux.SendKeysRequest{Command: tmux.Ptr("top")})
//	result, err := plan.Run(ctx, server)
//
// Each method mirrors the one that runs the same tmux command immediately and
// takes the same request, so a plan is written the way the same work is written
// without one. Both render through one builder, which is what stops a flag
// meaning different things planned and unplanned.
//
// The [Ref] returned by a recording method is the point. It addresses the pane
// that split is going to create, before it exists, so a build is written in one
// pass rather than stopping at each step to learn an ID.
//
// A command this package has no recorder for is still recordable: [Plan.Cmd]
// takes raw tmux arguments the way [Pane.Cmd] does, and a [Ref] still names
// what it acts on, so the escape hatch reaches a forward reference too.
//
// Recording touches nothing, so a plan can be read before it is run.
// [Plan.Preview] renders what would be sent and [Plan.Explain] reports how it
// would be grouped and why each group ends where it does.
//
// Reading it first is worth doing, because a plan is not atomic. tmux has no
// transaction, so an argument it would refuse at the last step is refused after
// every step before it has already changed something. [Plan.Preview] returns
// that as an error naming the step, and leaves only the steps whose target an
// earlier step has yet to create rendered as nil.
//
// What cannot be grouped is what tmux cannot report separately. tmux answers a
// command list with one merged stdout and one status, so an operation that
// prints something the caller reads, or an ID a later step needs, is sent on
// its own. Everything else travels together. That also fixes what a failure can
// say: tmux abandons a list at its first failure, so a plan stops there, and
// the operations sharing that dispatch cannot be told apart.
//
// What a plan costs is records. A method that runs a command returns the
// [Session], [Window], or [Pane] it changed, and a recorded operation returns
// an ID and a status, because a plan asks tmux once rather than after every
// step. Code that reads a record between steps wants the direct API, and is not
// giving anything up by staying there: a connection is the switch that makes
// that cheap, and it is a different switch.
//
// A plan helps most on a tmux process, where grouping removes a process per
// command. Over a control connection there is no process to remove and tmux
// still answers each command, so the saving is smaller -- the reason to plan
// there is the forward reference and the single round of results, not speed.
//
// # Engines
//
// By default every command in this package starts a tmux process. An [Engine]
// is a transport that can carry commands instead, and [Server.WithEngine]
// returns a handle that uses one:
//
//	client, err := server.OpenControl(ctx, session)
//	if err != nil {
//		return err
//	}
//	defer client.Close()
//	connected := server.WithEngine(client.Engine())
//
// Every operation means the same thing on the returned handle. Only the
// transport changes, so the code above is the whole difference between forking
// a tmux process per command and reusing one control-mode connection.
//
// A record carries the handle that produced it, so a session, window, pane, or
// client obtained before that call keeps starting a tmux process for every
// command and reports no error while doing so. [Pane.WithServer] and its
// counterparts on [Session], [Window], and [Client] move one across:
//
//	pane = pane.WithServer(connected)
//
// Nothing is read back, because a handle is configuration rather than state:
// the move is a struct copy, and the relations reached through the moved record
// come back on the same handle. [Server.Session] and its counterparts remain
// the way to obtain a record the caller does not already hold.
//
// One read stays on a process even on a connected handle, and there is a way
// around it. [Pane.Capture] and [Pane.CaptureBytes] promise tmux's own stdout
// bytes, which a control connection cannot deliver. [Pane.CaptureToFile]
// captures into a tmux buffer, saves that buffer to a path both tmux and the
// caller can reach, and reads it back: three commands that print nothing, in
// place of one that prints the pane. A loop built on it starts no tmux process
// per round. On a handle with no engine it is three processes where
// [Pane.Capture] is one, so it is a trade a connected handle makes rather than a
// better capture.
//
// An engine declares which [CommandKind] values it can carry, and a [Server]
// runs the rest as tmux processes through the same [ServerOptions.Runner] it
// always used. That is why selecting an engine can never remove an operation:
// interactive attachment needs a real terminal, tmux -V is a client-global
// option rather than a command, and [Pane.Capture], [Pane.CaptureBytes], and
// [Server.ShowBufferBytes] promise tmux's own stdout bytes rather than a
// transport's rendering of a reply. Each of those keeps starting a process on
// a handle that selected the control-mode engine.
//
// [Server.SubprocessEngine] is that default as a value, so a handle derived
// from a connected one can go back to starting processes.
// [ServerOptions.Runner] remains the seam that replaces process execution
// itself, and it stays in effect underneath any engine.
//
// # Filters and request values
//
// Generated filter criteria use pointers so false, zero, and empty values stay
// distinct from an unset field. [Ptr] returns a distinct pointer to a shallow
// copy for those criteria. Exact-match constructors such as [PaneCommandIs]
// cover common filters without pointer temporaries. Request fields use plain
// values where zero unambiguously means that tmux should select its default;
// this includes positive dimensions, counts, adjustments, and stable nominal
// targets.
//
// # Finding an option or hook
//
// tmux names map to Go names mechanically, so a name read from tmux(1) is
// enough to reach its Go API without scanning a method list.
//
// An option's Go accessor is its tmux name in Go spelling, and its setter is
// that name prefixed with Set. tmux's bell-action is [Session.SetBellAction]
// to write and [SessionOptionValues.BellAction] to read; main-pane-width is
// [Window.SetMainPaneWidth] and [WindowOptionValues.MainPaneWidth]. Each
// generated member links to its counterpart, so either half of a pair leads to
// the other.
//
// The scope tmux documents for an option decides the receiver:
//
//   - server options: [Server]
//   - session options: [Session], or [GlobalSessionScope] for the global set
//   - window options: [Window], or [GlobalWindowScope] for the global set
//   - pane options: [Pane]
//
// An option tmux accepts at more than one scope has a setter on each receiver,
// which is how the receiver expresses what a tmux -t target expresses:
// window-style is [Window.SetWindowStyle], [GlobalWindowScope.SetWindowStyle],
// and [Pane.SetWindowStyle].
//
// Hooks read the same way through [Session.Hooks] and its counterparts, and
// [SessionHookValues.ClientAttached] names the tmux hook it decodes. Hooks are
// written by name with [Session.SetHook] because a hook body is a tmux command
// rather than a typed value.
//
// Every generated option and hook member quotes its exact tmux spelling in its
// own documentation. Searching the rendered package documentation, or go doc
// -all output, for bell-action therefore reaches its setters and its accessor
// without knowing the Go spelling first.
//
// # Options, hooks, and concurrency
//
// Generated typed option and hook values cover known names. Scalar options
// have direct setters; choice options use named string types whose Valid method
// recognizes the supported-version union. Server options remain on [Server];
// [Server.GlobalSessionScope] and
// [Server.GlobalWindowScope] select global option and hook scopes before an
// operation. [Server.RawOption], [GlobalSessionScope.RawOption], and
// [GlobalSessionScope.RawHook] preserve caller-named values outside that
// catalog.
// [SparseArray] preserves array indexes and holes; typed array setters replace
// the complete local array and return [SetArrayResult] with confirmed progress.
// Replacement is ordered but not atomic, so callers must serialize concurrent
// writes to the same target and option. The receiver's UnsetOption method
// restores inheritance or the global default. Typed option values report
// explicit empty bases and inherited origin.
// Bulk Options and Hooks reads also use collection leniency: transport,
// completed-command, and applicable version-probe transport or command
// failures return zero value collections unless [Server.WithStrictErrors] is
// enabled. Context, decode, and malformed or invalid successful version output
// remain errors. Successfully decoded explicit empty values remain present.
// RawOption and RawHook do not use bulk leniency: a quiet missing name reports
// absent, while transport and command failures are returned. Mutations return
// failures regardless of the strict-error setting.
// [WarningHandler] receives compatibility warnings synchronously and may be
// called concurrently by server operations.
//
// # tmux vocabulary and raw fallbacks
//
// Core records expose their own scoped formats with receiver-shortened names:
// [Pane.Active], for example, decodes #{pane_active}. [Session.Formats],
// [Window.Formats], [Pane.Formats], and [Client.Formats] expose universal and
// projected fields through [FormatValues] using full tmux-token names such as
// [FormatValues.WindowName]. All return a typed value with an ok result and
// perform no tmux I/O. Use [FormatValues.Raw] when an empty or malformed
// expansion must remain distinguishable.
// Generated option and hook accessors likewise name the exact tmux option or
// hook, its scope, value type, and minimum version. RawOption, RawHook,
// SetOption, and SetHook are the adjacent escape hatches for caller-named
// values.
//
// Concurrent use is supported for [Server.Cmd], version-cache coordination,
// read-only snapshots and returned copy boundaries, and immutable values. No
// broader goroutine-safety guarantee is implied.
//
// # API stability
//
// The module is pre-v1. Until v1.0.0, a minor release may make a documented
// breaking API change. Starting with v1, exported identifiers, method
// signatures, error classification, and documented behavior follow semantic
// versioning: compatible additions may ship within v1, while removals and
// incompatible changes require a v2 module path.
//
// Generated exported identifiers follow the same policy as handwritten API.
// Python parity describes supported behavior, not a permanent name-for-name Go
// mapping; deliberate language translations and omissions are recorded in the
// parity manifest. The tmux compatibility range below is independent of the Go
// API version.
//
// # Compatibility
//
// tmux 3.2a is the minimum supported version. A configured socket name or path
// selects a particular tmux server; absent selectors use tmux's default socket.
// Version-gated optional behavior is reported through [WarningHandler] where
// the owning request documents it.
package tmux
