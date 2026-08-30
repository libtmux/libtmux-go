package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// ErrInvalidServer identifies an operation on an uninitialized [Server].
var ErrInvalidServer = errors.New("tmux: invalid server handle")

// ErrInvalidServerOptions identifies a value that fails [ServerOptions]
// validation. Executable lookup and working-directory failures retain their
// own error identities.
var ErrInvalidServerOptions = errors.New("tmux: invalid server options")

// ServerOptions configures a [Server] without starting tmux. [NewServer]
// snapshots the effective environment, working directory, and executable;
// callers retain ownership of every supplied value.
type ServerOptions struct {
	// Binary is the tmux executable name or path. Empty resolves tmux once
	// through the snapshotted PATH. Relative paths resolve against the
	// constructor's working directory. The resolved absolute path is shared by
	// subprocess and control-mode transports.
	Binary string
	// SocketName selects tmux's named socket. SocketPath takes precedence.
	SocketName string
	// SocketPath selects an explicit tmux socket path.
	SocketPath string
	// ConfigFile selects an exact tmux configuration file. Empty lets tmux read
	// its default configuration, so a program inherits whatever the user
	// running it has configured: a base-index other than zero, a prompt that
	// appears in captured pane text, hooks that fire on the sessions this
	// program creates. Point it at a file the program owns, which may be an
	// empty one, when the tmux it drives should not depend on that.
	ConfigFile string
	// Colors overrides tmux's terminal color capability.
	Colors ColorMode
	// ProcessEnvironment replaces the child process environment. Nil snapshots
	// the current process environment. A non-nil empty slice supplies no
	// caller-provided variables. Execution may add variables required by Go's
	// target platform and a canonical TMUX_TMPDIR that freezes named or default
	// socket selection. Duplicate names retain their last value. Entries
	// containing NUL are rejected except on Plan 9, where NUL separates path-list
	// elements. NewServer clones the slice.
	ProcessEnvironment []string
	// Unsupported selects what happens when a request needs an optional tmux
	// capability the running server does not have. The zero value refuses the
	// request; see UnsupportedPolicy.
	Unsupported UnsupportedPolicy
	// WarningHandler receives nonfatal compatibility warnings. Nil discards
	// warnings. See WarningHandler for delivery and concurrency semantics.
	WarningHandler WarningHandler
}

type serverConfig struct {
	executable                   string
	directory                    string
	socketName                   string
	socketPath                   string
	configFile                   string
	colors                       ColorMode
	processEnvironment           []string
	configuredProcessEnvironment []string
	socketSelection              SocketSelection
	unsupported                  UnsupportedPolicy
	warningHandler               WarningHandler
}

type serverDependencies struct {
	environ           func() []string
	getwd             func() (string, error)
	resolveExecutable func(string, []string, string) (string, error)
	executor          commandRunner
}

func defaultServerDependencies() serverDependencies {
	return serverDependencies{
		environ:           os.Environ,
		getwd:             os.Getwd,
		resolveExecutable: tmuxcmd.ResolveExecutable,
		executor:          tmuxcmd.Runner{},
	}
}

// NewServer validates and snapshots one tmux configuration without starting
// tmux. Empty socket selectors use the endpoint selected by the frozen
// environment.
func NewServer(options ServerOptions) (Server, error) {
	return newServer(options, defaultServerDependencies())
}

// WithSocketPath returns a server using path and the receiver's frozen
// executable, environment, working directory, and server-option policy. Empty
// clears explicit socket selectors and leaves endpoint selection to tmux. The
// result has fresh daemon-scoped coordination. A connection-bound receiver is
// rejected because its transport belongs to the original daemon.
func (s Server) WithSocketPath(path string) (Server, error) {
	state, err := s.stateForUse()
	if err != nil {
		return Server{}, err
	}
	if err := validateServerCommandArgument("tmux", "SocketPath", path, true); err != nil {
		return Server{}, invalidServerOptions(err)
	}
	if s.connection != nil {
		return Server{}, s.connection.terminalError(commandProcess)
	}
	config := state.config
	config.socketName = ""
	config.socketPath = path
	config.socketSelection.Path = selectedSocketPath(config, config.socketSelection.NamedDirectory)
	freezeNamedSocketEnvironment(&config)
	return Server{state: &serverState{
		config:   config,
		executor: state.executor,
		shared:   &serverShared{},
	}}, nil
}

func newServer(options ServerOptions, dependencies serverDependencies) (Server, error) {
	if err := validateColorMode(options.Colors); err != nil {
		return Server{}, invalidServerOptions(err)
	}
	if err := validateConnectionArguments(options); err != nil {
		return Server{}, invalidServerOptions(err)
	}
	if err := validateUnsupportedPolicy(options.Unsupported); err != nil {
		return Server{}, invalidServerOptions(err)
	}
	if err := validateServerCommandArgument("tmux", "Binary", options.Binary, true); err != nil {
		return Server{}, invalidServerOptions(err)
	}
	if dependencies.environ == nil || dependencies.getwd == nil ||
		dependencies.resolveExecutable == nil || dependencies.executor == nil {
		return Server{}, errors.New("tmux: incomplete constructor dependencies")
	}

	parentEnvironment := slices.Clone(dependencies.environ())
	configuredEnvironment := options.ProcessEnvironment != nil
	environment := options.ProcessEnvironment
	if environment == nil {
		environment = parentEnvironment
	} else {
		environment = slices.Clone(environment)
	}
	environment, err := normalizeProcessEnvironment(environment, parentEnvironment)
	if err != nil {
		return Server{}, err
	}
	cwd, err := dependencies.getwd()
	if err != nil {
		return Server{}, fmt.Errorf("snapshot working directory: %w", err)
	}
	if !filepath.IsAbs(cwd) {
		return Server{}, errors.New("snapshot working directory: path is not absolute")
	}
	binary := options.Binary
	if binary == "" {
		binary = "tmux"
	}
	executable, err := dependencies.resolveExecutable(binary, environment, cwd)
	if err != nil {
		return Server{}, fmt.Errorf("resolve tmux executable %q: %w", binary, err)
	}
	if !filepath.IsAbs(executable) {
		return Server{}, errors.New("resolve tmux executable: resolver returned a relative path")
	}

	config := serverConfig{
		executable:         filepath.Clean(executable),
		directory:          filepath.Clean(cwd),
		socketName:         options.SocketName,
		socketPath:         options.SocketPath,
		configFile:         options.ConfigFile,
		colors:             options.Colors,
		processEnvironment: environment,
		unsupported:        options.Unsupported,
		warningHandler:     options.WarningHandler,
	}
	if configuredEnvironment {
		config.configuredProcessEnvironment = slices.Clone(environment)
	}
	config.socketSelection = resolveSocketSelection(config)
	freezeNamedSocketEnvironment(&config)
	return Server{state: &serverState{
		config:   config,
		executor: dependencies.executor,
		shared:   &serverShared{},
	}}, nil
}

func invalidServerOptions(err error) error {
	if errors.Is(err, ErrInvalidServerOptions) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrInvalidServerOptions, err)
}

func validateUnsupportedPolicy(policy UnsupportedPolicy) error {
	switch policy {
	case FailUnsupported, DegradeUnsupported:
		return nil
	default:
		return fmt.Errorf(
			"%w: invalid Unsupported policy %s",
			ErrInvalidServerOptions,
			policy,
		)
	}
}
