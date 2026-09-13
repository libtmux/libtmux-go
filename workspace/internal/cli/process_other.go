//go:build !unix

package cli

import "os/exec"

func superviseProcess(_ *exec.Cmd) {}
