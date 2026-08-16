//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.pytest_plugin.control_mode
//
// libtmux:parity libtmux._internal.control_mode.ControlMode
// libtmux:parity libtmux._internal.control_mode.ControlMode.__enter__
// libtmux:parity libtmux._internal.control_mode.ControlMode.__init__
// libtmux:parity libtmux._internal.control_mode.ControlMode.client_name
// libtmux:parity libtmux._internal.control_mode.ControlMode.stdout
//
//libtmux:real-tmux
func TestControlModeRegistersAndExposesProtocolOutput(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	session := onlyControlSession(t, server)
	control := tmuxtest.NewControlMode(context.Background(), t, server, session)

	if control.ClientName() == "" {
		t.Fatal("ClientName() is empty")
	}
	result := controlCommand(t, server, "list-clients", "-F", "#{client_name}")
	if !slices.Contains(result.Stdout, control.ClientName().String()) {
		t.Fatalf("list-clients stdout = %#v, want %q", result.Stdout, control.ClientName())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	line := readControlLine(ctx, t, control)
	if !strings.HasPrefix(line, "%") {
		t.Fatalf("first control line = %q, want protocol notification", line)
	}
}

// libtmux:parity libtmux._internal.control_mode.ControlMode.server
// libtmux:parity libtmux._internal.control_mode.ControlMode.session
//
//libtmux:real-tmux
func TestControlModeRetainsValidatedServerAndSession(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	session := onlyControlSession(t, server)
	control := tmuxtest.NewControlMode(context.Background(), t, server, session)

	if got := control.Server(); !got.Equal(server) || got.SocketPath() != server.SocketPath() {
		t.Fatalf("Server() = %#v, want validated server %#v", got, server)
	}
	if got := control.Session(); got.ID() != session.ID() || !got.Server().Equal(server) {
		t.Fatalf("Session() = %#v, want validated session %#v", got, session)
	}
}

// libtmux:parity libtmux._internal.control_mode.ControlMode._proc
// libtmux:parity libtmux._internal.control_mode.ControlMode._write_fd
//
//libtmux:real-tmux
func TestControlModeCleanupRemovesClientAndIsIdempotent(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	session := onlyControlSession(t, server)
	var name tmux.ClientName

	t.Run("attached client", func(t *testing.T) {
		control := tmuxtest.NewControlMode(context.Background(), t, server, session)
		name = control.ClientName()
		if err := control.Close(); err != nil {
			t.Fatalf("first Close() error = %v", err)
		}
		if err := control.Close(); err != nil {
			t.Fatalf("second Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		result := controlCommand(t, server, "list-clients", "-F", "#{client_name}")
		if !slices.Contains(result.Stdout, name.String()) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("control client %q remained registered: %v", name, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// libtmux:parity libtmux._internal.control_mode.ControlMode.__exit__
//
//libtmux:real-tmux
func TestControlModeCloseContextCanRetryAfterCancellation(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	session := onlyControlSession(t, server)
	control := tmuxtest.NewControlMode(context.Background(), t, server, session)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := control.CloseContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext(canceled) error = %v, want context canceled", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := control.CloseContext(ctx); err != nil {
		t.Fatalf("CloseContext(retry) error = %v", err)
	}
}

func TestControlModeStartupContextDoesNotOwnClientLifetime(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	startupCtx, cancel := context.WithCancel(context.Background())
	control := tmuxtest.NewControlMode(startupCtx, t, server, onlyControlSession(t, server))
	cancel()

	result := controlCommand(t, server, "list-clients", "-F", "#{client_name}")
	if !slices.Contains(result.Stdout, control.ClientName().String()) {
		t.Fatalf("list-clients stdout = %#v, want live client %q", result.Stdout, control.ClientName())
	}
}

func TestControlModeWaitDoesNotStopClient(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	control := tmuxtest.NewControlMode(context.Background(), t, server, onlyControlSession(t, server))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := control.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, want context deadline exceeded", err)
	}
	result := controlCommand(t, server, "list-clients", "-F", "#{client_name}")
	if !slices.Contains(result.Stdout, control.ClientName().String()) {
		t.Fatalf("list-clients stdout = %#v, want live client %q", result.Stdout, control.ClientName())
	}
}

func TestControlModeReadReturnsClosedAfterClose(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	control := tmuxtest.NewControlMode(context.Background(), t, server, onlyControlSession(t, server))
	if err := control.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	n, err := control.Read(context.Background(), make([]byte, 1))
	if n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Read() after Close() = (%d, %v), want (0, os.ErrClosed)", n, err)
	}
}

func TestControlModeCloseAndWaitAreConcurrent(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	control := tmuxtest.NewControlMode(context.Background(), t, server, onlyControlSession(t, server))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	const callers = 8
	errs := make(chan error, 2*callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(2)
		go func() {
			defer group.Done()
			errs <- control.CloseContext(ctx)
		}()
		go func() {
			defer group.Done()
			errs <- control.Wait(ctx)
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("concurrent CloseContext or Wait error = %v", err)
		}
	}
}

func TestControlModeWaitPreservesProtocolOutputUntilEOF(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	control := tmuxtest.NewControlMode(context.Background(), t, server, onlyControlSession(t, server))
	controlCommand(t, server, "kill-server")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := control.Wait(ctx); errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, control client did not observe server exit", err)
	}
	output := readAllControlOutput(ctx, t, control)
	if !strings.Contains(string(output), "%exit") {
		t.Fatalf("control output after Wait() = %q, want final %%exit notification", output)
	}
}

func onlyControlSession(t *testing.T, server tmux.Server) tmux.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	return sessions[0]
}

func controlCommand(t *testing.T, server tmux.Server, arguments ...string) tmux.CommandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := server.Cmd(ctx, arguments...)
	if err != nil {
		t.Fatalf("tmux %v error = %v", arguments, err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("tmux %v exited %d: %v", arguments, result.ExitCode, result.Stderr)
	}
	return result
}

func readControlLine(
	ctx context.Context,
	t *testing.T,
	control *tmuxtest.ControlMode,
) string {
	t.Helper()
	value, err := bufio.NewReader(controlContextReader{ctx: ctx, control: control}).ReadString('\n')
	if err != nil {
		t.Fatalf("read control stdout: %v", err)
	}
	return value
}

func readAllControlOutput(
	ctx context.Context,
	t *testing.T,
	control *tmuxtest.ControlMode,
) []byte {
	t.Helper()
	value, err := io.ReadAll(controlContextReader{ctx: ctx, control: control})
	if err != nil {
		t.Fatalf("Read() after Wait() error = %v", err)
	}
	return value
}

type controlContextReader struct {
	ctx     context.Context
	control *tmuxtest.ControlMode
}

func (r controlContextReader) Read(data []byte) (int, error) {
	return r.control.Read(r.ctx, data)
}
