package tmux

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// Server is an immutable handle to one tmux configuration. Its zero value
// is invalid. Copying a configured Server preserves its configuration and
// shares version-cache coordination; only the documented concurrent operations
// are safe to share.
//
// [NewServer] resolves the executable, socket, and environment once and does
// not start tmux; creating the first session starts the daemon.
// A plain Server starts a tmux process for each command it sends, so
// discarding one leaves the daemon running; [Server.Kill] stops it.
// A Server from [Connection.Server] sends its commands over that connection's
// control lanes instead.
// Operations on the zero Server return [ErrInvalidServer].
//
// # What is on here
//
// Operations on one session, window, or pane live on [Session], [Window], and
// [Pane], such as [Session.NewWindow], [Window.SplitPane], [Pane.SendKeys], and
// [Pane.Capture].
// This type holds what belongs to the server as a whole, in the groups below.
//
// # Connecting
//
// Build a handle from a [ServerOptions] or from the TMUX variable;
// neither starts tmux.
// Of the two liveness checks, one answers false for an absent server and the
// other makes an absent server an error.
// A version minimum fails with [ErrVersionTooLow], and a control-mode client,
// or a session created together with its [Connection], needs tmux 3.7.
//
//   - Build: [NewServer], [NewServerFromEnv]
//   - Liveness: [Server.IsAlive], [Server.CheckAlive]
//   - Version: [Server.Version], [Server.RefreshVersion],
//     [Server.RequireVersion]
//   - Control mode: [Server.OpenControl], [Server.OpenNotifications],
//     [Server.NewSessionConnection], [Server.ConnectionBound]
//   - Built from: [Server.SocketPath], [Server.SocketSelection],
//     [Server.ConfigFile], [Server.Executable], [Server.ProcessEnvironment]
//   - Copies: [Server.WithSocketPath], [Server.WithProcessEnvironmentValue]
//   - Identity: [Server.Equal], [Server.String]
//
// # Finding one thing
//
// A lookup that finds nothing returns an error matching [ErrNotFound],
// or [ErrNoServer] when no server is running.
// The existence check returns a bool instead,
// and a missing server counts as no session.
//
//   - By ID: [Server.Session], [Server.Window], [Server.Pane]
//   - By name: [Server.SessionByName], [Server.Client]
//   - Exists: [Server.HasSession]
//
// # Listing everything
//
// Each listing returns one kind of record,
// and the window and pane listings cover every window link.
// Failures, including [ErrNoServer], stay errors,
// so an outage does not read as an empty server.
// A [Snapshot] reads the sessions, windows, panes, and clients together,
// as sequential commands rather than one atomic transaction.
//
//   - Records: [Server.Sessions], [Server.Windows], [Server.Panes],
//     [Server.Clients]
//   - Together: [Server.Snapshot]
//   - Raw lines: [Server.ListClients]
//
// # Querying things and getting around
//
// The search methods pass a [TmuxFilter] to tmux,
// which drops the rows that do not match before any record exists.
// To filter in Go instead, apply a [SessionFilter], [WindowFilter],
// [PaneFilter], or [ClientFilter] to a slice with tmuxq.Matching,
// or pass a predicate to tmuxq.Where, tmuxq.First, or tmuxq.ExactlyOne,
// which fails unless exactly one value matches.
// Records from a snapshot, and from the listings above, know their neighbors
// without querying tmux,
// but a record from a search, a lookup, or [Server.NewSession] carries no
// relations and reports false;
// the Resolve methods read live state for any record.
//
//   - Search from the server: [Server.SearchSessions], [Server.SearchWindows],
//     [Server.SearchPanes], [Server.SearchClients]
//   - Search below one record: [Session.SearchWindows], [Session.SearchPanes],
//     [Window.SearchPanes]
//   - Look up in a snapshot: [Snapshot.SessionByID], [Snapshot.WindowByID],
//     [Snapshot.PaneByID], [Snapshot.ClientByName]
//   - Down: [Session.Windows], [Session.Panes], [Window.Panes]
//   - Up: [Pane.Window], [Pane.Session], [Window.Session]
//   - Across: [Session.ActiveWindow], [Session.ActivePane],
//     [Window.ActivePane], [Client.AttachedSession]
//   - Live: [Pane.ResolveWindow], [Pane.ResolveSession],
//     [Window.ResolveSession], [Session.ResolveActiveWindow],
//     [Session.ResolveActivePane], [Window.ResolveActivePane]
//
// # Changing things
//
// A new session is detached and never changes client focus;
// the first one starts the daemon.
// A kill takes everything inside with it:
// one stops the daemon with all its sessions, windows, panes, and clients,
// and the other ends the session a tmux target selects,
// leaving pattern and prefix matching to tmux.
// Start starts the daemon without a session,
// so with tmux's exit-empty default it can exit before the next command.
//
// [Server.NewSession], [Server.Kill], [Server.KillSession], and [Server.Start]
//
// # Options and hooks
//
// Options read back as a typed [ServerOptionValues], or one raw value by name,
// and a write by name reaches an option the typed setters do not cover.
// Each known option has a typed setter that [ServerOptionValues] pairs with its
// accessor, and an array option such as command-alias is replaced whole by a
// setter whose result reports the writes confirmed.
// The defaults that sessions and windows inherit, and the global hooks,
// sit on two scope handles rather than on Server itself.
//
//   - Read: [Server.Options], [Server.RawOption]
//   - Write by name: [Server.SetOption], [Server.AppendOption],
//     [Server.UnsetOption]
//   - Typed setters, for example: [Server.SetEscapeTime],
//     [Server.SetExitEmpty], [Server.SetBufferLimit]
//   - Array setters, for example: [Server.SetCommandAlias],
//     [Server.SetTerminalFeatures]
//   - Scopes: [Server.GlobalSessionScope], [Server.GlobalWindowScope]
//   - Session-scope hooks: [GlobalSessionScope.Hooks],
//     [GlobalSessionScope.SetHook], [GlobalSessionScope.AppendHook],
//     [GlobalSessionScope.UnsetHook], [GlobalSessionScope.RunHook]
//   - Window-scope hooks: [GlobalWindowScope.Hooks],
//     [GlobalWindowScope.SetHook], [GlobalWindowScope.AppendHook],
//     [GlobalWindowScope.UnsetHook], [GlobalWindowScope.RunHook]
//
// # Everything else tmux keeps
//
// Paste buffers read as lines or as exact bytes and load from a string, a file,
// or a stream;
// the stream forms return [ErrConnectionRequiresProcess] on a handle bound to a
// [Connection], which loads and saves by path instead.
// A shell command's captured output needs tmux 3.5,
// and before tmux 3.8 abandoning a wait-for lock costs its channel one unlock.
// Layout validation checks each layout and its pane count before any mutation.
//
//   - Buffers: [Server.ListBuffers], [Server.ShowBuffer],
//     [Server.ShowBufferBytes], [Server.SetBuffer], [Server.LoadBuffer],
//     [Server.LoadBufferFrom], [Server.SaveBuffer], [Server.SaveBufferTo],
//     [Server.DeleteBuffer]
//   - Keys: [Server.BindKey], [Server.UnbindKey], [Server.ListKeys]
//   - Environment: [Server.ShowEnvironment], [Server.GetEnvironment],
//     [Server.SetEnvironment], [Server.UnsetEnvironment],
//     [Server.RemoveEnvironment]
//   - Configuration and layouts: [Server.SourceFile], [Server.ValidateLayouts]
//   - Shell: [Server.RunShell], [Server.IfShell]
//   - Channels: [Server.WaitFor]
//   - Messages and formats: [Server.ShowMessages], [Server.DisplayMessage]
//   - Prompt history: [Server.ShowPromptHistory], [Server.ClearPromptHistory]
//   - Commands and access: [Server.ListCommands], [Server.ServerAccess]
//
// # Things that need a terminal
//
// Menus, prompts, and confirmations draw on an attached client,
// and tmux refuses them without one.
// Attaching hands the caller's own terminal to a session and blocks until it
// detaches.
// Switching, detaching, locking, suspending, and refreshing a client act on a
// client, named or tmux's current one, and fail without one.
// Locking the server instead locks every attached client.
//
//   - Draw: [Server.DisplayMenu], [Server.CommandPrompt],
//     [Server.ConfirmBefore]
//   - Attach: [Server.AttachSession]
//   - Act on a client: [Server.SwitchClient], [Server.DetachClient],
//     [Server.DetachAllClients], [Server.LockClient], [Server.SuspendClient],
//     [Server.RefreshClient]
//   - Every client: [Server.LockServer]
//
// Anything tmux can do that is not here is reachable through [Server.Cmd],
// which runs raw tmux arguments and returns a [CommandResult];
// a completed nonzero exit is a result, not an error.
type Server struct {
	state           *serverState
	connection      *Connection
	daemon          *snapshotServerIdentity
	startupVersion  *Version
	requiredVersion Version
	// probesIdentity marks the private identity bootstrap, which must never
	// start a daemon.
	probesIdentity bool
	// requiresProcess marks exact-output and interactive operations that cannot
	// cross a persistent control connection.
	requiresProcess bool
	// inheritsLocale marks an operation whose tmux output reaches the caller's
	// own terminal rather than this package, so tmux's locale detection
	// governs it instead of the UTF-8 commandArguments otherwise asks for.
	inheritsLocale bool
}

