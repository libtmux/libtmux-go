package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWaitForChannelRejectsNegativeTimeout(t *testing.T) {
	runs := 0
	target := mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "negative-channel-timeout-unused",
		Runner: tmux.CommandRunnerFunc(func(
			context.Context,
			tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			runs++
			return tmux.CommandResult{ExitCode: -1}, errors.New("runtime reached")
		}),
	})
	instance := mustInternalMCPServer(t, target)

	_, _, err := instance.tools.waitForChannel(
		t.Context(), nil,
		waitForChannelInput{Channel: "negative-timeout", TimeoutSeconds: -1},
	)
	if err == nil || !strings.Contains(err.Error(), "timeoutSeconds") {
		t.Fatalf("negative wait_for_channel timeout error = %v", err)
	}
	if runs != 0 {
		t.Fatalf("negative wait_for_channel timeout reached tmux %d times, want 0", runs)
	}
}

func TestWaitForChannelSchemaRejectsNegativeTimeouts(t *testing.T) {
	schema, err := jsonschema.For[waitForChannelInput](nil)
	if err != nil {
		t.Fatal(err)
	}
	constrain("wait_for_channel", schema)
	timeout := schema.Properties["timeoutSeconds"]
	if timeout == nil || timeout.Minimum == nil || *timeout.Minimum != 0 {
		t.Fatalf("wait_for_channel timeoutSeconds minimum = %v, want 0", timeout.Minimum)
	}
}

func TestWaitForChannelSurfacesAnIndependentSetupDeadline(t *testing.T) {
	target := mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "channel-independent-deadline-unused",
		Runner: tmux.CommandRunnerFunc(func(
			context.Context,
			tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			return tmux.CommandResult{ExitCode: -1}, context.DeadlineExceeded
		}),
	})
	instance := mustInternalMCPServer(t, target)

	_, _, err := instance.tools.waitForChannel(
		t.Context(), nil,
		waitForChannelInput{Channel: "independent-deadline", TimeoutSeconds: 10},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("independent wait_for_channel setup deadline error = %v", err)
	}
}

func TestWaitForChannelSurfacesAnIndependentCommandDeadline(t *testing.T) {
	target := mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "channel-command-deadline-unused",
		Runner: tmux.CommandRunnerFunc(func(
			context.Context,
			tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			return tmux.CommandResult{ExitCode: -1}, context.DeadlineExceeded
		}),
	})
	instance := mustInternalMCPServer(t, target)
	ctx := withAcquiredServer(t.Context(), &runtimeAcquisition{
		server: target, runtime: instance.runtime, unbound: true,
	})

	_, _, err := instance.tools.waitForChannel(
		ctx, nil,
		waitForChannelInput{Channel: "independent-command-deadline", TimeoutSeconds: 10},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("independent wait_for_channel command deadline error = %v", err)
	}
}

func TestWaitForChannelBoundsColdRuntimeAcquisition(t *testing.T) {
	t.Setenv(WaitCeilingEnvironmentVariable, "1")
	longSetup := errors.New("runtime acquisition outlived the wait ceiling")
	target := mustInternalTmuxServer(t, tmux.ServerOptions{
		SocketName: "cold-channel-timeout-unused",
		Runner: tmux.CommandRunnerFunc(func(
			ctx context.Context,
			_ tmux.CommandRequest,
		) (tmux.CommandResult, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 2*time.Second {
				return tmux.CommandResult{ExitCode: -1}, longSetup
			}
			<-ctx.Done()
			return tmux.CommandResult{ExitCode: -1}, ctx.Err()
		}),
	})
	instance := mustInternalMCPServer(t, target)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	_, output, err := instance.tools.waitForChannel(
		ctx, nil,
		waitForChannelInput{Channel: "never-signalled", TimeoutSeconds: 9},
	)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if output.Signalled || output.EffectiveTimeoutSeconds != 1 || !output.TimeoutClamped {
		t.Fatalf("wait_for_channel output = %+v, want one-second clamped timeout", output)
	}
	if elapsed < 750*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("cold wait_for_channel elapsed = %v, want the one-second ceiling", elapsed)
	}
}

