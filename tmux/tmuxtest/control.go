package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

const (
	controlStartTimeout = 3 * time.Second
	controlStopGrace    = 250 * time.Millisecond
)

// ControlMode is an attached tmux control client for real-tmux tests. It owns
// one stdout reader and a temporary-file spool, which preserves unread protocol
// output until [ControlMode.Close] releases it. Instances come from
// [NewControlMode]; do not construct them directly or read concurrently.
type ControlMode struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *controlOutputSpool
	done    chan struct{}

	server     tmux.Server
	session    tmux.Session
	clientName tmux.ClientName
	waitErr    error
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
}

// NewControlMode starts a [ControlMode] attached to session and registers
// cleanup with t. It requires an explicitly configured process environment and
// validates that session belongs to server. The client pins the server's frozen
// effective socket path. Validation or startup failures call [testing.TB.Fatal].
// ctx bounds startup and registration only, not the returned client's lifetime.
func NewControlMode(
	ctx context.Context,
	t testing.TB,
	server tmux.Server,
	session tmux.Session,
) *ControlMode {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, controlStartTimeout)
	defer cancel()
	control, err := startControlMode(ctx, server, session)
	if err != nil {
		t.Fatal(harnessFailure("start control client", err))
	}
	t.Cleanup(func() {
		if err := control.Close(); err != nil {
			t.Error(harnessFailure("cleanup control client", err))
		}
	})
	return control
}

