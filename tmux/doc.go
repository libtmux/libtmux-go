// Package tmux provides a typed, context-aware API for tmux 3.2a through
// tmux 3.7b.
//
//	import "github.com/libtmux/libtmux-go/tmux"
//
//	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-app"})
//
// [NewServer] records configuration without starting tmux. Its zero-value
// [Server] targets tmux with its default socket and process environment.
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
//   - Reuse a control-mode transport with [Server.OpenControlPool], or receive
//     tmux notifications with [Server.OpenControl].
//   - Batch dependent commands with [NewPlan].
//
// The tmuxtest package runs integration tests against an isolated real tmux.
// [ServerOptions.Runner] substitutes process execution for unit tests or custom
// transports.
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
// the exact linked view matters. [Window.Equal] and [Pane.Equal] compare stable
// tmux identity and deliberately collapse linked views.
//
// [Snapshot] is observational, not transactional. It verifies the server
// identity around collection but cannot make several tmux commands atomic.
// Relationship accessors return new slices of shallow copies.
// Search methods use the same opening and closing identity probes and fail if
// the tmux daemon changes during their listing.
//
// Record relationship methods such as [Session.Windows] and [Window.Panes]
// return a bool because targeted lookups do not materialize relations. False
// means "not loaded," not "loaded and empty." A record from a snapshot retains
// the complete snapshot graph; Refresh returns a standalone record when that
// retention is undesirable.
//
// # Errors and context
//
// All I/O accepts a context. Cancellation stops this package from waiting; it
// does not prove whether tmux applied a mutation. Operations may make partial
// progress, and the package does not provide rollback.
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
// The default transport starts one tmux process per command. [Engine] lets a
// [Server] carry supported commands another way without changing their meaning.
// Unsupported engine operations normally fall back to subprocesses;
// [Server.WithEngineFallback] with [EngineFallbackReject] makes fallback an
// error.
//
// [Server.OpenControl] opens one persistent control-mode client.
// [Server.OpenControlPool] opens several command-carrying clients and returns a
// server and session already bound to the pool. Control connections appear as
// attached tmux clients, affect session_attached and hooks, and must be closed
// by their owner.
//
// Records retain the [Server] that materialized them. A record obtained before
// selecting an engine continues to use its old transport; use [Pane.WithEngine]
// and its counterparts or use the session returned by [Server.OpenControlPool].
// [WarningControlPoolUnused] reports this behavior when a pool can detect it.
//
// Printed captures remain subprocess operations because control-mode replies
// do not preserve the same byte contract. [Pane.CaptureToFile] avoids that
// process on an engine-backed server by staging output through a tmux buffer
// and a caller-supplied file.
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
