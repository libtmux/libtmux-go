//go:build unix

package tmux

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func prepareNamedSocketDirectory(directory string) error {
	// -S bypasses tmux's named-directory preparation. Mkdir must not create a
	// missing selected root, and Lstat must reject a per-UID symlink.
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("prepare tmux socket directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect tmux socket directory: %w", err)
	}
	if !usableNamedSocketDirectory(info) {
		return fmt.Errorf("prepare tmux socket directory %q: require a real directory owned by uid %d without other-user permissions", directory, os.Getuid())
	}
	return nil
}

func usableNamedSocketDirectory(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return info.IsDir() && ok && stat.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0o007 == 0
}
