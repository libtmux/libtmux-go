package tmux

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	controlClientStopGrace   = 250 * time.Millisecond
	controlClientStopTimeout = 2 * time.Second
	controlRegistrationPoll  = 10 * time.Millisecond
)

type controlNotificationMode uint8

const (
	controlNotificationsRetained controlNotificationMode = iota
	controlNotificationsDiscarded
)

// ErrControlClosed identifies an operation attempted after a control client
// began closing or lost its protocol stream.
var ErrControlClosed = errors.New("tmux: control client is closed")

// ControlClient is one attached tmux control-mode process. Create one with
// [Server.OpenControl]. Concurrent Cmd, Wait, and close calls are supported;
// exactly one caller may execute NextNotification at a time.
type ControlClient struct {
	server     Server
	session    Session
	clientName ClientName

	command       *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	stderr        *controlLockedBuffer
	notifications *controlNotificationQueue
	frames        chan controlFrame
	requests      chan controlRequest
	stopRequests  chan struct{}
	requestDone   chan struct{}
	closing       chan struct{}
	readDone      chan struct{}
	done          chan struct{}
	closeDone     chan struct{}

	stateMu sync.Mutex
	readErr error
	waitErr error

	closeRequested atomic.Bool
	closeOnce      sync.Once
	closeErr       error

	// dispatching excludes the attach frame before matching later command replies.
	dispatching atomic.Bool
}

// ownReply uses tmux's CMDQ_STATE_CONTROL guard flag. Treating a key-binding
// command as our reply would shift every later reply on the connection.
func (f controlFrame) ownReply() bool {
	return f.flags != 0
}

type controlRequest struct {
	ctx context.Context
	// command is the request's original argument vector.
	command []string
	// commands is how many tmux commands line carries. tmux answers a command
	// list with one frame per command, so this is how many replies to expect.
	commands int
	line     string
	response chan controlResponse
}

type controlResponse struct {
	results []ControlCommandResult
	err     error
}

type controlLockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *controlLockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func (b *controlLockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

// OpenControl starts a control-mode client attached to session. The startup
// context bounds process start, attach framing, and client registration but
// does not own the returned client's lifetime. Session must belong to the same
// configured server selector.
func (s Server) OpenControl(
	ctx context.Context,
	session Session,
) (*ControlClient, error) {
	return s.openControl(ctx, session, controlNotificationsRetained)
}

func (s Server) openControl(
	ctx context.Context,
	session Session,
	mode controlNotificationMode,
) (*ControlClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := s.connectionState()
	if err := validateColorMode(state.options.Colors); err != nil {
		return nil, err
	}
	if err := validateConnectionArguments(state.options); err != nil {
		return nil, err
	}
	if session.ID() == "" {
		return nil, invalidServerCommandRequest(
			"control", "Session", "", "must have a materialized session ID",
		)
	}
	if !session.Server().Equal(s) {
		return nil, invalidServerCommandRequest(
			"control", "Session", "[redacted]", "belongs to a different server selector",
		)
	}

	var notifications *controlNotificationQueue
	if mode == controlNotificationsRetained {
		notifications = newControlNotificationQueue(defaultControlNotificationLimit)
	}
	binary := state.options.Binary
	if binary == "" {
		binary = "tmux"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("resolve tmux executable %q: %w", binary, err),
			notifications.Close(),
		)
	}
	attach := []string{"-C", "attach-session"}
	if mode == controlNotificationsDiscarded {
		attach = append(attach, "-f", "no-output")
	}
	attach = append(attach, "-t", session.ID().String())
	arguments := s.commandArguments(attach)
	command := exec.Command(resolved, arguments...)
	command.Env = state.options.ProcessEnvironment
	command.WaitDelay = controlClientStopGrace
	stderr := &controlLockedBuffer{}
	command.Stderr = stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open control stdin: %w", err),
			notifications.Close(),
		)
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open control stdout: %w", err),
			stdin.Close(),
			notifications.Close(),
		)
	}
	command.Stdout = stdoutWriter
	client := &ControlClient{
		server:        s,
		session:       session,
		command:       command,
		stdin:         stdin,
		stdout:        stdout,
		stderr:        stderr,
		notifications: notifications,
		frames:        make(chan controlFrame, 1),
		requests:      make(chan controlRequest),
		stopRequests:  make(chan struct{}),
		requestDone:   make(chan struct{}),
		closing:       make(chan struct{}),
		readDone:      make(chan struct{}),
		done:          make(chan struct{}),
		closeDone:     make(chan struct{}),
	}
	if err := command.Start(); err != nil {
		return nil, errors.Join(
			fmt.Errorf("start control process: %w", err),
			stdin.Close(),
			stdout.Close(),
			stdoutWriter.Close(),
			notifications.Close(),
		)
	}
	if err := stdoutWriter.Close(); err != nil {
		killErr := command.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		return nil, errors.Join(
			fmt.Errorf("close parent control stdout writer: %w", err),
			killErr,
			command.Wait(),
			stdin.Close(),
			stdout.Close(),
			notifications.Close(),
		)
	}
	go client.waitProcess()
	go client.readStream()

	frame, err := client.nextFrame(ctx)
	if err != nil {
		return nil, client.failStartup(err)
	}
	if frame.failed {
		return nil, client.failStartup(fmt.Errorf(
			"control attach failed: %s",
			strings.TrimSpace(string(frame.rawStdout)),
		))
	}
	clientName, err := client.waitForRegistration(ctx)
	if err != nil {
		return nil, client.failStartup(err)
	}
	client.clientName = clientName
	client.dispatching.Store(true)
	go client.runRequests()
	return client, nil
}

