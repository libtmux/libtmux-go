package tmux

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
}

// SocketSelection returns tmux's -S, -L, TMUX, and default-socket precedence
// snapshotted by [NewServer]. It does not start tmux or require the selected
// socket to exist.
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