// CloseContext starts an idempotent shutdown and waits within ctx. Concurrent
// calls join the same shutdown. If ctx ends after shutdown starts, shutdown
// continues and a later call may wait for completion; an already-ended ctx does
// not start shutdown.
func (c *ControlMode) CloseContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.closeOnce.Do(func() { go c.close() })
	select {
	case <-c.closeDone:
		return c.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ClientName returns the tmux-assigned identity captured at registration.
func (c *ControlMode) ClientName() tmux.ClientName {
	return c.clientName
}

// Server returns the validated server handle captured at construction.
func (c *ControlMode) Server() tmux.Server {
	return c.server
}

// Session returns the validated session handle captured at construction.
func (c *ControlMode) Session() tmux.Session {
	return c.session
}

// Read reads control-protocol output within ctx. Exactly one caller may read at
// a time. After the client exits, callers may drain preserved output through
// [io.EOF]; after [ControlMode.Close], a non-empty Read returns [os.ErrClosed].
// It returns ctx errors while waiting for output.
func (c *ControlMode) Read(ctx context.Context, data []byte) (int, error) {
	return c.stdout.ReadContext(ctx, data)
}

// Wait blocks until the control client exits or ctx ends. It does not stop or
// close the client; use [ControlMode.Close] to release it.
func (c *ControlMode) Wait(ctx context.Context) error {
	select {
	case <-c.done:
		return c.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the control client and releases its output spool. It is safe to
// call concurrently and more than once.
func (c *ControlMode) Close() error {
	ctx, cancel := context.WithTimeout(
		context.Background(), cleanupTimeout+2*controlStopGrace,
	)
	defer cancel()
	return c.CloseContext(ctx)
}

func (c *ControlMode) close() {
	defer close(c.closeDone)
	defer func() {
		stdoutErr := c.stdout.Close()
		if errors.Is(stdoutErr, os.ErrClosed) {
			stdoutErr = nil
		}
		c.closeErr = errors.Join(c.closeErr, stdoutErr)
	}()
	stdinErr := c.stdin.Close()
	if errors.Is(stdinErr, os.ErrClosed) {
		stdinErr = nil
	}
	select {
	case <-c.done:
		c.closeErr = stdinErr
		return
	case <-time.After(controlStopGrace):
	}

	killErr := c.command.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	select {
	case <-c.done:
		c.closeErr = errors.Join(stdinErr, killErr)
	case <-time.After(cleanupTimeout):
		c.closeErr = errors.Join(
			stdinErr, killErr, errors.New("control client did not exit after kill"),
		)
	}
}

func startControlMode(
	ctx context.Context,
	server tmux.Server,
	session tmux.Session,
) (*ControlMode, error) {
	if err := validateControlTarget(server, session); err != nil {
		return nil, err
	}
	prefix, err := controlCommandPrefix(ctx, server)
	if err != nil {
		return nil, err
	}
	environment, err := controlProcessEnvironment(server)
	if err != nil {
		return nil, err
	}
	arguments := append(
		slices.Clone(prefix),
		"-C", "attach-session", "-t", session.ID().String(),
	)
	command := exec.Command(arguments[0], arguments[1:]...)
	command.WaitDelay = controlStopGrace
	command.Env = environment

	stdout, err := newControlOutputSpool()
	if err != nil {
		return nil, err
	}
	command.Stdout = stdout
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open stdin: %w", err), stdout.Close())
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	control := &ControlMode{
		command:   command,
		stdin:     stdin,
		stdout:    stdout,
		done:      make(chan struct{}),
		closeDone: make(chan struct{}),
		server:    server,
		session:   session,
	}
	if err := command.Start(); err != nil {
		return nil, errors.Join(
			fmt.Errorf("start process: %w", err),
			stdin.Close(),
			stdout.Close(),
		)
	}
	go func() {
		control.waitErr = command.Wait()
		stdout.finish()
		close(control.done)
	}()

	if err := control.waitForRegistration(ctx, server, stderr.String); err != nil {
		closeErr := control.Close()
		return nil, errors.Join(err, closeErr)
	}
	return control, nil
}

func controlProcessEnvironment(server tmux.Server) ([]string, error) {
	environment := server.ProcessEnvironment()
	if environment == nil {
		return nil, errors.New(
			"control mode requires ServerOptions with explicit ProcessEnvironment",
		)
	}
	return scrubTmuxEnvironment(environment), nil
}

func validateControlTarget(server tmux.Server, session tmux.Session) error {
	if session.ID() == "" {
		return errors.New("session id is empty")
	}
	serverSocketPath := server.SocketPath()
	if serverSocketPath == "" {
		return errors.New("server socket path is empty")
	}
	sessionServer := session.Server()
	sessionSocketPath := sessionServer.SocketPath()
	if sessionSocketPath == "" {
		return errors.New("session server socket path is empty")
	}
	if sessionSocketPath != serverSocketPath || !sessionServer.Equal(server) {
		return fmt.Errorf(
			"session server socket path %q does not match server socket path %q",
			sessionSocketPath,
			serverSocketPath,
		)
	}
	return nil
}

func controlCommandPrefix(ctx context.Context, server tmux.Server) ([]string, error) {
	const suffixLength = 3
	result, err := server.Cmd(ctx, "display-message", "-p", "#{pid}")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf(
			"display-message exited %d: %s",
			result.ExitCode,
			strings.Join(result.Stderr, "\n"),
		)
	}
	if len(result.Command) <= suffixLength {
		return nil, fmt.Errorf("display-message command is incomplete: %#v", result.Command)
	}
	prefix := result.Command[:len(result.Command)-suffixLength]
	if len(prefix) == 0 {
		return nil, errors.New("display-message command has no executable")
	}
	path := server.SocketPath()
	if path == "" {
		return nil, errors.New("server socket path is empty")
	}
	// The daemon already exists, so the control process needs neither the
	// startup configuration file nor the original selector spelling. Preserve
	// color capability flags and pin the absolute endpoint selected at server
	// construction.
	command := []string{prefix[0]}
	for _, argument := range prefix[1:] {
		if argument == "-2" || argument == "-8" {
			command = append(command, argument)
		}
	}
	return append(command, "-S"+path), nil
}

func (c *ControlMode) waitForRegistration(
	ctx context.Context,
	server tmux.Server,
	stderr func() string,
) error {
	pid := strconv.Itoa(c.command.Process.Pid)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := server.Cmd(ctx, "list-clients", "-F", "#{client_pid}\t#{client_name}")
		if err != nil {
			return err
		}
		if result.ExitCode == 0 {
			for _, line := range result.Stdout {
				clientPID, clientName, ok := strings.Cut(line, "\t")
				if ok && clientPID == pid && clientName != "" {
					c.clientName = tmux.ClientName(strings.TrimSpace(clientName))
					return nil
				}
			}
		}
		select {
		case <-c.done:
			return controlRegistrationExitError(c.waitErr, stderr())
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func controlRegistrationExitError(waitErr error, stderr string) error {
	if waitErr == nil {
		return fmt.Errorf("process exited before registration; stderr=%q", stderr)
	}
	return fmt.Errorf("process exited before registration: %w; stderr=%q", waitErr, stderr)
}
