//go:build windows

package main

import "os/exec"

// Native tmux is unsupported on Windows. CommandContext still owns the direct
// process there; descendant ownership would require a suspended Job launcher.
func ownPreflightProcess(_ *exec.Cmd) func() error {
	return func() error { return nil }
}
