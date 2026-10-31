package tmux

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// socketNameEnvironmentVariable names a socket for -L when the caller
	// names none.
	socketNameEnvironmentVariable = "LIBTMUX_SOCKET"
	// socketPathEnvironmentVariable names a socket path for -S when the
	// caller names none. It wins over socketNameEnvironmentVariable.
	socketPathEnvironmentVariable = "LIBTMUX_SOCKET_PATH"
)

// SocketSource names what chose the socket a [Server] uses.
type SocketSource string

const (
	// SocketSourceExplicit means the caller named the socket, through
	// ServerOptions, Server.WithSocketPath, or NewServerFromEnv.
	SocketSourceExplicit SocketSource = "explicit"
	// SocketSourceEnvironmentPath means LIBTMUX_SOCKET_PATH chose the socket.
	SocketSourceEnvironmentPath SocketSource = "env-socket-path"
	// SocketSourceEnvironmentName means LIBTMUX_SOCKET chose the socket.
	SocketSourceEnvironmentName SocketSource = "env-socket"
	// SocketSourceTmux means nothing chose a socket, so tmux chooses the
	// server: the one TMUX names, otherwise its default socket.
	SocketSourceTmux SocketSource = "tmux-choice"
)

// SocketSelection describes the Unix filesystem endpoint selected by a
// [Server]. It does not report whether a server is listening there or whether
// tmux would accept the named-socket directory's ownership and permissions.
type SocketSelection struct {
	// Path is the absolute path selected by the frozen socket options,
	// environment, and working directory.
	Path string
	// NamedDirectory is the directory tmux uses for named and default sockets.
	// It can differ from Path's directory when -S or TMUX selects another path.
	NamedDirectory string
	// Source says what chose Path.
	Source SocketSource
}

// SocketSelection returns the socket [NewServer] selected, snapshotted with the
// environment and working directory: an explicit socket, else
// LIBTMUX_SOCKET_PATH, else LIBTMUX_SOCKET, else the server tmux chooses
// itself from TMUX or its default socket. It does not start tmux or require
// the selected socket to exist.
func (s Server) SocketSelection() (SocketSelection, error) {
	state, err := s.stateForUse()
	if err != nil {
		return SocketSelection{}, err
	}
	return state.config.socketSelection, nil
}

func resolveSocketSelection(config serverConfig) SocketSelection {
	namedDirectory := tmuxNamedSocketDirectory(config)
	return SocketSelection{
		Path:           selectedSocketPath(config, namedDirectory),
		NamedDirectory: namedDirectory,
		Source:         config.socketSource,
	}
}

func selectedSocketPath(config serverConfig, namedDirectory string) string {
	path := config.socketPath
	switch {
	case path != "":
	case config.socketName != "":
		path = filepath.Join(namedDirectory, config.socketName)
	default:
		if selected, ok := tmuxEnvironmentSocketPath(config); ok {
			path = selected
		} else {
			path = filepath.Join(namedDirectory, "default")
		}
	}
	return absoluteSocketPath(config.directory, path)
}

func tmuxEnvironmentSocketPath(config serverConfig) (string, bool) {
	value, _ := processEnvironmentValue(config.processEnvironment, "TMUX")
	if value == "" || value[0] == ',' {
		return "", false
	}
	path, _, _ := strings.Cut(value, ",")
	return path, path != ""
}

func freezeNamedSocketEnvironment(config *serverConfig) {
	if config.socketPath != "" {
		return
	}
	if config.socketName == "" {
		if _, selectedByEnvironment := tmuxEnvironmentSocketPath(*config); selectedByEnvironment {
			return
		}
	}
	root := filepath.Dir(config.socketSelection.NamedDirectory)
	config.processEnvironment = setProcessEnvironmentValue(
		config.processEnvironment,
		"TMUX_TMPDIR",
		root,
	)
}

func tmuxNamedSocketDirectory(config serverConfig) string {
	base := ""
	if configured, found := processEnvironmentValue(
		config.processEnvironment,
		"TMUX_TMPDIR",
	); found && configured != "" {
		resolved, err := filepath.EvalSymlinks(
			absoluteSocketPath(config.directory, configured),
		)
		if err == nil {
			base = resolved
		}
	}
	if base == "" {
		base = absoluteSocketPath(config.directory, "/tmp")
		if resolved, err := filepath.EvalSymlinks(base); err == nil {
			base = resolved
		}
	}
	return filepath.Join(base, "tmux-"+strconv.Itoa(os.Getuid()))
}

func absoluteSocketPath(directory, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(directory, path))
}

// environmentSocket returns the socket the process environment selects when the
// caller named none: LIBTMUX_SOCKET_PATH, else LIBTMUX_SOCKET. A variable that
// is empty or only whitespace counts as unset, each one alone, and any other
// value is used as written. Neither variable usable returns SocketSourceTmux.
func environmentSocket(environment []string) (name, path string, source SocketSource) {
	if value, _ := processEnvironmentValue(environment, socketPathEnvironmentVariable); strings.TrimSpace(value) != "" {
		return "", value, SocketSourceEnvironmentPath
	}
	if value, _ := processEnvironmentValue(environment, socketNameEnvironmentVariable); strings.TrimSpace(value) != "" {
		return value, "", SocketSourceEnvironmentName
	}
	return "", "", SocketSourceTmux
}
