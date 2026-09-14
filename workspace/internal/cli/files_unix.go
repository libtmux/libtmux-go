//go:build unix

package cli

import (
	"os"
	"syscall"
)

// processUmask reports the file-creation mask this process publishes under.
// The system call has no read-only form, so the mask is restored immediately.
func processUmask() os.FileMode {
	mask := syscall.Umask(0)
	syscall.Umask(mask)
	return os.FileMode(mask)
}
