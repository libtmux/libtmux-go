//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestStartControlModeRequiresExplicitSocketProvenance(t *testing.T) {
	materializedSession := onlyInternalControlSession(t, NewServer(context.Background(), t))
	tests := []struct {
		name    string
		server  tmux.Server
		session tmux.Session
		want    string
	}{
		{
			name:    "server",
			server:  tmux.Server{},
			session: materializedSession,
			want:    "server socket path is empty",
		},
		{
			name:    "session identity",
			server:  tmux.NewServer(tmux.ServerOptions{SocketPath: "/explicit.sock"}),
			session: tmux.Session{},
			want:    "session id is empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			control, err := startControlMode(ctx, test.server, test.session)
			if control != nil {
				_ = control.Close()
				t.Fatal("startControlMode() returned a control client")
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("startControlMode() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestControlRegistrationExitErrorHandlesSuccessfulProcessExit(t *testing.T) {
	t.Parallel()
	err := controlRegistrationExitError(nil, "diagnostic")
	if err == nil || strings.Contains(err.Error(), "%!") {
		t.Fatalf("controlRegistrationExitError(nil) = %v, want a well-formed error", err)
	}
	if !strings.Contains(err.Error(), `stderr="diagnostic"`) {
		t.Fatalf("controlRegistrationExitError(nil) = %v, want stderr diagnostic", err)
	}

	cause := errors.New("wait failed")
	err = controlRegistrationExitError(cause, "")
	if !errors.Is(err, cause) {
		t.Fatalf("controlRegistrationExitError(cause) = %v, want wrapped cause", err)
	}
}

func TestStartControlModeRejectsSessionFromAnotherServer(t *testing.T) {
	server := NewServer(context.Background(), t)
	other := NewServer(context.Background(), t)
	serverSession := onlyInternalControlSession(t, server)
	otherSession := onlyInternalControlSession(t, other)
	if serverSession.ID() != "$0" || otherSession.ID() != "$0" {
		t.Fatalf(
			"session IDs = (%q, %q), want both $0 to exercise cross-server ambiguity",
			serverSession.ID(),
			otherSession.ID(),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlStartTimeout)
	defer cancel()
	control, err := startControlMode(ctx, server, otherSession)
	if control != nil {
		_ = control.Close()
		t.Fatal("startControlMode() accepted a session from another server")
	}
	if err == nil || !strings.Contains(err.Error(), "does not match server socket path") {
		t.Fatalf("startControlMode() error = %v, want socket provenance mismatch", err)
	}
}

func TestControlModeUsesServerProcessEnvironment(t *testing.T) {
	base := NewServer(context.Background(), t)
	session := onlyInternalControlSession(t, base)
	prefix, err := controlCommandPrefix(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefix) == 0 {
		t.Fatal("control command prefix is empty")
	}

	proxyPath := filepath.Join(t.TempDir(), "tmux-environment-proxy")
	proxy := []byte("#!/bin/sh\n" +
		"if [ \"$LIBTMUX_CONTROL_REQUIRED\" != configured ]; then exit 91; fi\n" +
		"exec \"$LIBTMUX_CONTROL_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := append(
		scrubTmuxEnvironment(os.Environ()),
		"LIBTMUX_CONTROL_REQUIRED=configured",
		"LIBTMUX_CONTROL_REAL_TMUX="+prefix[0],
	)
	server := tmux.NewServer(tmux.ServerOptions{
		Binary:             proxyPath,
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: environment,
	})

	ctx, cancel := context.WithTimeout(context.Background(), controlStartTimeout)
	defer cancel()
	control, err := startControlMode(ctx, server, session)
	if err != nil {
		t.Fatalf("startControlMode() error = %v", err)
	}
	if err := control.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestControlModeCloseBoundsInheritedOutputPipes(t *testing.T) {
	base := NewServer(context.Background(), t)
	session := onlyInternalControlSession(t, base)
	prefix, err := controlCommandPrefix(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}

	proxyPath := filepath.Join(t.TempDir(), "tmux-orphan-output-proxy")
	pidPath := filepath.Join(t.TempDir(), "orphan.pid")
	proxy := []byte("#!/bin/sh\n" +
		"for argument do\n" +
		"  if [ \"$argument\" = -C ]; then\n" +
		"    sleep 30 &\n" +
		"    echo \"$!\" > \"$LIBTMUX_CONTROL_ORPHAN_PID\"\n" +
		"    break\n" +
		"  fi\n" +
		"done\n" +
		"exec \"$LIBTMUX_CONTROL_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pidBytes, readErr := os.ReadFile(pidPath)
		if readErr != nil {
			if !errors.Is(readErr, os.ErrNotExist) {
				t.Errorf("read orphan pid: %v", readErr)
			}
			return
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if parseErr != nil {
			t.Errorf("parse orphan pid: %v", parseErr)
			return
		}
		process, findErr := os.FindProcess(pid)
		if findErr != nil {
			t.Errorf("find orphan process: %v", findErr)
			return
		}
		if killErr := process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			t.Errorf("kill orphan process: %v", killErr)
		}
	})

	environment := append(
		scrubTmuxEnvironment(os.Environ()),
		"LIBTMUX_CONTROL_REAL_TMUX="+prefix[0],
		"LIBTMUX_CONTROL_ORPHAN_PID="+pidPath,
	)
	server := tmux.NewServer(tmux.ServerOptions{
		Binary:             proxyPath,
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: environment,
	})

	ctx, cancel := context.WithTimeout(context.Background(), controlStartTimeout)
	defer cancel()
	control, err := startControlMode(ctx, server, session)
	if err != nil {
		t.Fatalf("startControlMode() error = %v", err)
	}
	if err := control.Close(); err != nil {
		t.Fatalf("Close() with inherited output pipe error = %v", err)
	}
	select {
	case <-control.done:
	default:
		t.Fatal("Close() returned before the control wait goroutine finished")
	}
}

func TestControlModeRegistrationBindsEachSubprocessPIDToItsClientName(t *testing.T) {
	server := NewServer(context.Background(), t)
	session := onlyInternalControlSession(t, server)
	controls := []*ControlMode{
		NewControlMode(context.Background(), t, server, session),
		NewControlMode(context.Background(), t, server, session),
	}
	if controls[0].ClientName() == controls[1].ClientName() {
		t.Fatalf("simultaneous client names are both %q, want distinct identities", controls[0].ClientName())
	}

	result := runCommand(
		context.Background(), server, "list-clients", "-F", "#{client_pid}\t#{client_name}",
	)
	if err := commandFailure("list-clients", result); err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) != len(controls) {
		t.Fatalf("list-clients rows = %#v, want exactly two control clients", result.Stdout)
	}
	clientsByPID := make(map[string]string, len(result.Stdout))
	for _, line := range result.Stdout {
		pid, name, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("list-clients row = %q, want PID and name", line)
		}
		clientsByPID[pid] = name
	}
	for _, control := range controls {
		pid := strconv.Itoa(control.command.Process.Pid)
		if got := clientsByPID[pid]; got != control.ClientName().String() {
			t.Fatalf(
				"client for subprocess PID %s = %q, ClientName() = %q",
				pid,
				got,
				control.ClientName(),
			)
		}
	}
}

func TestControlModeWaitThenDrainLargeProtocolOutput(t *testing.T) {
	server := NewServer(context.Background(), t)
	control := NewControlMode(context.Background(), t, server, onlyInternalControlSession(t, server))

	const commandCount = 1024
	payload := strings.Repeat("x", 4096)
	writeErr := make(chan error, 1)
	go func() {
		var err error
		for range commandCount {
			if _, err = io.WriteString(
				control.stdin,
				"display-message -p "+payload+"\n",
			); err != nil {
				break
			}
		}
		writeErr <- errors.Join(err, control.stdin.Close())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := control.Wait(ctx); err != nil {
		t.Fatalf("Wait() before draining large output error = %v", err)
	}
	select {
	case err := <-writeErr:
		if err != nil {
			t.Fatalf("write control commands error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("writing control commands did not finish: %v", ctx.Err())
	}
	output, err := io.ReadAll(controlModeContextReader{ctx: ctx, control: control})
	if err != nil {
		t.Fatalf("Read() after Wait() error = %v", err)
	}
	if got, want := strings.Count(string(output), payload), commandCount; got != want {
		t.Fatalf("payload count = %d, want %d in %d output bytes", got, want, len(output))
	}
}

func TestControlModePreservesUTF8ProtocolOutput(t *testing.T) {
	server := NewServer(context.Background(), t)
	control := NewControlMode(context.Background(), t, server, onlyInternalControlSession(t, server))
	const payload = "control-utf8-café-你好-λ"
	ctx, cancel := context.WithTimeout(context.Background(), controlStartTimeout)
	defer cancel()

	lineResult := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		scanner := bufio.NewScanner(controlModeContextReader{ctx: ctx, control: control})
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, payload) {
				lineResult <- struct {
					line string
					err  error
				}{line: line}
				return
			}
		}
		lineResult <- struct {
			line string
			err  error
		}{err: scanner.Err()}
	}()
	if _, err := io.WriteString(control.stdin, "display-message -p '"+payload+"'\n"); err != nil {
		t.Fatalf("write UTF-8 control command error = %v", err)
	}

	select {
	case result := <-lineResult:
		if result.err != nil {
			t.Fatalf("read UTF-8 protocol output error = %v", result.err)
		}
		if !utf8.ValidString(result.line) || !strings.Contains(result.line, payload) {
			t.Fatalf("protocol line = %q, want valid UTF-8 payload %q", result.line, payload)
		}
	case <-ctx.Done():
		_ = control.Close()
		t.Fatalf("UTF-8 protocol output did not arrive: %v", ctx.Err())
	}
}

func TestControlOutputSpoolReadHonorsContextWhileIdle(t *testing.T) {
	spool, err := newControlOutputSpool()
	if err != nil {
		t.Fatalf("newControlOutputSpool() error = %v", err)
	}
	t.Cleanup(func() {
		if err := spool.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := spool.ReadContext(ctx, make([]byte, 1))
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadContext() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadContext() did not unblock after context cancellation")
	}
}

type controlModeContextReader struct {
	ctx     context.Context
	control *ControlMode
}

func (r controlModeContextReader) Read(data []byte) (int, error) {
	return r.control.Read(r.ctx, data)
}

func TestStartControlModeCleansFailedRegistrationProcessAndSpool(t *testing.T) {
	base := NewServer(context.Background(), t)
	session := onlyInternalControlSession(t, base)
	prefix, err := controlCommandPrefix(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}

	proxyDir := t.TempDir()
	spoolDir := t.TempDir()
	t.Setenv("TMPDIR", spoolDir)
	proxyPath := filepath.Join(proxyDir, "tmux-failed-registration-proxy")
	pidPath := filepath.Join(proxyDir, "failed-registration.pid")
	proxy := []byte("#!/bin/sh\n" +
		"for argument in \"$@\"; do\n" +
		"  if [ \"$argument\" = -C ]; then\n" +
		"    printf '%s' \"$$\" > \"$LIBTMUX_CONTROL_PID_FILE\"\n" +
		"    printf 'registration failed' >&2\n" +
		"    exit 71\n" +
		"  fi\n" +
		"done\n" +
		"exec \"$LIBTMUX_CONTROL_REAL_TMUX\" \"$@\"\n")
	if err := os.WriteFile(proxyPath, proxy, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := append(
		scrubTmuxEnvironment(os.Environ()),
		"LIBTMUX_CONTROL_PID_FILE="+pidPath,
		"LIBTMUX_CONTROL_REAL_TMUX="+prefix[0],
	)
	server := tmux.NewServer(tmux.ServerOptions{
		Binary:             proxyPath,
		SocketPath:         base.SocketPath(),
		ConfigFile:         base.ConfigFile(),
		ProcessEnvironment: environment,
	})

	ctx, cancel := context.WithTimeout(context.Background(), controlStartTimeout)
	defer cancel()
	control, err := startControlMode(ctx, server, session)
	if control != nil {
		_ = control.Close()
		t.Fatal("startControlMode() returned a control client after registration failure")
	}
	if err == nil || !strings.Contains(err.Error(), "registration failed") {
		t.Fatalf("startControlMode() error = %v, want registration diagnostic", err)
	}
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read failed-registration pid error = %v", err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatalf("parse failed-registration pid %q: %v", pidBytes, err)
	}
	if processAlive(pid) {
		t.Fatalf("failed-registration process %d remains alive", pid)
	}
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "libtmux-control-output-") {
			t.Fatalf("failed-registration output spool remains: %q", entry.Name())
		}
	}
}

func TestControlModeCloseRemovesOutputSpool(t *testing.T) {
	server := NewServer(context.Background(), t)
	control := NewControlMode(context.Background(), t, server, onlyInternalControlSession(t, server))
	spoolPath := control.stdout.path
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("stat live output spool error = %v", err)
	}

	if err := control.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(spoolPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat closed output spool error = %v, want not exist", err)
	}
}

func onlyInternalControlSession(t *testing.T, server tmux.Server) tmux.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), controlStartTimeout)
	defer cancel()
	sessions, err := server.WithStrictErrors().Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want 1", len(sessions))
	}
	return sessions[0]
}
