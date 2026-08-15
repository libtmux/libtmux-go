package tmuxtest_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestWaitForRejectsNonpositiveIntervalBeforeCheck(t *testing.T) {
	t.Parallel()

	for _, interval := range []time.Duration{0, -time.Nanosecond} {
		calls := 0
		err := tmuxtest.WaitFor(context.Background(), interval, func(context.Context) (bool, error) {
			calls++
			return true, nil
		})
		if err == nil {
			t.Fatalf("WaitFor(interval %s) error = nil, want validation error", interval)
		}
		if calls != 0 {
			t.Fatalf("WaitFor(interval %s) check calls = %d, want 0", interval, calls)
		}
	}
}

func TestWaitForRejectsNilConditionBeforeContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	invalidIntervalErr := tmuxtest.WaitFor(ctx, 0, nil)
	if invalidIntervalErr == nil || !strings.Contains(invalidIntervalErr.Error(), "interval must be positive") {
		t.Fatalf("WaitFor(interval 0, nil) error = %v, want interval validation", invalidIntervalErr)
	}

	err := tmuxtest.WaitFor(ctx, time.Millisecond, nil)
	if err == nil || err.Error() != "tmuxtest: wait condition is nil" {
		t.Fatalf("WaitFor(canceled context, nil) error = %v, want stable nil-condition error", err)
	}
}

func TestWaitForPropagatesCheckError(t *testing.T) {
	t.Parallel()

	want := errors.New("check failed")
	err := tmuxtest.WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, error) {
		return false, want
	})
	if !sameErrorInstance(err, want) {
		t.Fatalf("WaitFor() error = %v, want exact check error %v", err, want)
	}
}

func TestWaitForPropagatesCanceledContextWithoutChecking(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := tmuxtest.WaitFor(ctx, time.Millisecond, func(context.Context) (bool, error) {
		calls++
		return true, nil
	})
	if !sameErrorInstance(err, context.Canceled) {
		t.Fatalf("WaitFor() error = %v, want context canceled", err)
	}
	if calls != 0 {
		t.Fatalf("WaitFor() check calls = %d, want 0", calls)
	}
}

// libtmux:parity libtmux.test.retry.retry_until
// libtmux:parity libtmux.test.retry.retry_until#parameter-branch:fun:7bb9650c5445
func TestWaitForRetriesUntilTrueWithCallerContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	err := tmuxtest.WaitFor(ctx, time.Millisecond, func(got context.Context) (bool, error) {
		if got != ctx {
			t.Fatalf("check context differs from caller context")
		}
		calls++
		return calls == 3, nil
	})
	if err != nil {
		t.Fatalf("WaitFor() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("WaitFor() check calls = %d, want 3", calls)
	}
}

// libtmux:parity libtmux.test.constants.RETRY_INTERVAL_SECONDS
// libtmux:parity libtmux.test.constants.RETRY_TIMEOUT_SECONDS
// libtmux:parity libtmux.test.retry.retry_until#parameter-branch:raises:3ae9164dc384
// libtmux:parity libtmux.test.retry.retry_until#parameter-branch:seconds:944b973d96b8
// libtmux:parity libtmux.exc.WaitTimeout
func TestWaitForReturnsDeadlineWhileConditionRemainsFalse(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	calls := 0
	err := tmuxtest.WaitFor(ctx, time.Millisecond, func(context.Context) (bool, error) {
		calls++
		return false, nil
	})
	if !sameErrorInstance(err, context.DeadlineExceeded) {
		t.Fatalf("WaitFor() error = %v, want context deadline exceeded", err)
	}
	if calls == 0 {
		t.Fatal("WaitFor() did not check the condition")
	}
}

func sameErrorInstance(got, want error) bool {
	gotValue := reflect.ValueOf(got)
	wantValue := reflect.ValueOf(want)
	if gotValue.Type() != wantValue.Type() {
		return false
	}
	if gotValue.Kind() == reflect.Pointer {
		return gotValue.Pointer() == wantValue.Pointer()
	}
	return reflect.DeepEqual(got, want)
}
