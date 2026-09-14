//go:build !unix

package cli

import (
	"os"
	"runtime"
)

func openAppendFile(string) (*os.File, error) {
	return nil, &failure{"unsupported_log_file", "log files are unsupported on " + runtime.GOOS, 2}
}
