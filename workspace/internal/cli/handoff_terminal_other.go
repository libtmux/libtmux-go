//go:build !unix

package cli

import (
	"os"
	"runtime"
)

func prepareTerminalRestore(*os.File) (func() error, error) {
	return nil, &failure{"unsupported_terminal", "terminal state restoration is unsupported on " + runtime.GOOS, 2}
}
