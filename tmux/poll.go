package tmux

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrPollCondition identifies a poll whose condition was nil. It is matched by
// errors.Is.
var ErrPollCondition = errors.New("tmux: poll condition is required")

// Poll checks condition immediately and then after each interval, until it
// reports true, returns an error, or ctx ends.
//
// A condition receives ctx and must observe cancellation itself; Poll cannot
// interrupt one already running. Poll returns condition errors unchanged and
// ctx.Err when ctx ends first.
//
// Poll is for a state tmux does not announce. Waiting for a pane to print
// something is not one: [PaneObservation.WaitFor] is woken by tmux as the
// pane writes, where a poll re-reads the screen on a guess and can match a
// shell's echo of what was typed. [Server.WaitFor] waits on tmux's separate
// wait-for channel.
func Poll(
	ctx context.Context,
	interval time.Duration,
	condition func(context.Context) (bool, error),
) error {
	if interval <= 0 {
		return fmt.Errorf("tmux: poll interval must be positive, got %s", interval)
	}
	if condition == nil {
		return ErrPollCondition
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		done, err := condition(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
