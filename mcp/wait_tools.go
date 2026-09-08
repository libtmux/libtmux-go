package mcp

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Wait tools replace screen polling for pane output and tmux signals.

const (
	runCommandDefaultTimeout = 120 * time.Second
	// waitCeilingDefault bounds how long any one wait may be asked to run.
	// Longer than a build a person waits for, short enough that one wrong
	// pattern costs a wait rather than the conversation it was part of.
	waitCeilingDefault = 300 * time.Second
)

// WaitCeilingEnvironmentVariable names the variable that raises or lowers the
// longest wait this server will run, in seconds. It matches the Python server
// so an operator configuring both writes one thing.
const WaitCeilingEnvironmentVariable = "LIBTMUX_MCP_WAIT_MAX_SECONDS"

// waitCeilingFromEnvironment resolves the per-server wait bound once.
func waitCeilingFromEnvironment() time.Duration {
	raw := strings.TrimSpace(os.Getenv(WaitCeilingEnvironmentVariable))
	if raw == "" {
		return waitCeilingDefault
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		// An unreadable ceiling selects the default, on the same reasoning as
		// the startup policy: refusing to start over a misspelled variable is
		// worse than running at the value it would have run at anyway.
		return waitCeilingDefault
	}
	return time.Duration(seconds) * time.Second
}

// resolveWaitTimeout reports clamping only when the caller's explicit timeout
// exceeded the ceiling.
func (t *tools) resolveWaitTimeout(requested int) (timeout time.Duration, clamped bool) {
	ceiling := t.waitCeiling
	if ceiling <= 0 {
		ceiling = waitCeilingDefault
	}
	timeout = time.Duration(requested) * time.Second
	if timeout <= 0 {
		return min(runCommandDefaultTimeout, ceiling), false
	}
	if timeout > ceiling {
		return ceiling, true
	}
	return timeout, false
}

func isOwnWaitDeadline(ctx, waitCtx context.Context, err error) bool {
	return ctx.Err() == nil &&
		errors.Is(waitCtx.Err(), context.DeadlineExceeded) &&
		errors.Is(err, context.DeadlineExceeded)
}

// addWaitTools advertises the tools that wait instead of polling.
