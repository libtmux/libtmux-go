//go:build unix

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func promptFileLine(ctx context.Context, input *os.File) (string, error) {
	connection, err := input.SyscallConn()
	if err != nil {
		return "", err
	}
	wake, interrupt, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer func() { _ = wake.Close(); _ = interrupt.Close() }()
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = interrupt.Close()
		close(done)
	})
	defer func() {
		if !stop() {
			<-done
		}
	}()
	var line strings.Builder
	var readErr error
	controlErr := connection.Control(func(fd uintptr) {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}, {Fd: int32(wake.Fd()), Events: unix.POLLIN}}
		var one [1]byte
		for {
			if readErr = ctx.Err(); readErr != nil {
				return
			}
			if _, readErr = unix.Poll(poll, -1); errors.Is(readErr, unix.EINTR) {
				continue
			}
			if readErr != nil {
				return
			}
			if readErr = ctx.Err(); readErr != nil {
				return
			}
			if poll[0].Revents&unix.POLLNVAL != 0 {
				readErr = unix.EBADF
				return
			}
			var n int
			n, readErr = unix.Read(int(fd), one[:])
			if errors.Is(readErr, unix.EINTR) || errors.Is(readErr, unix.EAGAIN) {
				continue
			}
			if readErr != nil {
				return
			}
			if n == 0 {
				readErr = io.EOF
				return
			}
			line.WriteByte(one[0])
			if one[0] == '\n' {
				return
			}
		}
	})
	return line.String(), errors.Join(controlErr, readErr)
}
