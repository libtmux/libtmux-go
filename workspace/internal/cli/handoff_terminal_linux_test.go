//go:build linux

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"golang.org/x/sys/unix"
)

func TestTerminalRestoreClosedInput(t *testing.T) {
	if os.Getenv("GO_TERMINAL_RESTORE_TEST") == "1" {
		fd, err := unix.FcntlInt(0, unix.F_DUPFD_CLOEXEC, 3)
		if err != nil {
			t.Fatal(err)
		}
		input := os.NewFile(uintptr(fd), "fixture-input")
		defer func() { _ = input.Close() }()
		restore, err := prepareTerminalRestore(input)
		if err != nil {
			t.Fatal(err)
		}
		connection, err := input.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		if err := input.Close(); err != nil {
			t.Fatal(err)
		}
		controlErr := connection.Control(func(uintptr) { t.Error("closed terminal was accessed") })
		if controlErr == nil {
			t.Fatal("closed terminal control succeeded")
		}
		if err := restore(); !errors.Is(err, controlErr) {
			t.Fatalf("restore closed terminal: %v", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	process := tmuxtest.StartPTYProcess(ctx, t, os.Args[0], []string{"-test.run=^TestTerminalRestoreClosedInput$"}, append(os.Environ(), "GO_TERMINAL_RESTORE_TEST=1"))
	if err := process.Wait(ctx); err != nil {
		t.Fatalf("terminal helper: %v %q", err, process.Output())
	}
}

func TestTerminalFailureKeepsPrimaryStatus(t *testing.T) {
	for _, test := range []struct {
		name            string
		primary, output error
		code            string
		exit            int
	}{
		{"restore only", nil, nil, "operation_failed", 1},
		{"child", &failure{"child_failed", "child failed", 9}, nil, "child_failed", 9},
		{"interrupted", &failure{"interrupted", "operation interrupted", 130}, nil, "interrupted", 130},
		{"output", nil, io.ErrClosedPipe, "operation_failed", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			r := &invocation{writeErr: test.output, terminalRestoreErr: os.ErrClosed}
			got := r.commandFailure(test.primary)
			if got == nil || got.Code != test.code || got.Exit != test.exit || !strings.Contains(got.Message, os.ErrClosed.Error()) {
				t.Fatalf("terminal failure: %+v", got)
			}
			primary := test.primary
			if primary == nil {
				primary = test.output
			}
			if primary != nil && (!strings.HasPrefix(got.Message, primary.Error()) || !strings.Contains(got.Message, "; terminal restoration also failed:")) {
				t.Fatalf("primary error lost: %+v", got)
			}
		})
	}
}
