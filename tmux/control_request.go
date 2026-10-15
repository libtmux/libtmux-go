package tmux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync/atomic"
	"time"
)

const (
	// Two distinct parser failures delimit a request whose alias may produce
	// any number of frames. Startup records tmux's version-specific replies.
	controlReplyFenceInput = "\\400\n\\uZZZZ\n"
)

type controlRequest struct {
	ctx context.Context
	// command is the request's original argument vector.
	command   []string
	line      string
	fenceOnly bool
	// nonce, when set, makes this a capture request: a fence command holding
	// nonce is sent behind the command, and the lines tmux prints before it
	// are the command's output. shellOutput says those lines are a shell
	// command's stdout, where otherwise they are tmux's own error text.
	nonce       string
	shellOutput bool
	response    chan controlResponse
	state       atomic.Uint32
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
// cmd observes what it runs. A caller already observing the command it drives
// uses cmdUnobserved instead, so one command produces one trace.
func (c *ControlClient) cmd(
	ctx context.Context,
	commandList bool,
	args ...string,
) (results []ControlCommandResult, err error) {
	if observer := c.server.commandObserver(); observer != nil {
		started := time.Now()
		defer func() {
			observeCommand(observer, args, started, CommandTransportConnection,
				controlExitCode(results, err), err)
		}()
	}
	return c.cmdUnobserved(ctx, commandList, args...)
}

func (c *ControlClient) cmdUnobserved(
	ctx context.Context,
	commandList bool,
	args ...string,
) ([]ControlCommandResult, error) {
	return c.cmdWith(ctx, commandList, "", false, args...)
}

// cmdCaptured is cmdUnobserved for a command that can print after its reply
// frame, as a foreground run-shell or if-shell does. The lines it prints come
// back as results, never as protocol; see [controlCapture]. shellOutput says
// the command prints a shell command's stdout.
func (c *ControlClient) cmdCaptured(
	ctx context.Context,
	commandList bool,
	shellOutput bool,
	args ...string,
) ([]ControlCommandResult, error) {
	return c.cmdWith(ctx, commandList, rand.Text(), shellOutput, args...)
}

func (c *ControlClient) cmdWith(
	ctx context.Context,
	commandList bool,
	nonce string,
	shellOutput bool,
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
		ctx:         ctx,
		command:     command,
		line:        line,
		nonce:       nonce,
		shellOutput: shellOutput,
		response:    make(chan controlResponse, 1),
	}
	return c.submitControlRequest(request)
}

func (c *ControlClient) crossReplyFence(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request := &controlRequest{
		ctx:       ctx,
		fenceOnly: true,
		response:  make(chan controlResponse, 1),
	}
	_, err := c.submitControlRequest(request)
	return err
}

func (c *ControlClient) submitControlRequest(request *controlRequest) ([]ControlCommandResult, error) {
	if c.closeRequested.Load() {
		return nil, ErrControlClosed
	}
	select {
	case c.requests <- request:
	case <-request.ctx.Done():
		return nil, request.ctx.Err()
	case <-c.requestDone:
		return nil, c.operationError()
	case <-c.stopRequests:
		return nil, ErrControlClosed
	}
	return request.await(request.ctx)
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
	input := controlReplyFenceInput
	if !request.fenceOnly {
		input = request.line + "\n" + input
		if request.nonce != "" {
			// The nonce is letters and digits, so it needs no quoting.
			c.parser.arm(request.nonce)
			input = request.line + "\ndisplay-message -p " + request.nonce + "\n" +
				controlReplyFenceInput
		}
	}
	if _, err := io.WriteString(c.stdin, input); err != nil {
		return request.finish(
			controlResponse{err: outcomeUnknown(c.classifyOperationError(err))},
			false,
		)
	}
	results := make([]ControlCommandResult, 0, 1)
	// A request can itself fail with the first fence's fingerprint. Hold that
	// frame until the next one distinguishes request A,A,B from boundary A,B.
	var pendingFirst *controlFrame
	var captured [][]byte
	for {
		frame, err := c.nextOwnFrame(context.Background())
		if err != nil {
			return request.finish(
				controlResponse{results: results, err: outcomeUnknown(err)},
				false,
			)
		}
		if frame.fence {
			captured = frame.captured
			continue
		}
		if pendingFirst != nil {
			if c.replyFence.second.matches(frame) {
				results = append(results, request.capturedResults(captured)...)
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

// capturedResults turns the lines a capture request collected into results.
// A shell command's output is stdout. Otherwise the lines are tmux's own error
// text, such as a branch that failed to parse, interleaved with the reply
// frames of commands the command inserted; lines that do not form a frame are
// errors.
func (r *controlRequest) capturedResults(lines [][]byte) []ControlCommandResult {
	if len(lines) == 0 {
		return nil
	}
	if r.shellOutput {
		return []ControlCommandResult{{
			Command:   slices.Clone(r.command),
			RawStdout: joinCapturedLines(lines),
		}}
	}
	var results []ControlCommandResult
	var bare, open [][]byte
	flush := func() {
		if len(bare) != 0 {
			results = append(results, ControlCommandResult{
				Command:   slices.Clone(r.command),
				RawStdout: joinCapturedLines(bare),
				Failed:    true,
			})
			bare = nil
		}
	}
	var parser controlStreamParser
	for _, line := range lines {
		if parser.guard == nil {
			kind, _, isGuard, err := parseControlGuard(line)
			if err != nil || !isGuard || kind != controlGuardBegin {
				bare = append(bare, line)
				continue
			}
		}
		open = append(open, line)
		frame, _, err := parser.consumeFramed(line)
		if err != nil {
			bare = append(bare, open...)
			open = nil
			parser = controlStreamParser{}
			continue
		}
		if frame != nil {
			flush()
			results = append(results, frame.result(r.command))
			open = nil
		}
	}
	// A frame that never closed is not a frame.
	bare = append(bare, open...)
	flush()
	return results
}

func joinCapturedLines(lines [][]byte) []byte {
	var joined []byte
	for _, line := range lines {
		joined = append(joined, line...)
		joined = append(joined, '\n')
	}
	return joined
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

// controlExitCode renders a control-mode outcome as the exit code a trace
// carries. tmux reports a failed command over control mode by closing its
// frame with %error rather than with a status, so a failed frame reads as 1
// and a local failure as the -1 that means no tmux command ran.
func controlExitCode(results []ControlCommandResult, err error) int {
	if err != nil {
		return -1
	}
	for _, result := range results {
		if result.Failed {
			return 1
		}
	}
	return 0
}
