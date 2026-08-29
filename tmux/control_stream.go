package tmux

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
)

// NextNotification returns the next ordered control-mode notification. Exactly
// one caller may execute it at a time. Natural process exit preserves queued
// notifications until they drain through io.EOF; Close releases the queue and
// makes subsequent reads report os.ErrClosed. A terminal reader error follows
// notifications queued before that failure. A full bounded queue likewise
// drains before reporting [ControlNotificationOverflowError].
func (c *ControlClient) NextNotification(
	ctx context.Context,
) (ControlNotification, error) {
	return c.nextNotificationAfter(ctx, 0)
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
