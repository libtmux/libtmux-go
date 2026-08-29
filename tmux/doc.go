// Package tmux provides a typed, context-aware API for tmux 3.2a through
// tmux 3.7c.
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
//
// # Where to start
//
//   - Create objects with [Server.NewSession], [Session.NewWindow], and
//     [Window.SplitPane].
//   - Find live objects with [Server.Session], [Server.Window], [Server.Pane],
//     and [Server.Client].
//   - Read the hierarchy with [Server.Snapshot].
//   - Capture pane text with [Pane.Capture] or exact bytes with
//     [Pane.CaptureBytes].
//   - Run arbitrary tmux commands with [Server.Cmd].
//   - Bind a materialized session to owned control lanes with
//     [Session.OpenControl], or receive notifications with [Server.OpenControl].
//   - Batch dependent commands with [NewPlan].
//
// The tmuxtest package runs integration tests against an isolated real tmux.
// Tests of process behavior can point [ServerOptions.Binary] at an executable
// fixture; construction still resolves and freezes it.
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
// Materialized records retain that daemon identity. Follow-up operations use
// an atomic tmux-side guard, so a daemon that later takes over the same socket
// cannot receive a stale record's command; the refusal matches
// [ErrDaemonReplaced]. Refs derived from records retain the same provenance,
// while [SessionRef], [WindowRef], and [PaneRef] remain selector-relative.
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
// Reads use a bool for legitimate absence. Materialized reads return (T, bool);
// live reads return (T, bool, error), separating "not found" from "could not
// ask." Collection failures are never converted to empty collections.
//
// [CommandResult.RawStdout], [Pane.CaptureBytes], and
// [Server.ShowBufferBytes] preserve byte output. Decoded line methods are the
// more convenient choice when exact bytes are not part of the contract.
//
// # Execution modes
//
// [Session.OpenControl] returns a terminal [Connection] to that session's exact
// daemon. Values obtained from [Connection.Server] and [Connection.Session]
// retain its owned control lanes. Closing the connection is terminal: those
// values return [ErrControlClosed] instead of reconnecting or falling back to
// subprocesses. Operations whose contract requires a process, including
// interactive attachment and exact-byte reads, return
// [ErrConnectionRequiresProcess] while it is open. When no session exists,
// [Server.NewSessionConnection] creates one and keeps the creating control
// process as its first lane.
//
// A plain [Server] starts one tmux process per operation. A [Connection] owns
// persistent command lanes and returns model values already bound to them.
// Control connections appear as attached tmux clients, affect session_attached
// and hooks, and must be closed by their owner. They require tmux 3.6 so
// destroying their initial session moves them to another session when one
// exists.
//
// Printed captures remain subprocess operations because control-mode replies
// do not preserve the same byte contract. [Pane.CaptureToFile] keeps its
// commands suitable for a connection by staging through a tmux buffer and
// caller-supplied file. A connection-backed server avoids those processes.
//
// [ControlClient.NextNotification] waits for pane output as a stream.
// [Server.WaitFor] waits for an explicit tmux channel signal. Polling
// [Pane.Capture] reads the visible screen and may match a shell's command echo
// before the command produces output.
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
// Releases are alpha until v1.0.0. Exported identifiers and documented behavior
// may change without a deprecation period, so pin an exact version. Generated
// exports follow the same policy as handwritten API.
package tmux

// The repository root contains the Markdown generated from the sibling example
// modules.
//
//go:generate go run ./internal/generate/docs -root ..