type serverState struct {
	config   serverConfig
	executor commandRunner
	shared   *serverShared
}

// serverShared coordinates version caching across handles that address the
// same daemon with different process environments.
type serverShared struct {
	version versionCache

	checkedMu sync.Mutex
	checked   *snapshotServerIdentity
}

// checkedDaemon returns the identity last checked at this endpoint.
func (s *serverShared) checkedDaemon() *snapshotServerIdentity {
	s.checkedMu.Lock()
	defer s.checkedMu.Unlock()
	return s.checked
}

// rememberChecked records a version-checked identity, or forgets it when nil. The
// if-shell fence on every dispatch is what proves the record still describes
// the live daemon.
func (s *serverShared) rememberChecked(identity *snapshotServerIdentity) {
	s.checkedMu.Lock()
	defer s.checkedMu.Unlock()
	s.checked = identity
}

// coordination returns the daemon-scoped shared state.
func (s *serverState) coordination() *serverShared {
	return s.shared
}

type commandRunner interface {
	Run(context.Context, tmuxcmd.Request) (tmuxcmd.Result, error)
}

// SocketPath returns the absolute socket path selected when the server was
// constructed. An invalid server returns an empty string.
func (s Server) SocketPath() string {
	if s.state == nil {
		return ""
	}
	return s.state.config.socketSelection.Path
}

