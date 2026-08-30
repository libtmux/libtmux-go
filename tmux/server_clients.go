package tmux

import "context"

var (
	serverClientsVersion33 = Version{raw: "3.3", major: 3, minor: 3}
	// serverClientsClipboardVersion is where refresh-client -l becomes safe to
	// send. tmux has accepted the flag since before the supported range, but
	// 3.2a ends the server on it for any client and 3.3a for a client with a
	// terminal, so the flag is withheld below this rather than gated on the
	// kind of client a caller happens to target.
	serverClientsClipboardVersion = Version{raw: "3.4", major: 3, minor: 4}
)

// ServerAccessRequest configures tmux server access control. Its zero value is
// invalid: List or exactly one nonempty Allow or Deny is required. Allow and
// Deny, and ReadOnly and Write, are mutually exclusive; nil pointers omit
// their selectors while explicit empty users are rejected. List selects the
// returned-list path, but compatible selector and mode flags are still sent to
// tmux rather than ignored.
type ServerAccessRequest struct {
	// Allow grants the named user access.
	Allow *string
	// Deny revokes the named user's access.
	Deny *string
	// List returns the configured access entries instead of mutating them.
	List bool
	// ReadOnly grants read-only access to the selected user.
	ReadOnly bool
	// Write grants write access to the selected user.
	Write bool
}

// RefreshClientRequest configures refreshing one tmux client. Its zero value
// refreshes tmux's current client; a zero TargetClient omits -t.
type RefreshClientRequest struct {
	// TargetClient selects a stable client; zero selects tmux's current client.
	TargetClient ClientName
	// RequestClipboard requests clipboard data and requires tmux 3.4; older tmux
	// versions may exit when given the flag, so it follows UnsupportedPolicy.
	RequestClipboard bool
}

// DetachClientRequest configures detaching one tmux client. Its zero value
// detaches tmux's current client; nil fields omit their flags while an empty
// ShellCommand pointer is explicit.
type DetachClientRequest struct {
	// TargetClient selects a stable client; zero selects tmux's current client.
	TargetClient ClientName
	// ShellCommand is run after detaching when nonnil.
	ShellCommand *string
}

// DetachAllClientsRequest configures detaching every client except one. Its
// zero value keeps tmux's current client; nil fields omit their flags.
type DetachAllClientsRequest struct {
	// KeepClient selects the stable client to retain; zero keeps tmux's current client.
	KeepClient ClientName
	// ShellCommand is run after detaching when nonnil.
	ShellCommand *string
}

// ServerAccess changes or lists the server access-control entries. It requires
// tmux 3.3 and returns [VersionTooLowError] below that floor. List returns an
// owned snapshot, and reports a completed or transport failure rather than
// answering with no entries; mutation completed stderr is an error.
func (s Server) ServerAccess(
	ctx context.Context,
	request ServerAccessRequest,
) ([]string, error) {
	allow, hasAllow := optionalClientString(request.Allow)
	deny, hasDeny := optionalClientString(request.Deny)
	if hasAllow && allow == "" {
		return nil, invalidServerCommandRequest(
			"server-access", "Allow", allow, "must not be empty",
		)
	}
	if hasDeny && deny == "" {
		return nil, invalidServerCommandRequest(
			"server-access", "Deny", deny, "must not be empty",
		)
	}
	if hasAllow {
		if err := validateServerCommandArgument(
			"server-access", "Allow", allow, true,
		); err != nil {
			return nil, err
		}
	}
	if hasDeny {
		if err := validateServerCommandArgument(
			"server-access", "Deny", deny, true,
		); err != nil {
			return nil, err
		}
	}
	if hasAllow && hasDeny {
		return nil, invalidServerCommandRequest(
			"server-access", "Allow", allow, "is mutually exclusive with Deny",
		)
	}
	if request.ReadOnly && request.Write {
		return nil, invalidServerCommandRequest(
			"server-access", "ReadOnly", "true", "is mutually exclusive with Write",
		)
	}
	if !request.List && !hasAllow && !hasDeny {
		return nil, invalidServerCommandRequest(
			"server-access", "User", "", "is required unless List is set",
		)
	}

	current, err := s.Version(ctx)
	if err != nil {
		return nil, err
	}
	if !current.AtLeast(serverClientsVersion33) {
		return nil, &VersionTooLowError{Current: current, Minimum: serverClientsVersion33}
	}

	arguments := []string{"server-access"}
	user := ""
	if hasAllow {
		arguments = append(arguments, "-a")
		user = allow
	}
	if hasDeny {
		arguments = append(arguments, "-d")
		user = deny
	}
	if request.List {
		arguments = append(arguments, "-l")
	}
	if request.ReadOnly {
		arguments = append(arguments, "-r")
	}
	if request.Write {
		arguments = append(arguments, "-w")
	}
	if user != "" {
		arguments = append(arguments, "--", user)
	}
	if request.List {
		return runServerListCommand(ctx, s, arguments)
	}
	return nil, runClientCommand(ctx, s, "server-access", arguments)
}

