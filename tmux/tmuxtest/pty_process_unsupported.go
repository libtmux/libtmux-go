//go:build !linux

package tmuxtest

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func preparePTYProcessCommand(*exec.Cmd) (*os.File, *os.File, error) {
	return nil, nil, fmt.Errorf("PTY process terminals are unsupported on %s", runtime.GOOS)
}