// ConfigFile returns the configured tmux configuration path.
func (s Server) ConfigFile() string {
	if s.state == nil {
		return ""
	}
	return s.state.config.configFile
}

// ProcessEnvironment returns the normalized environment explicitly supplied
// through [ServerOptions]. Nil means the server inherited a private process
// snapshot. The returned slice is owned by the caller; an invalid server
// returns nil.
func (s Server) ProcessEnvironment() []string {
	if s.state == nil {
		return nil
	}
	return slices.Clone(s.state.config.configuredProcessEnvironment)
}

// Executable returns the absolute tmux executable path resolved when the
// server was constructed. An invalid server returns an empty string.
func (s Server) Executable() string {
	if s.state == nil {
		return ""
	}
	return s.state.config.executable
}

// Cmd executes raw tmux arguments and returns caller-owned result slices. A
// completed nonzero exit remains a result; validation and transport failures
// return errors. Cancellation cannot prove whether a mutation reached tmux.
//
// An argument that is exactly ";" separates two tmux commands, so one call can
// submit a command list. tmux runs a list until a command fails and drops the
// rest, and answers with one merged stdout, so a caller that needs to know
// which command produced which line submits them separately. A list means the
// same thing through either execution binding.
//
// Standalone -V and -h diagnose the configured executable without daemon version
// checks. They do not qualify a later daemon operation.
//
// A standalone ";" separates commands across bindings. The subprocess path
// also follows tmux's trailing-semicolon and escaping rules; typed operations
// keep values literal across bindings.
//
// On a [Connection]-bound server Cmd uses a lane, and tmux cannot cancel a
// command already sent, so a command that waits on something outside tmux,
// such as wait-for or a foreground run-shell, holds that lane until it
// finishes even after ctx ends. [Server.WaitFor], [Server.RunShell], and
// [Server.IfShell] take a client of their own instead.
func (s Server) Cmd(ctx context.Context, args ...string) (CommandResult, error) {
	if len(args) == 1 && (args[0] == "-V" || args[0] == "-h") {
		result, err := s.runExactArgv(ctx, args)
		return cloneCommandResult(CommandResult{
			Command: result.Command, Stdout: result.Stdout, RawStdout: result.RawStdout,
			Stderr: result.Stderr, ExitCode: result.ExitCode,
		}), err
	}
	result, _, err := s.dispatch(ctx, true, args...)
	return result, err
}

func (s Server) literalCmd(ctx context.Context, args ...string) (CommandResult, error) {
	result, _, err := s.literalCmdWithRaw(ctx, args...)
	return result, err
}

// literalCmdAs is literalCmd for a command whose kind the caller knows, which
// is how one that can hold its client keeps off a connection's shared lanes.
func (s Server) literalCmdAs(
	ctx context.Context,
	kind commandKind,
	args ...string,
) (CommandResult, error) {
	if err := validateLiteralCommandArguments(args); err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	result, _, err := s.dispatchAs(ctx, kind, false, args...)
	return result, err
}

func (s Server) literalCmdWithRaw(
	ctx context.Context,
	args ...string,
) (CommandResult, []byte, error) {
	if err := validateLiteralCommandArguments(args); err != nil {
		return CommandResult{ExitCode: -1}, nil, err
	}
	return s.dispatch(ctx, false, args...)
}

