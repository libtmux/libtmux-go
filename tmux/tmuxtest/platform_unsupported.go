//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package tmuxtest

func platformSupported() bool {
	return false
}

func shortTempBase() string {
	return ""
}

func processAlive(int) bool {
	return false
}
