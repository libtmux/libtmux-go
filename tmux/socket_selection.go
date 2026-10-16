package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func captureSocketDefaults(options *ServerOptions, environment []string) error {
	if options.SocketPath != "" && options.SocketName != "" {
		return fmt.Errorf("%w: SocketPath and SocketName are mutually exclusive", ErrInvalidServerOptions)
	}
	if options.SocketPath == "" && options.SocketName == "" {
		if path, _ := processEnvironmentValue(environment, "LIBTMUX_SOCKET_PATH"); path != "" {
			options.SocketPath = path
		} else if name, _ := processEnvironmentValue(environment, "LIBTMUX_SOCKET_NAME"); name != "" {
			options.SocketName = name
		} else if context, _ := processEnvironmentValue(environment, "TMUX"); context != "" {
			path, err := socketPathFromTmuxContext(context)
			if err != nil {
				return invalidServerOptions(err)
			}
			options.SocketPath = path
		} else {
			options.SocketName = "default"
		}
	}
	if options.SocketPath != "" {
		if !filepath.IsAbs(options.SocketPath) || strings.ContainsRune(options.SocketPath, '\x00') {
			return fmt.Errorf("%w: SocketPath must be absolute and contain no NUL", ErrInvalidServerOptions)
		}
		return nil
	}
	if options.SocketName == "." || options.SocketName == ".." || strings.ContainsAny(options.SocketName, "/\\\x00") {
		return fmt.Errorf("%w: SocketName must be a leaf name other than '.' or '..'", ErrInvalidServerOptions)
	}
	if root, _ := processEnvironmentValue(environment, "TMUX_TMPDIR"); root != "" && !filepath.IsAbs(root) {
		return fmt.Errorf("%w: TMUX_TMPDIR must be absolute", ErrInvalidServerOptions)
	}
	return nil
}

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

// SocketSelection returns the endpoint snapshotted by [NewServer]. It does not
// start tmux or require the selected socket or its parent directory to exist.
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
	if config.socketPath != "" {
		return config.socketPath
	}
	return appendSocketComponent(namedDirectory, config.socketName)
}

func freezeNamedSocketEnvironment(config *serverConfig) {
	if config.socketPath != "" {
		return
	}
	directory := config.socketSelection.NamedDirectory
	separator := strings.LastIndexByte(directory, os.PathSeparator)
	root := directory[:max(separator, 1)]
	config.processEnvironment = setProcessEnvironmentValue(
		config.processEnvironment,
		"TMUX_TMPDIR",
		root,
	)
}

func tmuxNamedSocketDirectory(config serverConfig) string {
	base, _ := processEnvironmentValue(
		config.processEnvironment,
		"TMUX_TMPDIR",
	)
	if base == "" {
		base = "/tmp"
	}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	return appendSocketComponent(base, "tmux-"+strconv.Itoa(os.Getuid()))
}

func appendSocketComponent(parent, leaf string) string {
	// Cleaning before filesystem resolution can erase a missing component or
	// change the destination of a symlink followed by '..'.
	if strings.HasSuffix(parent, string(os.PathSeparator)) {
		return parent + leaf
	}
	return parent + string(os.PathSeparator) + leaf
}

func (config serverConfig) prepareSocketDirectory() error {
	if config.socketName == "" {
		return nil
	}
	return prepareNamedSocketDirectory(config.socketSelection.NamedDirectory)
}