// LockServer locks every attached client on the tmux server. Cancellation does
// not prove the server did not lock a client.
func (s Server) LockServer(ctx context.Context) error {
	return runClientCommand(ctx, s, "lock-server", []string{"lock-server"})
}

// RefreshClient redraws a client, or tmux's current client when no target is set.
// RequestClipboard requires tmux 3.4 and follows [UnsupportedPolicy].
func (s Server) RefreshClient(ctx context.Context, request RefreshClientRequest) error {
	targetClient, hasTargetClient, err := requestClientTarget(request.TargetClient)
	if err != nil {
		return err
	}
	requestClipboard := false
	if request.RequestClipboard {
		current, versionErr := s.Version(ctx)
		if versionErr != nil {
			return versionErr
		}
		if current.AtLeast(serverClientsClipboardVersion) {
			requestClipboard = true
		} else if err := s.unsupportedFeature(
			"refresh-client",
			"request_clipboard",
			current,
			serverClientsClipboardVersion,
		); err != nil {
			return err
		}
	}
	arguments := []string{"refresh-client"}
	if hasTargetClient {
		arguments = append(arguments, "-t", targetClient)
	}
	if requestClipboard {
		arguments = append(arguments, "-l")
	}
	return runClientCommand(ctx, s, "refresh-client", arguments)
}

// LockClient locks a client, or tmux's current client when targetClient is nil.
// Cancellation does not prove the target client was not locked.
func (s Server) LockClient(ctx context.Context, targetClient *ClientName) error {
	target, hasTarget, err := clientTarget(targetClient)
	if err != nil {
		return err
	}
	arguments := []string{"lock-client"}
	if hasTarget {
		arguments = append(arguments, "-t", target)
	}
	return runClientCommand(ctx, s, "lock-client", arguments)
}

// SuspendClient suspends a client, or tmux's current client when targetClient is nil.
// Cancellation does not prove suspension did not occur.
func (s Server) SuspendClient(ctx context.Context, targetClient *ClientName) error {
	target, hasTarget, err := clientTarget(targetClient)
	if err != nil {
		return err
	}
	arguments := []string{"suspend-client"}
	if hasTarget {
		arguments = append(arguments, "-t", target)
	}
	return runClientCommand(ctx, s, "suspend-client", arguments)
}

// DetachClient detaches one client, or tmux's current client when no target is set.
// A completed stderr result is an error; cancellation does not prove delivery
// or detachment did not occur.
func (s Server) DetachClient(ctx context.Context, request DetachClientRequest) error {
	targetClient, hasTargetClient, err := requestClientTarget(request.TargetClient)
	if err != nil {
		return err
	}
	shellCommand, hasShellCommand := optionalClientString(request.ShellCommand)
	if hasShellCommand {
		if err := validateServerCommandArgument(
			"detach-client", "ShellCommand", shellCommand, true,
		); err != nil {
			return err
		}
	}
	arguments := []string{"detach-client"}
	if hasShellCommand {
		arguments = append(arguments, "-E", shellCommand)
	}
	if hasTargetClient {
		arguments = append(arguments, "-t", targetClient)
	}
	return runClientCommand(ctx, s, "detach-client", arguments)
}

