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
)

// NextNotification returns the next ordered control-mode notification. Exactly
// one caller may execute it at a time. Close releases the queue and makes
// subsequent reads report os.ErrClosed, drained through queued notifications
// first. Natural process exit before a caller asked to close instead reports
// [ErrControlStreamLost] - most often because the server the underlying tmux
// client was attached to exited - naming the tmux exit reason when one was
// sent. A terminal reader error follows notifications queued before that
// failure. A full bounded queue likewise drains before reporting
// [ControlNotificationOverflowError].
func (c *ControlClient) NextNotification(
	ctx context.Context,
) (ControlNotification, error) {
	notification, err := c.nextNotificationAfter(ctx, 0)
	if err == nil || !errors.Is(err, io.EOF) {
		return notification, err
	}
	if c.closeRequested.Load() {
		return ControlNotification{}, os.ErrClosed
	}
	c.stateMu.Lock()
	reason := c.lastExitReason
	c.stateMu.Unlock()
	if reason != "" {
		return ControlNotification{}, fmt.Errorf(
			"%w: tmux exited the client: %s: %w", ErrControlStreamLost, reason, err,
		)
	}
	return ControlNotification{}, fmt.Errorf("%w: %w", ErrControlStreamLost, err)
}

func (c *ControlClient) nextNotificationAfter(
	ctx context.Context,
	sequence uint64,
) (ControlNotification, error) {
	record, err := c.notifications.next(ctx, sequence)
	if err != nil {
		return ControlNotification{}, err
	}
	return ParseControlNotification(record)
}

// readyNotificationAfter is nextNotificationAfter without the wait; ok is
// false when nothing is queued.
func (c *ControlClient) readyNotificationAfter(
	sequence uint64,
) (notification ControlNotification, ok bool, err error) {
	record, err := c.notifications.nextReady(sequence)
	if err != nil || record == nil {
		return ControlNotification{}, false, err
	}
	notification, err = ParseControlNotification(record)
	return notification, true, err
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
// Every other error ends the stream after being yielded, including the
// [ErrControlStreamLost] or os.ErrClosed [ControlClient.NextNotification]
// reports at a natural end.
//
// Leaving early preserves queued notifications for the next read.
func (c *ControlClient) Notifications(
	ctx context.Context,
) iter.Seq2[ControlNotification, error] {
	return notificationSeq(ctx, c.NextNotification)
}

// notificationSeq turns one blocking read into a range loop with the
// [ControlClient.Notifications] contract.
func notificationSeq(
	ctx context.Context,
	next func(context.Context) (ControlNotification, error),
) iter.Seq2[ControlNotification, error] {
	return func(yield func(ControlNotification, error) bool) {
		for {
			notification, err := next(ctx)
			// Identity, not errors.Is: a richly wrapped loss such as
			// ErrPaneObservationLost or ErrControlStreamLost can unwrap to
			// io.EOF too, and must still be yielded rather than silently
			// swallowed here. Only the bare sentinel itself means "clean end,
			// nothing more to report."
			if err == io.EOF { //nolint:errorlint // see comment above
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

func (c *ControlClient) readStream() {
	var finalErr error
	var wireSequence uint64
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
				c.trackSessionChange(notification)
				c.trackExitReason(notification)
				wireSequence++
				if appendErr := c.notifications.append(
					wireSequence,
					notification,
				); appendErr != nil {
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
				wireSequence++
				frame.wireSequence = wireSequence
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

func (c *ControlClient) trackSessionChange(record []byte) {
	if !bytes.HasPrefix(record, []byte(ControlNotificationSessionChanged+" ")) {
		return
	}
	notification, err := ParseControlNotification(record)
	if err != nil || notification.Kind() != ControlNotificationSessionChanged {
		return
	}
	arguments := notification.arguments
	if len(arguments) == 0 || validateStableTarget("session", arguments[0]) != nil {
		return
	}
	c.stateMu.Lock()
	c.currentSessionID = SessionID(arguments[0])
	c.stateMu.Unlock()
}

// trackExitReason records the tail of a %exit notification as it streams
// past, so a later unsolicited EOF can name why tmux ended the connection
// even though the caller may have already read past that notification.
func (c *ControlClient) trackExitReason(record []byte) {
	const kind = ControlNotificationExit
	if !bytes.HasPrefix(record, []byte(kind)) {
		return
	}
	if len(record) > len(kind) && record[len(kind)] != ' ' {
		return
	}
	notification, err := ParseControlNotification(record)
	if err != nil || notification.Kind() != ControlNotificationExit {
		return
	}
	var reason string
	if arguments := notification.Arguments(); len(arguments) != 0 {
		reason = arguments[0]
	}
	c.stateMu.Lock()
	c.lastExitReason = reason
	c.stateMu.Unlock()
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
