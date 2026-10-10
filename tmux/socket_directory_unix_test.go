//go:build unix

package tmux

import (
	"os"
	"syscall"
	"testing"
)

func TestNamedSocketDirectoryRejectsForeignUID(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !usableNamedSocketDirectory(info) {
		t.Fatal("rejected an owned private directory")
	}
	// Change only this stat snapshot; testing a foreign owner needs no chown.
	info.Sys().(*syscall.Stat_t).Uid++
	if usableNamedSocketDirectory(info) {
		t.Fatal("accepted a directory owned by another UID")
	}
}