// DetachAllClients detaches every client except KeepClient or tmux's current client.
// Cancellation does not prove every client was detached.
func (s Server) DetachAllClients(
	ctx context.Context,
	request DetachAllClientsRequest,
) error {
	keepClient, hasKeepClient, err := requestClientTarget(request.KeepClient)
	if err != nil {
		return err
	}
	shellCommand, hasShellCommand := optionalClientString(request.ShellCommand)
	if hasShellCommand {
		if err := validateServerCommandArgument(
			"detach-client", "ShellCommand", shellCommand, true,
		); err != nil {
			return err
		}
	}
	arguments := []string{"detach-client", "-a"}
	if hasShellCommand {
		arguments = append(arguments, "-E", shellCommand)
	}
	if hasKeepClient {
		arguments = append(arguments, "-t", keepClient)
	}
	return runClientCommand(ctx, s, "detach-client", arguments)
}

// SwitchClient switches tmux's current client to targetSession. TargetSession
// is a validated session name rather than a stable identity; cancellation does
// not prove the switch did not occur.
func (s Server) SwitchClient(ctx context.Context, targetSession string) error {
	if err := validateLifecycleSessionName("target session", targetSession); err != nil {
		return err
	}
	return runClientCommand(
		ctx,
		s,
		"switch-client",
		[]string{"switch-client", "-t", targetSession},
	)
}

// Lock locks every client attached to the session identified by this handle's
// stable ID. Cancellation does not prove that tmux did not lock a client.
func (s Session) Lock(ctx context.Context) error {
	if err := validateServerCommandArgument(
		"lock-session", "Target", s.sessionID.String(), true,
	); err != nil {
		return err
	}
	if err := validateTypedTarget(
		"lock-session", "Target", "session", s.sessionID.String(),
	); err != nil {
		return err
	}
	return runClientCommand(
		ctx,
		s.server,
		"lock-session",
		[]string{"lock-session", "-t", s.sessionID.String()},
	)
}

// DetachClients detaches every client attached to the session identified by
// this handle's stable ID. A nonnil ShellCommand is run after detaching.
func (s Session) DetachClients(ctx context.Context, shellCommand *string) error {
	if err := validateServerCommandArgument(
		"detach-client", "Target", s.sessionID.String(), true,
	); err != nil {
		return err
	}
	if err := validateTypedTarget(
		"detach-client", "Target", "session", s.sessionID.String(),
	); err != nil {
		return err
	}
	command, hasCommand := optionalClientString(shellCommand)
	if hasCommand {
		if err := validateServerCommandArgument(
			"detach-client", "ShellCommand", command, true,
		); err != nil {
			return err
		}
	}
	arguments := []string{"detach-client"}
	if hasCommand {
		arguments = append(arguments, "-E", command)
	}
	arguments = append(arguments, "-s", s.sessionID.String())
	return runClientCommand(ctx, s.server, "detach-client", arguments)
}

// SwitchClient switches tmux's current client to the session identified by
// this handle's stable ID.
func (s Session) SwitchClient(ctx context.Context) error {
	if err := validateServerCommandArgument(
		"switch-client", "Target", s.sessionID.String(), true,
	); err != nil {
		return err
	}
	if err := validateTypedTarget(
		"switch-client", "Target", "session", s.sessionID.String(),
	); err != nil {
		return err
	}
	return runClientCommand(
		ctx,
		s.server,
		"switch-client",
		[]string{"switch-client", "-t", s.sessionID.String()},
	)
}

func runClientCommand(
	ctx context.Context,
	server Server,
	subcommand string,
	arguments []string,
) error {
	result, err := server.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr(subcommand, result, err)
}

func clientTarget(target *ClientName) (string, bool, error) {
	if target == nil {
		return "", false, nil
	}
	value := target.String()
	if err := validateServerCommandArgument(
		"client", "TargetClient", value, true,
	); err != nil {
		return "", false, err
	}
	if err := validateTypedTarget("client", "TargetClient", "client", value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

func requestClientTarget(target ClientName) (string, bool, error) {
	if target == "" {
		return "", false, nil
	}
	value := target.String()
	if err := validateServerCommandArgument(
		"client", "TargetClient", value, true,
	); err != nil {
		return "", false, err
	}
	if err := validateTypedTarget("client", "TargetClient", "client", value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

func optionalClientString(value *string) (string, bool) {
	if value == nil {
		return "", false
	}
	return *value, true
}
