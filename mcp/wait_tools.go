package mcp

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Wait tools replace screen polling. run_command tracks authored commands,
// wait_for_text follows external output, and wait_for_channel follows tmux signals.

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

// waitCeiling bounds caller latency without blocking other requests. Oversized
// waits are clamped, and each reply reports the effective timeout.
func waitCeiling() time.Duration {
	raw := strings.TrimSpace(os.Getenv(WaitCeilingEnvironmentVariable))
	if raw == "" {
		return waitCeilingDefault
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		// An unreadable ceiling selects the default, on the same reasoning as
		// the safety level: refusing to start over a misspelled variable is
		// worse than running at the value it would have run at anyway.
		return waitCeilingDefault
	}
	return time.Duration(seconds) * time.Second
}

// resolveWaitTimeout reports clamping only when the caller's explicit timeout
// exceeded the ceiling.
func resolveWaitTimeout(requested int) (timeout time.Duration, clamped bool) {
	ceiling := waitCeiling()
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
func addWaitTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityPaneControl, &mcp.Tool{
		Name:        "run_command",
		Annotations: mutating("Run a Command in a Pane"),
		Description: "Run a shell command in one pane, wait for it to finish, and " +
			"return its exit status and its output. Prefer this to send_keys " +
			"followed by capture_pane: it does not read the screen to decide the " +
			"command is done, so the shell's echo of the command cannot be " +
			"mistaken for the command's output, and an exit status is something " +
			"no capture recovers. Pass detach for a command you do not need the " +
			"answer to yet, such as a build: it returns a jobId at once, you do " +
			"other work, and get_job collects the status and the output later.",
	}, t.runCommand)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "get_job",
		Annotations: readOnly("Collect a Detached Command"),
		Description: "Collect a command started with run_command and detach. " +
			"Without timeoutSeconds it reports whether the command has finished " +
			"and returns at once, which is what to call between other work; with " +
			"one it waits that long. A finished job reports its exit status and " +
			"its output, and answers the same way however often you ask.",
	}, t.getJob)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "wait_for_text",
		Annotations: readOnly("Wait for Pane Output"),
		Description: "Wait until a pane writes one of several patterns. It takes " +
			"one exact screen baseline after attaching, then reads what the pane " +
			"produces without polling. Use it for output the " +
			"client did not author, such as a service announcing it is ready. " +
			"Pass stop with the markers of failure you already know, so a run " +
			"that failed returns at once instead of at the deadline. Omit " +
			"patterns to wait for any output at all. Matching retains the most recent " +
			"one megabyte and reports any older prefix as truncation. When you cannot predict what " +
			"finishing prints, set idleSeconds and wait for the pane to go quiet " +
			"instead.",
	}, t.waitForText)
}
