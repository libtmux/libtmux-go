//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

func platformSupported() bool {
	return true
}

func shortTempBase() string {
	return "/tmp"
}

// ownedByCaller reports whether info describes a file this process owns.
func ownedByCaller(info fs.FileInfo) bool {
	status, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(status.Uid) == os.Getuid()
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
