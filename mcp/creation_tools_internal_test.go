package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestCreateWindowPreservesIdentityAfterPostCreationFailure(t *testing.T) {
	for _, test := range []struct {
		name          string
		failedRefresh int32
		terminal      bool
	}{
		{name: "creation refresh", failedRefresh: 1},
		{name: "canonical refresh", failedRefresh: 2},
		{name: "terminal canonical refresh", failedRefresh: 2, terminal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			backing := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
			if _, err := backing.NewSession(ctx, tmux.NewSessionRequest{
				Name: "partial-window-anchor", Command: "sleep 300",
			}); err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected post-creation window refresh failure")
			failure := error(injected)
			if test.terminal {
				failure = errors.Join(tmux.ErrDaemonReplaced, injected)
			}
			delegate := tmux.SubprocessRunner()
			var created atomic.Bool
			var refreshes atomic.Int32
			target := mustInternalTmuxServer(t, tmux.ServerOptions{
				SocketPath:         backing.SocketPath(),
				ConfigFile:         backing.ConfigFile(),
				ProcessEnvironment: backing.ProcessEnvironment(),
				Runner: tmux.CommandRunnerFunc(func(
					ctx context.Context,
					request tmux.CommandRequest,
				) (tmux.CommandResult, error) {
					contains := func(command string) bool {
						return slices.ContainsFunc(request.Arguments, func(argument string) bool {
							return argument == command || strings.HasPrefix(argument, "'"+command+"' ")
						})
					}
					if created.Load() && contains("list-windows") &&
						refreshes.Add(1) == test.failedRefresh {
						return tmux.CommandResult{ExitCode: -1}, failure
					}
					result, err := delegate.Run(ctx, request)
					if err == nil && result.ExitCode == 0 && contains("new-window") {
						created.Store(true)
					}
					return result, err
				}),
			})
			instance := mustInternalMCPServer(t, target)
			callCtx := withAcquiredServer(ctx, &runtimeAcquisition{server: target})

			result, output, err := instance.tools.createWindow(
				callCtx, nil,
				createWindowInput{SessionName: "partial-window-anchor", Name: "partial-window"},
			)
			if err != nil {
				t.Fatalf("createWindow() error = %v, want classified partial failure", err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("createWindow() result = %#v, want classified partial failure", result)
			}
			if output.WindowID == "" {
				t.Fatal("classified window failure omitted the created window ID")
			}
			if _, err := backing.Window(ctx, tmux.WindowID(output.WindowID)); err != nil {
				t.Fatalf("created window %s did not survive its refresh failure: %v", output.WindowID, err)
			}
			if !strings.Contains(callToolResultText(result), injected.Error()) {
				t.Fatalf("failure text = %q, want injected refresh failure", callToolResultText(result))
			}
		})
	}
}

func callToolResultText(result *sdk.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if block, ok := content.(*sdk.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}
