package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	_ *mcp.CallToolRequest,
	input waitForChannelInput,
) (*mcp.CallToolResult, waitForChannelOutput, error) {
	channel, err := validChannel(input.Channel)
	if err != nil {
		return nil, waitForChannelOutput{}, err
	}
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = runCommandDefaultTimeout
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()

	// A wait that blocks inside tmux holds a pooled connection for as long as
	// it blocks, which would leave nothing to carry the command that ends it.
	server := t.tmux()
	waiter := server.WithEngine(server.SubprocessEngine())
	if err := waiter.WaitFor(waitCtx, tmux.WaitForRequest{Channel: channel}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, waitForChannelOutput{}, nil
		}
		return nil, waitForChannelOutput{}, err
	}
	return nil, waitForChannelOutput{Signalled: true}, nil
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
	if err := t.tmux().WaitFor(ctx, tmux.WaitForRequest{
		Channel: channel,
		Mode:    tmux.WaitForModeSignal,
	}); err != nil {
		return nil, signalChannelOutput{}, err
	}
	return nil, signalChannelOutput{Channel: channel}, nil
}

// validChannel accepts a channel name tmux will take as one word.
//
// tmux reads a channel as a plain argument, so a name carrying whitespace
// would be read as a different command's worth of arguments, and one starting
// with a dash would be read as a flag.
func validChannel(name string) (string, error) {
	channel := strings.TrimSpace(name)
	if channel == "" {
		return "", errors.New("channel is required")
	}
	if strings.HasPrefix(channel, "-") {
		return "", fmt.Errorf("channel %q must not begin with a dash", name)
	}
	if strings.ContainsAny(channel, " \t\n") {
		return "", fmt.Errorf("channel %q must not contain whitespace", name)
	}
	return channel, nil
}

// addChannelTools advertises the tools that use tmux's own coordination.
func addChannelTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "wait_for_channel",
		Annotations: readOnly("Wait on a tmux Channel"),
		Description: "Wait until something signals a tmux wait-for channel. Use " +
			"it to coordinate with anything that signals one, including a script " +
			"or another client; run_command covers commands this client starts.",
	}, t.waitForChannel)
	register(server, t, &mcp.Tool{
		Name:        "signal_channel",
		Annotations: mutating("Signal a tmux Channel"),
		Description: "Signal a tmux wait-for channel, releasing whoever waits on it.",
	}, t.signalChannel)
}
