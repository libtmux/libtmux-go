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
	// Two distinct parser failures delimit a request whose alias may produce
	// any number of frames. Startup records tmux's version-specific replies.
	controlReplyFenceInput = "\\400\n\\uZZZZ\n"
)

type controlNotificationMode uint8

const (
	controlNotificationsRetained controlNotificationMode = iota
	controlNotificationsDiscarded
)

// ErrControlClosed identifies an operation attempted after a control client
// began closing or lost its protocol stream.
var ErrControlClosed = errors.New("tmux: control client is closed")

// ErrOutcomeUnknown identifies a command whose write began but whose reply
// boundary was not observed. The operation may have changed tmux.
var ErrOutcomeUnknown = errors.New("tmux: command outcome is unknown")

// ErrControlReplyCount identifies a call to [ControlClient.Cmd] for a command
// that produced zero or multiple reply frames.
var ErrControlReplyCount = errors.New("tmux: control command did not return exactly one reply")

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
	requests      chan *controlRequest
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
	replyFence     controlReplyFence

	// dispatching excludes flags-0 frames after the attach reply.
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
	command  []string
	line     string
	response chan controlResponse
	state    atomic.Uint32
}

type controlResponse struct {
	results []ControlCommandResult
	err     error
}

type controlRequestState uint32

const (
	controlRequestPending controlRequestState = iota
	controlRequestAccepted
	controlRequestWriting
	controlRequestFinished
	controlRequestCanceled
)

type controlFrameFingerprint struct {
	flags     int
	rawStdout string
}

func (f controlFrameFingerprint) matches(frame controlFrame) bool {
	return frame.failed && frame.flags == f.flags && string(frame.rawStdout) == f.rawStdout
}

type controlReplyFence struct {
	first  controlFrameFingerprint
	second controlFrameFingerprint
}

func newControlReplyFence(first, second controlFrame) (controlReplyFence, error) {
	if !first.failed || !second.failed || !first.ownReply() || !second.ownReply() {
		return controlReplyFence{}, errors.New("control reply fence calibration did not fail")
	}
	fence := controlReplyFence{
		first:  controlFrameFingerprint{flags: first.flags, rawStdout: string(first.rawStdout)},
		second: controlFrameFingerprint{flags: second.flags, rawStdout: string(second.rawStdout)},
	}
	if fence.first == fence.second {
		return controlReplyFence{}, errors.New("control reply fence calibration is not distinct")
	}
	return fence, nil
}

type outcomeUnknownError struct {
	cause error
}

func (e *outcomeUnknownError) Error() string {
	return ErrOutcomeUnknown.Error() + ": " + e.cause.Error()
}

func (e *outcomeUnknownError) Unwrap() []error {
	return []error{ErrOutcomeUnknown, e.cause}
}

