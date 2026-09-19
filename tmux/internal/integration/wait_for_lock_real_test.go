package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// tmux hands a wait-for mutex to the next queued client whether or not it is
// still there, so abandoning a queued lock wait costs the channel one unlock.
// It does not lose the channel, which is what this pins: unlocking again hands
// the mutex to the next real locker.
//
//libtmux:real-tmux
func TestAnAbandonedLockWaitLeavesTheChannelUsable(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server := tmuxtest.NewServer(ctx, t)
	const channel = "libtmux-lock"
	lock := tmux.WaitForRequest{Channel: channel, Mode: tmux.WaitForModeLock}
	unlock := tmux.WaitForRequest{Channel: channel, Mode: tmux.WaitForModeUnlock}

	if err := server.WaitFor(ctx, lock); err != nil {
		t.Fatalf("first lock: %v", err)
	}

	// A second locker queues behind the first and is abandoned.
	abandoned, abandon := context.WithTimeout(ctx, 250*time.Millisecond)
	defer abandon()
	err := server.WaitFor(abandoned, lock)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("abandoned lock = %v, want the deadline", err)
	}

	// The first release is consumed by the client that has gone. The second
	// reaches a locker that is still there.
	if err := server.WaitFor(ctx, unlock); err != nil {
		t.Fatalf("release consumed by the abandoned locker: %v", err)
	}
	if err := server.WaitFor(ctx, unlock); err != nil {
		t.Fatalf("release reaching a real locker: %v", err)
	}

	// The whole point: a later locker still gets the channel.
	taken, take := context.WithTimeout(ctx, 10*time.Second)
	defer take()
	if err := server.WaitFor(taken, lock); err != nil {
		t.Fatalf("lock after an abandoned wait: %v, want the channel recovered", err)
	}
	if err := server.WaitFor(ctx, unlock); err != nil {
		t.Fatalf("final release: %v", err)
	}
}
