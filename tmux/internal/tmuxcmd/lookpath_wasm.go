//go:build wasm

package tmuxcmd

import "os/exec"

func resolveExecutable(name string, _ []string, _ string) (string, error) {
	return "", executableError(name, exec.ErrNotFound)
}
