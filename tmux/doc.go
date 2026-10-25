// Package tmux provides a typed, context-aware API for tmux 3.3a through
// tmux 3.8.
//
//	import "github.com/libtmux/libtmux-go/tmux"
//
//	server, err := tmux.NewServer(tmux.ServerOptions{SocketName: "my-app"})
//	if err != nil {
//		log.Fatal(err)
//	}
//
// [NewServer] validates and snapshots configuration without starting tmux.
// The zero [Server] is invalid and operations on it return [ErrInvalidServer].
// Ordinary operations refuse daemons below [MinimumSupportedVersion], including
// unpatched 3.3, with [ErrVersionTooLow] before dispatch. An absent endpoint is
// checked by the executable that can create it. [Server.Version] remains an
// executable diagnostic on an unbound handle; [Server.RequireVersion] checks
// the selected daemon. Standalone Cmd -V and -h are executable diagnostics.
// They do not qualify later daemon operations. Unversioned OpenBSD base tmux
// and bare master reports are refused because they cannot establish a release;
// the error calls them unrecognized rather than too low. A refusal sends
// nothing: upgrade tmux and restart its server, or pin the previous release.
// A tmux older than 3.2 cannot answer the check and is refused the same way.
//
// # Where to start
//
//   - Create objects with [Server.NewSession], [Session.NewWindow], and
//     [Window.SplitPane].
//   - Find live objects with [Server.Session], [Server.Window], [Server.Pane],
//     and [Server.Client].
//   - Read the hierarchy with [Server.Snapshot].
//   - Capture pane text with [Pane.Capture], exact bytes with
//     [Pane.CaptureBytes], or a whole scrollback into an [io.Writer] with
//     [Pane.CaptureTo].
//   - Run arbitrary tmux commands with [Server.Cmd].
//   - Bind a materialized session to owned control lanes with
//     [Session.OpenControl], or receive notifications with
//     [Session.OpenNotifications] or [Server.OpenNotifications].
//   - Batch dependent commands with [NewPlan].
//   - Encode a record as JSON with [Pane.MarshalJSON] and its siblings.
//   - See what this package runs with [CommandObserver].
//
// The tmuxtest package runs integration tests against an isolated real tmux,
// and tmuxtest.ScriptedTmux answers named invocations from an executable it
// writes, for a test with no tmux to drive.
//
// # Commands that need a person
//
// Some tmux commands ask an attached client to show something - a menu, a
// prompt, a chooser, the clock - and do nothing a headless program can
// observe: [Server.CommandPrompt], [Server.ConfirmBefore],
// [Server.DisplayMenu], [Server.LockClient], [Server.LockServer],
// [Pane.ChooseTree], [Pane.ChooseBuffer], [Pane.ChooseClient],
// [Pane.ClockMode], [Pane.CustomizeMode], [Pane.DisplayPanes] and
// [Pane.FindWindow]. [Pane.DisplayPopup] is the exception: it can run a
// command and is useful with nobody watching.
//
// # Naming and call shapes
//
// Methods normally drop the receiver noun: kill-pane is [Pane.Kill] and
// rename-session is [Session.Rename]. A noun naming another object remains, as
// in [Session.NewWindow] and [Window.SplitPane].
//
// A method takes only a context when its receiver identifies the operation,
// positional values when all arguments are required, a Request value when any
// argument is optional, and required values followed by Options when it has
// both. A mutating method returns a newly materialized record when it changes
// that record's identity, placement, extent, selection, or process. Records do
// not refresh in place; use [Session.Refresh] and its counterparts after other
// mutations.
//
// Generated names follow tmux spelling. For example, bell-action maps to
// [Session.SetBellAction] and [SessionOptionValues.BellAction]. The documented
// tmux scope determines whether a setter belongs to [Server], [Session],
// [Window], [Pane], [GlobalSessionScope], or [GlobalWindowScope]. Raw option,
// hook, format, and command methods cover names outside the generated catalog.
//
// # Records, identity, and snapshots
//
// [Session], [Window], [Pane], and [Client] are materialized records, not live
// handles. Reading their fields and relationships performs no I/O. Search and
// Resolve methods query tmux; Refresh methods replace a stale record.
//
// A [Window] is a winlink view within a session. Linked sessions can therefore
// expose several views with the same [WindowID], and panes in those views share
// a [PaneID]. ID-only snapshot lookups may return [ErrSnapshotAmbiguous]; use
// [Snapshot.WindowsByID], [Snapshot.PanesByID], or relationship resolvers when
// the exact linked view matters. Equal methods compare stable tmux identity
// and the daemon provenance that materialized it. [Window.Equal] and
// [Pane.Equal] deliberately collapse linked views from that same daemon.
//
// [Snapshot] is observational, not transactional. It verifies the server
// identity around collection but cannot make several tmux commands atomic.
// Relationship accessors return new slices of shallow copies.
// Search methods use the same opening and closing identity probes and fail if
// the tmux daemon changes during their listing.
//
// Materialized records retain that daemon identity. Follow-up subprocess
// operations use an atomic tmux-side guard and refuse ordinary socket
// replacement with [ErrDaemonReplaced]. This guard assumes tmux's parser
// primitives and aliases remain trusted and stable. A [Connection] established
// before replacement is the exact-daemon boundary. Refs derived from records
// retain the same provenance, while [SessionRef], [WindowRef], and [PaneRef]
// remain selector-relative.
//
// Record relationship methods such as [Session.Windows] and [Window.Panes]
// return a bool because targeted lookups do not materialize relations. False
// means "not loaded," not "loaded and empty." A record from a snapshot retains
// the complete snapshot graph; Refresh returns a standalone record when that
// retention is undesirable.
//
// # Errors and context
//
// All tmux process and transport I/O accepts a context. [NewServer] performs
// synchronous local environment, working-directory, executable, and socket-path
// resolution before a server exists. Cancellation stops this package from
// waiting; it does not prove whether tmux applied a mutation. Operations may
// make partial progress, and the package does not provide rollback.
//
// [Server.Cmd] returns a completed nonzero tmux exit in [CommandResult], while
// validation, process, and transport failures are errors. Higher-level calls
// return classified errors such as [CommandError]. Check sentinels with
// errors.Is and concrete error values with errors.As.
//
// A mutation reports a completed failure from what tmux wrote to stderr, not
// from its exit status: tmux exits nonzero for a target that was already in
// the state asked for, and saying nothing is how it says so. A command tmux
// refused for a target it could not find matches [ErrNotFound], as does a
// lookup that found nothing.
//
// Reads use a bool for legitimate absence. A materialized read returns
// (T, bool). A live read of a setting that may be unset, [Server.RawOption]
// and [Session.RawHook] with their counterparts on each scope, returns
// (T, bool, error), separating "not set" from "could not ask." A live lookup
// of an object by its identity, such as [Server.Session], returns (T, error),
// and one that finds nothing matches [ErrNotFound]. Collection failures are
// never converted to empty collections.
//
// [CommandResult.RawStdout], [Pane.CaptureBytes], and
// [Server.ShowBufferBytes] preserve byte output. Decoded line methods are the
// more convenient choice when exact bytes are not part of the contract.
//
// # Execution modes
//
// [Session.OpenControl] returns a terminal [Connection]. Once established, its
// values retain the owned control lanes to that exact daemon. Closing the
// connection is terminal: those values return [ErrControlClosed] instead of
// reconnecting or falling back to subprocesses. Operations whose contract
// requires a process, including interactive attachment and exact-byte reads,
// return [ErrConnectionRequiresProcess] while it is open. When no session
// exists, [Server.NewSessionConnection] creates one and keeps the creating
// control process as its first lane.
//
// A plain [Server] starts a tmux process for each command it sends, so an
// operation returning a materialized record costs the listing too. A
// [Connection] owns persistent command lanes and returns model values
// already bound to them.
// Control connections appear as attached tmux clients, affect session_attached
// and hooks, and must be closed by their owner. On tmux 3.6 and later, their
// clients move to another session when the initial session is destroyed. On
// earlier releases, destruction follows the session's detach-on-destroy policy
// and may close the connection.
//
// Printed captures remain subprocess operations because control-mode replies
// do not preserve the same byte contract. [Pane.CaptureToFile] keeps its
// commands suitable for a connection by staging through a tmux buffer and
// caller-supplied file. A connection-backed server avoids those processes.
//
// [Session.Run] runs a command to its exit status in a window of its own, and
// [Session.Start] returns a [Running] handle before it finishes:
// [Running.Wait] waits for it, [Running.Kill] stops it, [Running.StreamTo]
// copies what it prints while it runs, and [Running.Pane] exposes the pane so
// it can also be typed into.
// [Pane.Writer] types into a pane and [PaneObservation.Reader] reads what it
// prints, as an [io.Writer] and an [io.Reader]. [Server.LoadBufferFrom],
// [Server.SaveBufferTo] and [Pane.CaptureTo] move a payload or a scrollback
// through tmux's own stdin and stdout, which no quoting rule and no command
// length limit apply to; tmux offers a control client neither stream, so those
// three need a process and their path-taking siblings are what a connection
// uses. [PaneObservation.WaitFor] waits for a pane to print something,
// woken by tmux as the pane writes it; [ControlClient.NextNotification] is
// that same stream as notifications, and [NotificationStream.Subscribe] asks
// tmux to report a format whenever its value changes. [Server.WaitFor] waits
// for an explicit tmux channel signal. Polling [Pane.Capture] reads the
// visible screen on a guess and may match a shell's command echo before the
// command produces output.
//
// # Plans
//
// [Plan] records commands and groups those whose results tmux can still
// attribute safely. A [Ref] can target an object that an earlier plan step will
// create. [Plan.Preview] performs no I/O, and [Plan.Explain] reports grouping.
//
// Plans are not transactions. tmux stops a command list at its first failure,
// after earlier commands may have changed state. Commands that print caller-used
// output or create an ID needed later run separately because tmux otherwise
// merges their results.
//
// # Optional and generated values
//
// Generated filter fields use pointers so false, zero, empty, and unset remain
// distinct. [Ptr] returns a distinct pointer even for zero-size values. Request
// fields use plain values only where zero unambiguously asks tmux for its
// default.
//
// Generated option, hook, format, and filter APIs are version-aware. Choice
// option types expose Valid methods; [SparseArray] preserves indexes and holes.
// Replacing an option array is ordered but not atomic and returns
// [SetArrayResult] with confirmed progress. Callers must serialize writes to
// the same target and option.
//
// [WarningHandler] receives compatibility warnings synchronously and may be
// called by concurrent server operations. Immutable values, snapshots, and
// returned copies support concurrent reads; no broader goroutine-safety
// guarantee is implied.
//
// # Compatibility and stability
//
// Requests using a tmux capability newer than the running server fail by
// default. [ServerOptions.Unsupported] can omit the unsupported flag and report
// a warning instead; see [UnsupportedPolicy].
//
// Releases are numbered 0.1.N, and each release increments N. The API is not
// settled: any release may change or remove exported identifiers or documented
// behavior, and the changelog marks each break. Pin an exact version to avoid
// one. Generated exports follow the same policy as handwritten API.
package tmux

// The repository root contains the Markdown generated from the sibling example
// modules.
//
//go:generate go run ./internal/generate/docs -root ..
