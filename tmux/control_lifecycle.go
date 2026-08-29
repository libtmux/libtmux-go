package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	controlClientStopGrace    = 250 * time.Millisecond
	controlClientStopTimeout  = 2 * time.Second
	controlClientCloseTimeout = controlClientStopTimeout + 2*controlClientStopGrace
)

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
	c.startClose()
	return c.waitClose(ctx)
}

func (c *ControlClient) startClose() {
	c.closeOnce.Do(func() {
		c.closeRequested.Store(true)
		close(c.stopRequests)
		go c.closeAfterRequests()
	})
}

func (c *ControlClient) waitClose(ctx context.Context) error {
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
		controlClientCloseTimeout,
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

func (c *ControlClient) waitProcess() {
	err := c.command.Wait()
	c.stateMu.Lock()
	c.waitErr = err
	c.stateMu.Unlock()
	close(c.done)
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
