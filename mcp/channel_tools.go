package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// waitForChannelInput waits on one of tmux's own channels.
type waitForChannelInput struct {
	// Channel is the tmux wait-for channel name.
	Channel string `json:"channel" jsonschema:"the tmux wait-for channel to wait on"`
	// TimeoutSeconds bounds the wait. Zero uses a default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait before giving up"`
}

// waitForChannelOutput reports how the wait ended.
type waitForChannelOutput struct {
	// Signalled reports whether the channel was signalled before the deadline.
	Signalled bool `json:"signalled"`
	// EffectiveTimeoutSeconds is the wait this call actually ran, which is
	// what timeoutSeconds asked for unless the server's ceiling was lower.
	EffectiveTimeoutSeconds int `json:"effectiveTimeoutSeconds"`
	// TimeoutClamped reports that the ceiling shortened the requested wait.
	TimeoutClamped bool `json:"timeoutClamped,omitempty"`
}

// signalChannelInput releases whoever is waiting on a channel.
type signalChannelInput struct {
	// Channel is the tmux wait-for channel name.
	Channel string `json:"channel" jsonschema:"the tmux wait-for channel to signal"`
}

// signalChannelOutput reports the channel that was signalled.
type signalChannelOutput struct {
	// Channel is the channel this call signalled.
	Channel string `json:"channel"`
}

// waitForChannel blocks until something signals a tmux channel.
//
// It is the coordination primitive tmux already has, so a client can wait on
// anything that signals one: a shell script, a key binding, another program,
// or another client of this server. run_command covers a command this client
// starts; this covers everything it did not.
func (t *tools) waitForChannel(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input waitForChannelInput,
) (*mcp.CallToolResult, waitForChannelOutput, error) {
	channel, err := validChannel(input.Channel)
	if err != nil {
		return nil, waitForChannelOutput{}, err
	}
	if input.TimeoutSeconds < 0 {
		return nil, waitForChannelOutput{}, errors.New("timeoutSeconds must not be negative")
	}
	timeout, clamped := resolveWaitTimeout(input.TimeoutSeconds)
	output := waitForChannelOutput{
		EffectiveTimeoutSeconds: int(timeout.Seconds()),
		TimeoutClamped:          clamped,
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	reporter := newProgressReporter(ctx, request, timeout, "waiting for the channel to be signalled")
	defer reporter.stop()

	// A caller-named channel cannot be signalled on timeout without releasing
	// other waiters, so its bounded wait owns a cancellable process lane. Killing
	// that client ends this call, but tmux retains its global waiter until a later
	// signal.
	waiter, err := t.runtime.process(waitCtx)
	if err != nil {
		if isOwnWaitDeadline(ctx, waitCtx, err) {
			return nil, output, nil
		}
		return nil, output, err
	}
	if err := t.runtime.deps.waitForChannel(
		waitCtx, waiter, tmux.WaitForRequest{Channel: channel},
	); err != nil {
		if isOwnWaitDeadline(ctx, waitCtx, err) {
			return nil, output, nil
		}
		return nil, output, err
	}
	output.Signalled = true
	return nil, output, nil
}

// signalChannel releases whoever is waiting on a tmux channel.
func (t *tools) signalChannel(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input signalChannelInput,
) (*mcp.CallToolResult, signalChannelOutput, error) {
	channel, err := validChannel(input.Channel)
	if err != nil {
		return nil, signalChannelOutput{}, err
	}
	if err := t.tmux(ctx).WaitFor(ctx, tmux.WaitForRequest{
		Channel: channel,
		Mode:    tmux.WaitForModeSignal,
	}); err != nil {
		return nil, signalChannelOutput{}, err
	}
	return nil, signalChannelOutput{Channel: channel}, nil
}

// validChannel rejects empty names and leading dashes.
func validChannel(name string) (string, error) {
	channel := strings.TrimSpace(name)
	if channel == "" {
		return "", errors.New("channel is required")
	}
	if strings.HasPrefix(channel, "-") {
		return "", fmt.Errorf("channel %q must not begin with a dash", name)
	}
	return channel, nil
}

// addChannelTools advertises the tools that use tmux's own coordination.
func addChannelTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityPaneControl, &mcp.Tool{
		Name:        "wait_for_channel",
		Annotations: mutating("Wait on a tmux Channel"),
		Description: "Wait until something signals a tmux wait-for channel. " +
			"Timeout or cancellation ends this call but cannot remove tmux's global " +
			"waiter; a later signal may be consumed by it, so do not reuse channel names.",
	}, t.waitForChannel)
	register(server, t, CapabilityPaneControl, &mcp.Tool{
		Name:        "signal_channel",
		Annotations: mutating("Signal a tmux Channel"),
		Description: "Signal a tmux wait-for channel, releasing whoever waits on it.",
	}, t.signalChannel)
}
