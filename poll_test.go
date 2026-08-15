package tmux_test

import (
	"context"
	"errors"
	"testing"
	"time"

	tmux "github.com/libtmux/libtmux-go"
)

func TestPollStopsWhenTheConditionHolds(t *testing.T) {
	t.Parallel()
	checks := 0
	err := tmux.Poll(context.Background(), time.Millisecond,
		func(context.Context) (bool, error) {
			checks++
			return checks == 3, nil
		})
	if err != nil || checks != 3 {
		t.Fatalf("Poll() = %v after %d checks, want nil after 3", err, checks)
	}
}

func TestPollChecksImmediatelyWithoutWaitingAnInterval(t *testing.T) {
	t.Parallel()
	// An hour-long interval proves the first check does not wait for one.
	err := tmux.Poll(context.Background(), time.Hour,
		func(context.Context) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("Poll() = %v, want nil", err)
	}
}

func TestPollReturnsTheConditionError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("condition failed")
	err := tmux.Poll(context.Background(), time.Millisecond,
		func(context.Context) (bool, error) { return false, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("Poll() = %v, want the condition's own error", err)
	}
}

func TestPollBoundsTheWaitByTheContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := tmux.Poll(ctx, time.Millisecond,
		func(context.Context) (bool, error) { return false, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Poll() = %v, want context.DeadlineExceeded", err)
	}
}

func TestPollRejectsAnUnusableRequest(t *testing.T) {
	t.Parallel()
	if err := tmux.Poll(context.Background(), 0,
		func(context.Context) (bool, error) { return true, nil }); err == nil {
		t.Fatal("Poll() with a zero interval = nil, want an error")
	}
	if err := tmux.Poll(context.Background(), time.Millisecond, nil); !errors.Is(
		err, tmux.ErrPollCondition,
	) {
		t.Fatalf("Poll() with no condition = %v, want ErrPollCondition", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tmux.Poll(cancelled, time.Millisecond,
		func(context.Context) (bool, error) {
			t.Error("condition ran on an ended context")
			return true, nil
		}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Poll() on an ended context = %v, want context.Canceled", err)
	}
}
