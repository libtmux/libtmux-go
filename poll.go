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
// tmux accepts keys before the program in a pane has run them, so reading a
// command's output back is a wait rather than a read. This is that wait. It is
// not [Server.WaitFor], which signals tmux's own wait-for channel between
// commands and cannot observe what a pane printed.
//
// Poll returns ctx.Err when the context ends first, so a caller bounds the wait
// by giving ctx a deadline rather than by passing a second timeout. A condition
// receives ctx and must observe it itself; Poll cannot interrupt one that is
// already running. Condition errors are returned unchanged.
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