// Cmd executes one safely encoded tmux command through the control client.
// A %error frame is returned as ControlCommandResult with Failed set. If ctx
// ends after the command is written, Cmd returns the context error while the
// client drains that reply before writing a later command. Closing rejects an
// unaccepted request and gives an accepted request a bounded drain window.
func (c *ControlClient) Cmd(
	ctx context.Context,
	args ...string,
) (ControlCommandResult, error) {
	results, err := c.cmd(ctx, false, args...)
	if err != nil || len(results) == 0 {
		return ControlCommandResult{}, err
	}
	return results[0], nil
}

// cmd returns one result per executed command in a command list. A failed list
// includes the failure and omits commands tmux dropped after it.
func (c *ControlClient) cmd(
	ctx context.Context,
	commandList bool,
	args ...string,
) ([]ControlCommandResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := slices.Clone(args)
	line, err := encodeControlCommand(command, commandList)
	if err != nil {
		return nil, err
	}
	request := controlRequest{
		ctx:      ctx,
		command:  command,
		commands: countCommandListCommands(command, commandList),
		line:     line,
		response: make(chan controlResponse, 1),
	}
	if c.closeRequested.Load() {
		return nil, ErrControlClosed
	}
	select {
	case c.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.requestDone:
		return nil, c.operationError()
	case <-c.stopRequests:
		return nil, ErrControlClosed
	}
	select {
	case response := <-request.response:
		return response.results, response.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func countCommandListCommands(arguments []string, commandList bool) int {
	if !commandList {
		return 1
	}
	commands := 1
	for _, argument := range arguments {
		if argument == ";" {
			commands++
		}
	}
	return commands
}

// NextNotification returns the next ordered control-mode notification. Exactly
// one caller may execute it at a time. Natural process exit preserves queued
// notifications until they drain through io.EOF; Close releases the queue and
// makes subsequent reads report os.ErrClosed. A terminal reader error follows
// notifications queued before that failure. A full bounded queue likewise
// drains before reporting [ControlNotificationOverflowError].
func (c *ControlClient) NextNotification(
	ctx context.Context,
) (ControlNotification, error) {
	record, err := c.notifications.next(ctx)
	if err != nil {
		return ControlNotification{}, err
	}
	return ParseControlNotification(record)
}

// Notifications returns an iterator over what tmux says without being asked:
// pane output, and the events behind [ControlNotification].
//
// It is [ControlClient.NextNotification] as a range loop; exactly one iterator
// or direct notification read may run at a time.
//
//	for notification, err := range client.Notifications(ctx) {
//		if err != nil {
//			return err
//		}
//		if pane, output, ok := notification.Output(); ok {
//			handle(pane, output)
//		}
//	}
//
// Malformed or unknown notifications yield their error and iteration continues.
// Every other error ends the stream after being yielded.
//
// Natural tmux exit drains queued notifications and then ends without error.
//
// Leaving early preserves queued notifications for the next read.
func (c *ControlClient) Notifications(
	ctx context.Context,
) iter.Seq2[ControlNotification, error] {
	return func(yield func(ControlNotification, error) bool) {
		for {
			notification, err := c.NextNotification(ctx)
			if errors.Is(err, io.EOF) {
				return
			}
			if !yield(notification, err) {
				return
			}
			var unreadable *ControlNotificationError
			if err != nil && !errors.As(err, &unreadable) {
				return
			}
		}
	}
}

// ClientName returns the tmux-assigned identity captured during registration.
func (c *ControlClient) ClientName() ClientName { return c.clientName }

// Server returns the server handle used to start the control client.
func (c *ControlClient) Server() Server { return c.server }

// Session returns the materialized session selected during startup.
func (c *ControlClient) Session() Session { return c.session }

// Wait blocks until the control process exits or ctx ends. It does not close
// the notification queue; callers may drain final notifications before Close.
func (c *ControlClient) Wait(ctx context.Context) error {
	select {
	case <-c.done:
		c.stateMu.Lock()
		defer c.stateMu.Unlock()
		return c.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CloseContext starts idempotent control-client shutdown and waits within ctx.
// An already-ended context does not start shutdown; shutdown continues after a
// context that ends while waiting, so a later call may retry the wait. Shutdown
// rejects unaccepted commands and gives an accepted frame a bounded drain
// window before process-stop escalation.
func (c *ControlClient) CloseContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.closeOnce.Do(func() {
		c.closeRequested.Store(true)
		close(c.stopRequests)
		go c.closeAfterRequests()
	})
	select {
	case <-c.closeDone:
		return c.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the control process and releases its notification queue. It is
// safe to call concurrently and more than once.
func (c *ControlClient) Close() error {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		controlClientStopTimeout+2*controlClientStopGrace,
	)
	defer cancel()
	return c.CloseContext(ctx)
}

// Reconnect closes the receiver and starts a new control client for the same
// server and session. It returns a new identity and never replays commands.
func (c *ControlClient) Reconnect(ctx context.Context) (*ControlClient, error) {
	if err := c.CloseContext(ctx); err != nil {
		return nil, err
	}
	return c.server.OpenControl(ctx, c.session)
}

func (c *ControlClient) runRequests() {
	defer close(c.requestDone)
	for {
		select {
		case request := <-c.requests:
			if c.closeRequested.Load() {
				request.response <- controlResponse{err: ErrControlClosed}
				return
			}
			response, keepRunning := c.executeControlRequest(request)
			request.response <- response
			if !keepRunning {
				return
			}
		case <-c.stopRequests:
			return
		}
	}
}

func (c *ControlClient) executeControlRequest(
	request controlRequest,
) (controlResponse, bool) {
	if err := request.ctx.Err(); err != nil {
		return controlResponse{err: err}, true
	}
	if _, err := io.WriteString(c.stdin, request.line+"\n"); err != nil {
		return controlResponse{err: c.classifyOperationError(err)}, false
	}
	// A list ends at its first %error or after one %end per command. Reading too
	// few frames skews the next reply; waiting for a dropped command hangs.
	results := make([]ControlCommandResult, 0, request.commands)
	for range request.commands {
		frame, err := c.nextFrame(context.Background())
		if err != nil {
			return controlResponse{err: err}, false
		}
		results = append(results, frame.result(request.command))
		if frame.failed {
			break
		}
	}
	return controlResponse{results: results}, true
}

func (c *ControlClient) closeAfterRequests() {
	timer := time.NewTimer(controlClientStopGrace)
	defer timer.Stop()
	select {
	case <-c.requestDone:
	case <-timer.C:
	}
	close(c.closing)
	c.close()
}

func (c *ControlClient) readStream() {
	var finalErr error
	parser := controlStreamParser{}
	reader := bufio.NewReader(c.stdout)
	defer func() {
		if finalErr == nil && !c.isClosing() {
			finalErr = parser.finish()
		}
		c.stateMu.Lock()
		c.readErr = finalErr
		c.stateMu.Unlock()
		c.notifications.finish(finalErr)
		close(c.frames)
		close(c.readDone)
		if finalErr != nil && !c.isClosing() {
			_ = c.command.Process.Kill()
		}
	}()

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) != 0 {
			if line[len(line)-1] != '\n' {
				finalErr = controlProtocolError("stream", "record ended without LF")
				return
			}
			line = line[:len(line)-1]
			frame, notification, parseErr := parser.consume(line)
			if parseErr != nil {
				finalErr = parseErr
				return
			}
			if notification != nil {
				if appendErr := c.notifications.append(notification); appendErr != nil {
					if errors.Is(appendErr, ErrControlNotificationOverflow) {
						continue
					}
					if c.isClosing() && errors.Is(appendErr, os.ErrClosed) {
						return
					}
					finalErr = appendErr
					return
				}
			}
			if frame != nil {
				// Somebody else's block. Dropping it is what keeps this
				// client's replies matched to its own commands.
				if c.dispatching.Load() && !frame.ownReply() {
					continue
				}
				select {
				case c.frames <- *frame:
				case <-c.closing:
					return
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || c.isClosing() {
				return
			}
			finalErr = fmt.Errorf("read control stream: %w", err)
			return
		}
	}
}

func (c *ControlClient) nextFrame(ctx context.Context) (controlFrame, error) {
	select {
	case frame, ok := <-c.frames:
		if !ok {
			return controlFrame{}, c.operationError()
		}
		return frame, nil
	case <-ctx.Done():
		return controlFrame{}, ctx.Err()
	case <-c.closing:
		return controlFrame{}, ErrControlClosed
	}
}

func (c *ControlClient) waitProcess() {
	err := c.command.Wait()
	c.stateMu.Lock()
	c.waitErr = err
	c.stateMu.Unlock()
	close(c.done)
}

// Use a printable separator because tmux rewrites control characters for clients
// without a UTF-8 locale. Neither numeric PIDs nor client names contain it.
const registrationFormat = "#{client_pid}" + string(formatFieldSeparator) + "#{client_name}"

func (c *ControlClient) waitForRegistration(ctx context.Context) (ClientName, error) {
	pid := strconv.Itoa(c.command.Process.Pid)
	ticker := time.NewTicker(controlRegistrationPoll)
	defer ticker.Stop()
	for {
		result, err := c.server.Cmd(ctx, "list-clients", "-F", registrationFormat)
		if err != nil {
			return "", err
		}
		if result.ExitCode == 0 {
			for _, line := range result.Stdout {
				clientPID, clientName, ok := strings.Cut(line, string(formatFieldSeparator))
				if ok && clientPID == pid && strings.TrimSpace(clientName) != "" {
					return ClientName(strings.TrimSpace(clientName)), nil
				}
			}
		}
		select {
		case <-c.done:
			return "", c.registrationExitError()
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *ControlClient) failStartup(err error) error {
	return errors.Join(err, c.Close())
}

func (c *ControlClient) registrationExitError() error {
	c.stateMu.Lock()
	waitErr := c.waitErr
	c.stateMu.Unlock()
	stderr := c.stderr.String()
	if waitErr == nil {
		return fmt.Errorf("control process exited before registration; stderr=%q", stderr)
	}
	return fmt.Errorf(
		"control process exited before registration: %w; stderr=%q",
		waitErr,
		stderr,
	)
}

func (c *ControlClient) operationError() error {
	c.stateMu.Lock()
	readErr := c.readErr
	waitErr := c.waitErr
	c.stateMu.Unlock()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("%w: %w", ErrControlClosed, waitErr)
	}
	return ErrControlClosed
}

func (c *ControlClient) classifyOperationError(err error) error {
	if c.isClosing() || errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return ErrControlClosed
	}
	return fmt.Errorf("write control command: %w", err)
}

func (c *ControlClient) isClosing() bool {
	select {
	case <-c.closing:
		return true
	default:
		return false
	}
}

func (c *ControlClient) close() {
	defer close(c.closeDone)
	stdinErr := c.stdin.Close()
	if errors.Is(stdinErr, os.ErrClosed) {
		stdinErr = nil
	}
	select {
	case <-c.done:
	case <-time.After(controlClientStopGrace):
		killErr := c.command.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		c.closeErr = errors.Join(c.closeErr, killErr)
		select {
		case <-c.done:
		case <-time.After(controlClientStopTimeout):
			c.closeErr = errors.Join(
				c.closeErr,
				errors.New("control process did not exit after kill"),
			)
		}
	}
	stdoutErr := c.stdout.Close()
	if errors.Is(stdoutErr, os.ErrClosed) {
		stdoutErr = nil
	}
	c.closeErr = errors.Join(
		c.closeErr,
		stdinErr,
		stdoutErr,
		c.notifications.Close(),
	)
}
