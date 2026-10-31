//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package tmuxtest

import "io/fs"

func platformSupported() bool {
	return false
}

func shortTempBase() string {
	return ""
}

func ownedByCaller(fs.FileInfo) bool {
	return true
}

func processAlive(int) bool {
	return false
}