//libtmux:real-tmux
func TestWaitForChannelReportsTheResolvedTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Setenv(WaitCeilingEnvironmentVariable, "7")
	target, instance := newChannelTestInstance(ctx, t, "channel-timeout-reporting")

	for _, test := range []struct {
		name       string
		requested  int
		effective  int
		wasClamped bool
	}{
		{name: "default bounded by ceiling", effective: 7},
		{name: "explicit value clamped", requested: 9, effective: 7, wasClamped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			channel := "resolved-" + test.name
			signalTestChannel(ctx, t, target, channel)
			_, output, err := instance.tools.waitForChannel(
				ctx, nil, waitForChannelInput{
					Channel: channel, TimeoutSeconds: test.requested,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !output.Signalled || output.EffectiveTimeoutSeconds != test.effective ||
				output.TimeoutClamped != test.wasClamped {
				t.Fatalf("wait_for_channel output = %+v, want effective=%d clamped=%t signalled",
					output, test.effective, test.wasClamped)
			}
		})
	}
}

//libtmux:real-tmux
func TestWaitForChannelHonorsTheEnvironmentCeiling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Setenv(WaitCeilingEnvironmentVariable, "1")
	_, instance := newChannelTestInstance(ctx, t, "channel-timeout-ceiling")
	if _, err := instance.runtime.command(ctx); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, output, err := instance.tools.waitForChannel(
		ctx, nil, waitForChannelInput{Channel: "never-signalled", TimeoutSeconds: 9},
	)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if output.Signalled || output.EffectiveTimeoutSeconds != 1 || !output.TimeoutClamped {
		t.Fatalf("wait_for_channel output = %+v, want one-second clamped timeout", output)
	}
	if elapsed < 750*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("wait_for_channel elapsed = %v, want the one-second ceiling", elapsed)
	}
}

//libtmux:real-tmux
func TestWaitForChannelOwnsProgressUntilTheWaitReturns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Setenv(WaitCeilingEnvironmentVariable, "4")
	target, instance := newChannelTestInstance(ctx, t, "channel-progress")
	if _, err := instance.runtime.command(ctx); err != nil {
		t.Fatal(err)
	}

	connection := newBlockingProgressConnection()
	progressServer := sdk.NewServer(
		&sdk.Implementation{Name: "channel-progress", Version: "0"}, nil,
	)
	progressSession, err := progressServer.Connect(
		t.Context(), blockingProgressTransport{connection}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = progressSession.Close() })
	params := &sdk.CallToolParamsRaw{}
	params.SetProgressToken("channel-progress-token")
	request := &sdk.CallToolRequest{Session: progressSession, Params: params}

	type result struct {
		output waitForChannelOutput
		err    error
	}
	returned := make(chan result, 1)
	go func() {
		_, output, err := instance.tools.waitForChannel(
			ctx, request,
			waitForChannelInput{Channel: "progress-channel", TimeoutSeconds: 9},
		)
		returned <- result{output: output, err: err}
	}()
	select {
	case <-connection.writeStarted:
	case <-time.After(3 * time.Second):
		signalTestChannel(ctx, t, target, "progress-channel")
		<-returned
		_ = connection.Close()
		t.Fatal("wait_for_channel emitted no progress during a long wait")
	}
	signalTestChannel(ctx, t, target, "progress-channel")
	select {
	case got := <-returned:
		if got.err != nil || !got.output.Signalled ||
			got.output.EffectiveTimeoutSeconds != 4 || !got.output.TimeoutClamped {
			t.Fatalf("wait_for_channel result = (%+v, %v)", got.output, got.err)
		}
	case <-time.After(time.Second):
		_ = connection.Close()
		t.Fatal("wait_for_channel did not join its progress reporter")
	}
	select {
	case <-connection.writeReturned:
	default:
		_ = connection.Close()
		t.Fatal("wait_for_channel returned while its progress write was still active")
	}
}

func newChannelTestInstance(
	ctx context.Context,
	t *testing.T,
	name string,
) (tmux.Server, *Instance) {
	t.Helper()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	if _, err := target.NewSession(ctx, tmux.NewSessionRequest{Name: name}); err != nil {
		t.Fatal(err)
	}
	return target, mustInternalMCPServer(t, target)
}

func signalTestChannel(
	ctx context.Context,
	t *testing.T,
	target tmux.Server,
	channel string,
) {
	t.Helper()
	if err := target.WaitFor(ctx, tmux.WaitForRequest{
		Channel: channel, Mode: tmux.WaitForModeSignal,
	}); err != nil {
		t.Fatal(err)
	}
}