// dispatch runs one tmux command. commandList reports whether a bare ";" in
// args separates two commands, which decides how each transport renders it.
func (s Server) dispatch(
	ctx context.Context,
	commandList bool,
	args ...string,
) (CommandResult, []byte, error) {
	return s.dispatchAs(ctx, commandServer, commandList, args...)
}

func (s Server) dispatchAs(
	ctx context.Context,
	kind commandKind,
	commandList bool,
	args ...string,
) (CommandResult, []byte, error) {
	result, err := s.runCommand(ctx, kind, args, nil, commandList)
	commandResult := cloneCommandResult(CommandResult{
		Command:   result.Command,
		Stdout:    result.Stdout,
		RawStdout: result.RawStdout,
		Stderr:    result.Stderr,
		ExitCode:  result.ExitCode,
	})
	if err != nil {
		return commandResult, bytes.Clone(result.RawStdout), commandTransportFailure(err)
	}
	return commandResult, bytes.Clone(result.RawStdout), nil
}

// IsAlive reports whether a tmux server answers on the configured socket.
// Only an absent server reports false without an error: a socket that exists
// but cannot be reached, such as one the process may not read, is a question
// that could not be answered and is returned as an error. Use
// [Server.CheckAlive] where an absent server is itself the failure.
func (s Server) IsAlive(ctx context.Context) (bool, error) {
	result, err := s.literalCmd(ctx, "list-sessions")
	if err != nil {
		return false, err
	}
	if result.ExitCode == 0 {
		return true, nil
	}
	commandErr := newCommandError("list-sessions", result)
	if errors.Is(commandErr, ErrNoServer) {
		return false, nil
	}
	return false, commandErr
}

// CheckAlive returns a [CommandError] when the configured server is not
// alive. Cancellation and transport failures are returned directly. Use
// [Server.IsAlive] where an absent server is an answer rather than a
// failure.
func (s Server) CheckAlive(ctx context.Context) error {
	result, err := s.literalCmd(ctx, "list-sessions")
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return newCommandError("list-sessions", result)
	}
	return nil
}

func (s Server) connectionState() *serverState {
	return s.state
}

func (s Server) stateForUse() (*serverState, error) {
	if s.state == nil || s.state.executor == nil || s.state.shared == nil ||
		s.state.config.executable == "" || s.state.config.directory == "" {
		return nil, ErrInvalidServer
	}
	return s.state, nil
}

func (s Server) commandArguments(args []string) []string {
	if s.state == nil {
		return slices.Clone(args)
	}
	config := s.state.config
	selectorFlag, selectorValue := effectiveSocketSelectorValues(
		config.socketPath,
		config.socketName,
	)
	globalCount := 1
	if config.colors != ColorDefault {
		globalCount++
	}
	if config.configFile != "" {
		globalCount++
	}
	if selectorValue != "" {
		globalCount++
	}

	command := make([]string, 0, globalCount+len(args))
	// tmux replaces every non-ASCII byte in a format it expands for a command
	// or control client whose locale does not name UTF-8, so a session named
	// café reads back as caf_ with no error. Captured bytes go out whole
	// either way; formats are what this is for. Go strings are UTF-8 and the environment
	// this package runs under is not its to choose, so it asks for UTF-8
	// rather than inheriting whatever the locale says.
	if !s.inheritsLocale {
		command = append(command, "-u")
	}
	if config.colors == Color256 {
		command = append(command, "-2")
	}
	// Global arguments are consumed before tmux's command-list parser. Their
	// values are already literal; a separator escape would become part of the
	// configured path or socket name.
	if config.configFile != "" {
		command = append(command, "-f"+config.configFile)
	}
	if selectorValue != "" {
		command = append(command, selectorFlag+selectorValue)
	}
	return append(command, args...)
}

func validateConnectionArguments(options ServerOptions) error {
	if options.ConfigFile != "" {
		if err := validateServerCommandArgument(
			"tmux", "ConfigFile", options.ConfigFile, true,
		); err != nil {
			return err
		}
	}
	selectorFlag, selectorValue := effectiveSocketSelector(options)
	if selectorValue == "" {
		return nil
	}
	field := "SocketName"
	if selectorFlag == "-S" {
		field = "SocketPath"
	}
	return validateServerCommandArgument("tmux", field, selectorValue, true)
}

func effectiveSocketSelector(options ServerOptions) (flag string, value string) {
	return effectiveSocketSelectorValues(options.SocketPath, options.SocketName)
}

func effectiveSocketSelectorValues(socketPath, socketName string) (flag string, value string) {
	if socketPath != "" {
		return "-S", socketPath
	}
	if socketName != "" {
		return "-L", socketName
	}
	return "", ""
}

func validateColorMode(mode ColorMode) error {
	switch mode {
	case ColorDefault, Color256:
		return nil
	default:
		return &ColorError{Mode: mode}
	}
}
