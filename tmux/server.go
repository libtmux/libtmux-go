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
// [NewServer] validates one [ServerOptions] without starting tmux, and
// [NewServerFromEnv] reads the server this process is already inside from TMUX.
// [Server.IsAlive] answers whether anything is listening, and only an absent
// server reports false; [Server.CheckAlive] makes an absent server an error.
// [Server.Start] starts the daemon without a session, so with tmux's
// exit-empty default it can exit before the next command.
// [Server.Version] and [Server.RefreshVersion] report the configured tmux
// binary, and [Server.RequireVersion] fails with [ErrVersionTooLow] when it is
// older than a minimum.
//
// [Server.OpenControl] attaches a control-mode client to a session,
// [Server.OpenNotifications] opens an observation-only [NotificationStream] on
// one, and [Server.NewSessionConnection] creates a session together with a
// [Connection].
// [Server.ConnectionBound] reports whether a handle belongs to one.
//
// [Server.SocketPath], [Server.SocketSelection], [Server.ConfigFile],
// [Server.Executable], and [Server.ProcessEnvironment] report what the handle
// was built from.
// [Server.WithSocketPath] and [Server.WithProcessEnvironmentValue] return a
// copy that differs in that one setting.
// [Server.Equal] compares two handles by socket, and [Server.String] names the
// selector.
//
// # Finding one thing
//
// [Server.Session] and [Server.SessionByName] return one session,
// [Server.Window] and [Server.Pane] take tmux IDs,
// and [Server.Client] takes a client name.
// A lookup that finds nothing returns an error matching [ErrNotFound],
// or [ErrNoServer] when no server is running.
// [Server.HasSession] returns a bool instead,
// and a missing server counts as no session.
//
// # Listing everything
//
// [Server.Sessions], [Server.Windows], [Server.Panes], and [Server.Clients]
// list one kind of record each; [Server.Windows] returns one per window link.
// Failures, including [ErrNoServer], stay errors,
// so an outage does not read as an empty server.
// [Server.Snapshot] reads them together into a [Snapshot].
// [Server.ListClients] returns tmux's own client lines instead of records.
//
// # Querying things and getting around
//
// [Server.SearchSessions], [Server.SearchWindows], [Server.SearchPanes], and
// [Server.SearchClients] pass a [TmuxFilter] to tmux,
// which drops the rows that do not match before any record exists.
// [Session.SearchWindows], [Session.SearchPanes], and [Window.SearchPanes]
// search below one record the same way.
// To filter in Go instead, apply a [SessionFilter], [WindowFilter],
// [PaneFilter], or [ClientFilter] to a slice with tmuxq.Matching.
// tmuxq.Where, tmuxq.First, and tmuxq.ExactlyOne take a predicate function,
// and tmuxq.ExactlyOne fails unless exactly one value matches.
//
// A [Snapshot] answers further questions without another command:
// [Snapshot.SessionByID], [Snapshot.WindowByID], [Snapshot.PaneByID], and
// [Snapshot.ClientByName].
//
// Records from a snapshot, and from the listings above, know their neighbors.
// Go down with [Session.Windows], [Session.Panes], and [Window.Panes],
// up with [Pane.Window], [Pane.Session], and [Window.Session],
// and across to the focused child with [Session.ActiveWindow],
// [Session.ActivePane], and [Window.ActivePane].
// [Client.AttachedSession] reaches the session a client shows.
// None of them queries tmux, and each reports false on a record from a search,
// a lookup, or [Server.NewSession], which carries no relations.
//
// [Pane.ResolveWindow], [Pane.ResolveSession], [Window.ResolveSession],
// [Session.ResolveActiveWindow], [Session.ResolveActivePane], and
// [Window.ResolveActivePane] answer the same questions from live state,
// for any record.
//
// # Changing things
//
// [Server.NewSession] creates a detached session and returns its record.
// [Server.Kill] stops the daemon with everything in it,
// and [Server.KillSession] ends the session a tmux target selects.
//
// # Options and hooks
//
// [Server.Options] reads the server's options as a [ServerOptionValues].
// [Server.RawOption], [Server.SetOption], [Server.AppendOption], and
// [Server.UnsetOption] work by name, which reaches an option the typed setters
// do not cover.
// Known options also have typed setters, such as [Server.SetEscapeTime] and
// [Server.SetExitEmpty].
//
// [Server.GlobalSessionScope] and [Server.GlobalWindowScope] reach the defaults
// that sessions and windows inherit, and the global hooks,
// which are not methods of Server itself:
// [GlobalSessionScope.Hooks], [GlobalSessionScope.SetHook],
// [GlobalSessionScope.AppendHook], [GlobalSessionScope.UnsetHook], and
// [GlobalSessionScope.RunHook], with the same on [GlobalWindowScope].
//
// # Everything else tmux keeps
//
// Paste buffers ([Server.ListBuffers], [Server.ShowBuffer],
// [Server.ShowBufferBytes], [Server.SetBuffer], [Server.LoadBuffer],
// [Server.LoadBufferFrom], [Server.SaveBuffer], [Server.SaveBufferTo],
// [Server.DeleteBuffer]),
// key bindings ([Server.BindKey], [Server.UnbindKey], [Server.ListKeys]),
// the global environment ([Server.ShowEnvironment], [Server.GetEnvironment],
// [Server.SetEnvironment], [Server.UnsetEnvironment],
// [Server.RemoveEnvironment]),
// configuration ([Server.SourceFile]),
// shell commands ([Server.RunShell], [Server.IfShell]),
// wait-for channels, to wait, signal, lock, or unlock ([Server.WaitFor]),
// the message log ([Server.ShowMessages]),
// format expansion ([Server.DisplayMessage]),
// prompt history ([Server.ShowPromptHistory], [Server.ClearPromptHistory]),
// the commands tmux has ([Server.ListCommands]),
// access control ([Server.ServerAccess]),
// and layout checks ([Server.ValidateLayouts]).
//
// # Things that need a terminal
//
// [Server.DisplayMenu], [Server.CommandPrompt], and [Server.ConfirmBefore] draw
// on an attached client, and tmux refuses them without one.
// [Server.AttachSession] attaches the caller's own terminal and blocks until it
// detaches.
// [Server.SwitchClient], [Server.DetachClient], [Server.DetachAllClients],
// [Server.LockClient], [Server.SuspendClient], and [Server.RefreshClient] act
// on a client and fail without one;
// [Server.LockServer] locks every attached client.
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
