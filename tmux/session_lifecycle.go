package tmux

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrSessionExists identifies a named session that was not replaced.
var ErrSessionExists = errors.New("tmux: session already exists")

// HasSessionRequest selects exact or tmux-pattern session matching on tmux
// 3.2a or later. Its zero value is invalid because Target is required.
type HasSessionRequest struct {
	// Target is the required session name or tmux session pattern.
	Target string
	// Pattern lets tmux interpret Target as a pattern; false requires an exact
	// session name.
	Pattern bool
}

// NewSessionRequest configures session creation. [Server.NewSession] creates a
// detached session; [Server.NewSessionConnection] creates one attached to its
// first control-mode lane. Its zero value uses tmux defaults. Width and Height
// are either zero or 1 through 65535; KillExisting requires Name. KillExisting
// may remove a session before a later creation failure.
//
// [Server.NewSession] copies Environment before validation and retains no map
// storage; concurrent mutation during the copy is unsafe. Foreground-only tmux
// flags are not exposed.
type NewSessionRequest struct {
	// Name selects the new session name; empty lets tmux generate one.
	Name string
	// KillExisting removes an existing session named Name before creation.
	KillExisting bool
	// StartDirectory expands ~ and ~/... for the current user. Named-user
	// forms such as ~other are rejected; empty inherits tmux's default.
	StartDirectory string
	// WindowName names the initial window; empty lets tmux choose.
	WindowName string
	// Width sets the detached session width; zero lets tmux choose.
	Width int
	// Height sets the detached session height; zero lets tmux choose.
	Height int
	// Environment is emitted in lexically sorted key order. The map is not
	// retained; nil and an empty map both add no entries.
	Environment map[string]string
	// Command starts the initial pane with this shell command; empty uses tmux's
	// default command.
	Command string
}

// newSessionArguments renders one new-session argument vector. It performs no
// tmux I/O, so a [Plan] can render a session it has not created. The existence
// probe KillExisting needs is the caller's, not this.
func newSessionArguments(request NewSessionRequest) ([]string, error) {
	return renderNewSessionArguments(request, "#{session_id}", true, "")
}

func renderNewSessionArguments(
	request NewSessionRequest,
	format string,
	detached bool,
	clientFlags string,
) ([]string, error) {
	if request.Width < 0 || request.Width > 65535 {
		return nil, invalidLifecycleRequest("Width must be between 1 and 65535")
	}
	if request.Height < 0 || request.Height > 65535 {
		return nil, invalidLifecycleRequest("Height must be between 1 and 65535")
	}
	if request.Name != "" {
		if err := validateLifecycleSessionName("name", request.Name); err != nil {
			return nil, err
		}
	}
	environment, err := lifecycleEnvironmentArguments(request.Environment)
	if err != nil {
		return nil, err
	}
	startDirectory, err := expandLifecycleDirectory(request.StartDirectory)
	if err != nil {
		return nil, err
	}

	arguments := []string{"new-session", "-P", "-F" + format}
	if clientFlags != "" {
		arguments = append(arguments, "-f"+clientFlags)
	}
	if request.Name != "" {
		arguments = append(arguments, "-s"+request.Name)
	}
	if detached {
		arguments = append(arguments, "-d")
	}
	if startDirectory != "" {
		arguments = append(arguments, "-c", startDirectory)
	}
	if request.WindowName != "" {
		arguments = append(arguments, "-n", request.WindowName)
	}
	if request.Width != 0 {
		arguments = append(arguments, "-x", strconv.Itoa(request.Width))
	}
	if request.Height != 0 {
		arguments = append(arguments, "-y", strconv.Itoa(request.Height))
	}
	arguments = append(arguments, environment...)
	if request.Command != "" {
		arguments = append(arguments, request.Command)
	}
	return arguments, nil
}

