package tmuxcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// The shortest Unix sockaddr path capacity among supported hosts is 104
// bytes, including its terminating NUL.
const maxControlSocketPathBytes = 103

// PinSocket gives one control startup a private name for the current Unix
// socket inode. The caller must verify the daemon through that name before
// starting control mode and keep it until the connection has been accepted.
func PinSocket(path string) (string, func() error, error) {
	source, err := os.Lstat(path)
	if err != nil {
		return "", nil, fmt.Errorf("inspect control socket: %w", err)
	}
	if source.Mode()&os.ModeSocket == 0 {
		return "", nil, errors.New("tmux: control endpoint is not a Unix socket")
	}
	// A peer alias can exceed sockaddr's limit even when the original is valid.
	// Prefer a short temporary root; a different filesystem needs the peer root.
	parents := []string{os.TempDir()}
	if peer := filepath.Dir(path); peer != parents[0] {
		parents = append(parents, peer)
	}
	var failures []error
	for _, parent := range parents {
		directory, err := os.MkdirTemp(parent, ".lc-")
		if err != nil {
			failures = append(failures, fmt.Errorf("create private control carrier: %w", err))
			continue
		}
		alias := filepath.Join(directory, "s")
		cleanup := sync.OnceValue(func() error {
			aliasErr := os.Remove(alias)
			if errors.Is(aliasErr, os.ErrNotExist) {
				aliasErr = nil
			}
			return errors.Join(aliasErr, os.Remove(directory))
		})
		fail := func(err error) (string, func() error, error) {
			return "", nil, errors.Join(err, cleanup())
		}
		if len(alias) > maxControlSocketPathBytes {
			err = errors.New("tmux: private control socket path exceeds Unix limit")
		} else {
			err = os.Link(path, alias)
		}
		if err != nil {
			if cleanupErr := cleanup(); cleanupErr != nil {
				return "", nil, errors.Join(err, cleanupErr)
			}
			failures = append(failures, fmt.Errorf("pin control socket: %w", err))
			continue
		}
		pinned, err := os.Lstat(alias)
		if err != nil {
			return fail(fmt.Errorf("inspect pinned control socket: %w", err))
		}
		if pinned.Mode()&os.ModeSocket == 0 || !os.SameFile(source, pinned) {
			return fail(errors.New("tmux: control socket changed while pinning"))
		}
		return alias, cleanup, nil
	}
	return "", nil, errors.Join(failures...)
}