func outcomeUnknown(cause error) error {
	if cause == nil {
		return ErrOutcomeUnknown
	}
	return &outcomeUnknownError{cause: cause}
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
	if _, err := s.stateForUse(); err != nil {
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
	if session.server.daemon != nil {
		if s.daemon != nil && !sameSnapshotIdentity(*s.daemon, *session.server.daemon) {
			return nil, ErrDaemonReplaced
		}
		s = s.withDaemon(*session.server.daemon)
	}
	if s.connection != nil {
		return nil, s.connection.routeError(ctx, CommandProcess)
	}

	attach := []string{"attach-session"}
	if mode == controlNotificationsDiscarded {
		attach = append(attach, "-f", "no-output")
	}
	attach = append(attach, "-t", session.ID().String())
	attach, guard, err := s.guardCommand(attach, false)
	if err != nil {
		return nil, err
	}
	attach = append([]string{"-C"}, attach...)
	return s.startControl(
		ctx,
		session,
		mode,
		attach,
		func(client *ControlClient) error {
			return client.acceptAttach(ctx, guard)
		},
	)
}

// startControl owns process setup shared by existing-session attachment and
// first-session creation. accept consumes the startup command's frames and may
// replace the client's initially unknown server and session provenance.
func (s Server) startControl(
	ctx context.Context,
	session Session,
	mode controlNotificationMode,
	startup []string,
	accept func(*ControlClient) error,
) (*ControlClient, error) {
	state, err := s.stateForUse()
	if err != nil {
		return nil, err
	}

	var notifications *controlNotificationQueue
	if mode == controlNotificationsRetained {
		notifications = newControlNotificationQueue(defaultControlNotificationLimit)
	}
	arguments := s.commandArguments(startup)
	command := exec.Command(state.config.executable, arguments...)
	command.Env = slices.Clone(state.config.processEnvironment)
	command.Dir = state.config.directory
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
		requests:      make(chan *controlRequest),
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

	if err := accept(client); err != nil {
		return nil, client.failStartup(err)
	}
	client.dispatching.Store(true)
	if err := client.calibrateReplyFence(ctx); err != nil {
		return nil, client.failStartup(err)
	}
	clientName, err := client.waitForRegistration(ctx)
	if err != nil {
		return nil, client.failStartup(err)
	}
	client.clientName = clientName
	go client.runRequests()
	return client, nil
}

func (c *ControlClient) acceptAttach(
	ctx context.Context,
	guard *daemonCommandGuard,
) error {
	frame, err := c.nextStartupFrame(ctx, guard)
	if err != nil {
		return err
	}
	if frame.failed {
		return fmt.Errorf(
			"control attach failed: %s",
			strings.TrimSpace(string(frame.rawStdout)),
		)
	}
	return nil
}

func (c *ControlClient) nextStartupFrame(
	ctx context.Context,
	guard *daemonCommandGuard,
) (controlFrame, error) {
	if guard != nil {
		// if-shell completes before the startup command it schedules.
		frame, err := c.nextFrame(ctx)
		if err != nil {
			return controlFrame{}, err
		}
		if frame.failed &&
			string(frame.rawStdout) == "unknown command: "+guard.failure+"\n" {
			return controlFrame{}, ErrDaemonReplaced
		}
		if frame.failed {
			return controlFrame{}, fmt.Errorf(
				"control startup guard failed: %s",
				strings.TrimSpace(string(frame.rawStdout)),
			)
		}
	}
	return c.nextFrame(ctx)
}

// Cmd executes one safely encoded tmux command through the control client. It
// requires exactly one reply frame and returns [ErrControlReplyCount]
// otherwise. Use [ControlClient.Call] for aliases that may produce zero or
// multiple frames.
func (c *ControlClient) Cmd(
	ctx context.Context,
	args ...string,
) (ControlCommandResult, error) {
	results, err := c.Call(ctx, args...)
	if err != nil {
		return ControlCommandResult{}, err
	}
	if len(results) != 1 {
		return ControlCommandResult{}, fmt.Errorf(
			"%w: got %d; use ControlClient.Call",
			ErrControlReplyCount,
			len(results),
		)
	}
	return results[0], nil
}

// Call executes one safely encoded tmux command and returns all its reply
// frames. Cancellation after writing returns [ErrOutcomeUnknown] with the
// context error while the client drains through the boundary before reuse. A
// transport failure returns any frames proven before the failure together with
// [ErrOutcomeUnknown].
func (c *ControlClient) Call(
	ctx context.Context,
	args ...string,
) ([]ControlCommandResult, error) {
	return c.cmd(ctx, false, args...)
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
	request := &controlRequest{
		ctx:      ctx,
		command:  command,
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
	return request.await(ctx)
}

func (r *controlRequest) await(ctx context.Context) ([]ControlCommandResult, error) {
	select {
	case response := <-r.response:
		return response.results, response.err
	case <-ctx.Done():
		if r.cancelBeforeWrite() {
			return nil, ctx.Err()
		}
		if controlRequestState(r.state.Load()) == controlRequestFinished {
			response := <-r.response
			return response.results, response.err
		}
		return nil, outcomeUnknown(ctx.Err())
	}
}

func (r *controlRequest) cancelBeforeWrite() bool {
	for {
		state := controlRequestState(r.state.Load())
		if state != controlRequestPending && state != controlRequestAccepted {
			return state == controlRequestCanceled
		}
		if r.state.CompareAndSwap(uint32(state), uint32(controlRequestCanceled)) {
			return true
		}
	}
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
// The context bounds only the wait: an already-ended context still starts
// shutdown, and a later call may resume waiting for the same close.
func (c *ControlClient) CloseContext(ctx context.Context) error {
	c.closeOnce.Do(func() {
		c.closeRequested.Store(true)
		close(c.stopRequests)
		go c.closeAfterRequests()
	})
	if err := ctx.Err(); err != nil {
		return err
	}
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
			if !request.state.CompareAndSwap(
				uint32(controlRequestPending),
				uint32(controlRequestAccepted),
			) {
				request.response <- controlResponse{err: request.ctx.Err()}
				continue
			}
			if c.closeRequested.Load() {
				request.state.Store(uint32(controlRequestFinished))
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
	request *controlRequest,
) (controlResponse, bool) {
	if err := request.ctx.Err(); err != nil {
		request.state.Store(uint32(controlRequestCanceled))
		return controlResponse{err: err}, true
	}
	if !request.state.CompareAndSwap(
		uint32(controlRequestAccepted),
		uint32(controlRequestWriting),
	) {
		return controlResponse{err: request.ctx.Err()}, true
	}
	if _, err := io.WriteString(c.stdin, request.line+"\n"+controlReplyFenceInput); err != nil {
		return request.finish(
			controlResponse{err: outcomeUnknown(c.classifyOperationError(err))},
			false,
		)
	}
	results := make([]ControlCommandResult, 0, 1)
	// A request can itself fail with the first fence's fingerprint. Hold that
	// frame until the next one distinguishes request A,A,B from boundary A,B.
	var pendingFirst *controlFrame
	for {
		frame, err := c.nextOwnFrame(context.Background())
		if err != nil {
			return request.finish(
				controlResponse{results: results, err: outcomeUnknown(err)},
				false,
			)
		}
		if pendingFirst != nil {
			if c.replyFence.second.matches(frame) {
				return request.finish(controlResponse{results: results}, true)
			}
			results = append(results, pendingFirst.result(request.command))
			pendingFirst = nil
		}
		if c.replyFence.first.matches(frame) {
			pendingFirst = &frame
			continue
		}
		results = append(results, frame.result(request.command))
	}
}

func (r *controlRequest) finish(
	response controlResponse,
	keepRunning bool,
) (controlResponse, bool) {
	r.state.Store(uint32(controlRequestFinished))
	return response, keepRunning
}

func (c *ControlClient) calibrateReplyFence(ctx context.Context) error {
	if _, err := io.WriteString(c.stdin, controlReplyFenceInput); err != nil {
		return fmt.Errorf("calibrate control reply fence: %w", err)
	}
	first, err := c.nextOwnFrame(ctx)
	if err != nil {
		return fmt.Errorf("calibrate first control reply fence: %w", err)
	}
	second, err := c.nextOwnFrame(ctx)
	if err != nil {
		return fmt.Errorf("calibrate second control reply fence: %w", err)
	}
	fence, err := newControlReplyFence(first, second)
	if err != nil {
		return err
	}
	c.replyFence = fence
	return nil
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

func (c *ControlClient) nextOwnFrame(ctx context.Context) (controlFrame, error) {
	for {
		frame, err := c.nextFrame(ctx)
		if err != nil || frame.ownReply() {
			return frame, err
		}
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