// HasSession reports whether Target selects a session. With Pattern false it
// compares session names exactly; with Pattern true, tmux may resolve IDs,
// client TTYs, unique prefixes, or globs. Pattern-mode completed nonzero exits
// are misses. Exact-name lookup treats [ErrNoServer] as a miss and returns
// other failures.
//
// Exact-name lookup lists sessions because tmux's exact-match marker still
// resolves identifiers and client TTYs.
func (s Server) HasSession(ctx context.Context, request HasSessionRequest) (bool, error) {
	if err := validateServerCommandArgument(
		"has-session", "Target", request.Target, true,
	); err != nil {
		return false, err
	}
	if err := validateLifecycleSessionName("target", request.Target); err != nil {
		return false, err
	}
	if !request.Pattern {
		identifier, err := s.sessionNamed(ctx, request.Target)
		return identifier != "", err
	}
	result, err := s.literalCmd(ctx, "has-session", "-t", request.Target)
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

// sessionNamed returns the identifier of the session carrying exactly this
// name, or an empty identifier when no session does. A server that is not
// running holds no session by any name, which is a miss rather than a failure.
//
// The identifier leads the format so that the split point is unambiguous: a
// name may hold a space, and every identifier is one token.
func (s Server) sessionNamed(ctx context.Context, name string) (SessionID, error) {
	rows, err := s.literalCmd(ctx, "list-sessions", "-F", "#{session_id} #{session_name}")
	if err != nil {
		return "", err
	}
	if rows.ExitCode != 0 {
		commandErr := newCommandError("list-sessions", rows)
		if errors.Is(commandErr, ErrNoServer) {
			return "", nil
		}
		return "", commandErr
	}
	for _, row := range rows.Stdout {
		identifier, found, ok := strings.Cut(row, " ")
		if ok && found == name {
			return SessionID(identifier), nil
		}
	}
	return "", nil
}

// NewSession creates a detached session, then returns a newly materialized
// [Session]. It never changes client focus. KillExisting is not rolled back;
// transport and context failures may be delivery-ambiguous.
//
// The returned record has unmaterialized [Session.Windows] and [Session.Panes]
// relations. Use the resolvers or [Server.Snapshot] when relations are needed.
//
// If tmux reports an ID before a transport or refresh failure, the returned
// partial Session contains its [Server] and [SessionID]. Other failures return zero.
func (s Server) NewSession(ctx context.Context, request NewSessionRequest) (Session, error) {
	request = captureNewSessionRequest(request)
	if err := validateServerCommandArguments(
		"new-session",
		serverCommandArgument{field: "Name", value: request.Name},
		serverCommandArgument{field: "StartDirectory", value: request.StartDirectory},
		serverCommandArgument{field: "WindowName", value: request.WindowName},
		serverCommandArgument{field: "Command", value: request.Command},
	); err != nil {
		return Session{}, err
	}
	if request.KillExisting && request.Name == "" {
		return Session{}, invalidLifecycleRequest("KillExisting requires Name")
	}
	if request.Width < 0 || request.Width > 65535 {
		return Session{}, invalidLifecycleRequest("Width must be between 1 and 65535")
	}
	if request.Height < 0 || request.Height > 65535 {
		return Session{}, invalidLifecycleRequest("Height must be between 1 and 65535")
	}
	effective, err := newSessionCommandServer(s)
	if err != nil {
		return Session{}, err
	}
	if request.Name != "" {
		if err := validateLifecycleSessionName("name", request.Name); err != nil {
			return Session{}, err
		}
		// The session is killed by identifier rather than by the name that
		// found it, because tmux would resolve that name again through its own
		// ladder and can land on a different session.
		existing, err := effective.sessionNamed(ctx, request.Name)
		if err != nil {
			return Session{}, err
		}
		if existing != "" && !request.KillExisting {
			return Session{}, fmt.Errorf("%w: %q", ErrSessionExists, request.Name)
		}
		if existing != "" {
			result, err := effective.literalCmd(ctx, "kill-session", "-t", existing.String())
			if _, err = requireRedactedLifecycleSuccess("kill-session", result, err); err != nil {
				return Session{}, err
			}
		}
	}

	arguments, err := newSessionArguments(request)
	if err != nil {
		return Session{}, err
	}
	result, err := effective.literalCmd(ctx, arguments...)
	if err != nil {
		if identity, identityErr := lifecycleStableIdentity("session", result.Stdout); identityErr == nil {
			return Session{server: effective, sessionID: SessionID(identity)}, err
		}
		return Session{}, err
	}
	result, err = requireRedactedLifecycleSuccess("new-session", result, nil)
	if err != nil {
		return Session{}, err
	}
	identity, err := lifecycleStableIdentity("session", result.Stdout)
	if err != nil {
		return Session{}, err
	}
	session, err := effective.Session(ctx, SessionID(identity))
	if err != nil {
		return Session{server: effective, sessionID: SessionID(identity)}, err
	}
	return session, nil
}

// Rename changes the receiver session's name and returns a canonical freshly
// materialized [Session]. If the command succeeds but refresh fails, it
// returns the receiver with that error. A transport or context error can be
// delivery-ambiguous; no rollback is attempted. Invalid names are rejected
// before execution and match [ErrInvalidRequest].
func (s Session) Rename(ctx context.Context, name string) (Session, error) {
	if err := validateServerCommandArguments(
		"rename-session",
		serverCommandArgument{field: "Target", value: s.sessionID.String()},
		serverCommandArgument{field: "Name", value: name},
	); err != nil {
		return Session{}, err
	}
	if err := validateLifecycleSessionName("name", name); err != nil {
		return Session{}, err
	}
	result, err := s.literalCmd(ctx, "rename-session", name)
	if _, err = requireLifecycleSuccess("rename-session", result, err); err != nil {
		return Session{}, err
	}
	refreshed, err := s.Refresh(ctx)
	if err != nil {
		return s, err
	}
	return refreshed, nil
}

func validateLifecycleSessionName(field, name string) error {
	if name == "" {
		return invalidLifecycleRequest(field + " must not be empty")
	}
	if strings.ContainsAny(name, ".:") {
		return invalidLifecycleRequest(field + " must not contain periods or colons")
	}
	// A control byte is refused for the same reason a delimiter is: to make one
	// name mean one thing on every supported release. tmux rejects these from
	// 3.7; before that it accepts them and stores the name visibility-encoded,
	// so a bell arrives as a backslash and an "a" and the session ends up under
	// a name nobody asked for.
	if !utf8.ValidString(name) {
		return invalidLifecycleRequest(field + " must be valid UTF-8")
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return invalidLifecycleRequest(
				field + " must not contain control characters")
		}
	}
	return nil
}

