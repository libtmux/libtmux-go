package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var errNilWaitCondition = errors.New("tmuxtest: wait condition is nil")

// WaitFor checks condition serially and immediately, then after each interval,
// until it succeeds, returns an error, or ctx ends. It validates interval,
// condition, and ctx in that order: interval must be positive, condition must
// be non-nil, and an already-ended context prevents every check. Condition
// errors and context errors are returned unchanged. A running condition receives
// ctx and must observe it itself; WaitFor cannot interrupt the condition.
func WaitFor(
	ctx context.Context,
	interval time.Duration,
	condition func(context.Context) (bool, error),
) error {
	if interval <= 0 {
		return fmt.Errorf("tmuxtest: wait interval must be positive, got %s", interval)
	}
	if condition == nil {
		return errNilWaitCondition
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		ready, err := condition(ctx)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
