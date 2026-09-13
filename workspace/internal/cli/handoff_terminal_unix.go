//go:build unix

package cli

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func prepareTerminalRestore(input *os.File) (func() error, error) {
	connection, err := input.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("capture terminal state: %w", err)
	}
	var state *term.State
	var flags int
	var captureErr error
	controlErr := connection.Control(func(fd uintptr) {
		flags, captureErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
		if captureErr == nil {
			state, captureErr = term.GetState(int(fd))
		}
	})
	if err := errors.Join(controlErr, captureErr); err != nil {
		return nil, fmt.Errorf("capture terminal state: %w", err)
	}
	return func() error {
		var stateErr, flagsErr error
		controlErr := connection.Control(func(fd uintptr) {
			stateErr = term.Restore(int(fd), state)
			_, flagsErr = unix.FcntlInt(fd, unix.F_SETFL, flags)
		})
		if err := errors.Join(controlErr, stateErr, flagsErr); err != nil {
			return fmt.Errorf("restore terminal state: %w", err)
		}
		return nil
	}, nil
}