// ValidateSessionName reports whether name can be used as a tmux session
// name. Empty names, the target delimiters '.' and ':', control characters and
// invalid UTF-8 are rejected.
func ValidateSessionName(name string) error {
	return validateLifecycleSessionName("name", name)
}

func captureNewSessionRequest(request NewSessionRequest) NewSessionRequest {
	request.Environment = maps.Clone(request.Environment)
	return request
}

func newSessionCommandServer(server Server) (Server, error) {
	state, err := server.stateForUse()
	if err != nil {
		return Server{}, err
	}
	config := state.config
	if config.socketPath == "" && config.socketName == "" {
		if _, selectedByEnvironment := tmuxEnvironmentSocketPath(config); selectedByEnvironment {
			config.socketPath = config.socketSelection.Path
		}
	}
	environment := slices.Clone(config.processEnvironment)
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if processEnvironmentKey(name) != processEnvironmentKey("TMUX") {
			filtered = append(filtered, entry)
		}
	}
	config.processEnvironment = filtered
	configured := slices.Clone(config.configuredProcessEnvironment)
	configured = slices.DeleteFunc(configured, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return processEnvironmentKey(name) == processEnvironmentKey("TMUX")
	})
	config.configuredProcessEnvironment = configured
	return Server{
		state: &serverState{
			config:   config,
			executor: state.executor,
			// Different options, the same tmux: the version it reports and the
			// pools open on it are properties of the server, not of whether
			// TMUX was removed from the environment reaching it.
			shared: state.shared,
		},
		connection:      server.connection,
		daemon:          server.daemon,
		requiresProcess: server.requiresProcess,
	}, nil
}
