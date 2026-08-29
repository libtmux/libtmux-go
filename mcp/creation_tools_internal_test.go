package mcp

import (
	"context"
	"errors"
	"strings"
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
			target := mustInternalTmuxServer(t, tmux.ServerOptions{
				SocketPath:         backing.SocketPath(),
				ConfigFile:         backing.ConfigFile(),
				ProcessEnvironment: backing.ProcessEnvironment(),
			})
			instance := mustInternalMCPServer(t, target)
			defaults := instance.runtime.deps
			if test.failedRefresh == 1 {
				instance.runtime.deps.newWindow = func(
					ctx context.Context,
					session tmux.Session,
					request tmux.NewWindowRequest,
				) (tmux.Window, error) {
					window, err := defaults.newWindow(ctx, session, request)
					if err != nil {
						return window, err
					}
					return window, failure
				}
			} else {
				instance.runtime.deps.refreshWindow = func(
					context.Context,
					tmux.Server,
					tmux.WindowID,
				) (tmux.Window, error) {
					return tmux.Window{}, failure
				}
			}
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

//libtmux:real-tmux
func TestCreateSessionReportsPartialIdentityAfterRefreshFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	target := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	instance := mustInternalMCPServer(t, target)
	refreshFailure := errors.New("injected post-creation refresh failure")
	create := instance.runtime.deps.newSessionConnection
	failedRefresh := false
	instance.runtime.deps.newSessionConnection = func(
		ctx context.Context,
		server tmux.Server,
		request tmux.NewSessionRequest,
	) (tmux.Session, *tmux.Connection, error) {
		session, connection, err := create(ctx, server, request)
		if err != nil {
			return session, connection, err
		}
		failedRefresh = true
		return session, connection, refreshFailure
	}

	result, output, err := instance.tools.createSession(
		ctx, nil,
		createSessionInput{Name: "partially-created", Command: "sleep 300"},
	)
	if err != nil {
		t.Fatalf("createSession() error = %v, want a classified tool failure", err)
	}
	if !failedRefresh {
		t.Fatal("create_session did not reach the injected post-creation refresh failure")
	}
	if result == nil || !result.IsError {
		t.Fatalf("createSession() result = %#v, want classified failure", result)
	}
	if output.SessionID == "" || output.SessionName != "partially-created" {
		t.Fatalf("createSession() output = %+v, want the created session identity", output)
	}
	if !strings.Contains(callToolResultText(result), refreshFailure.Error()) {
		t.Fatalf("failure text = %q, want injected refresh failure", callToolResultText(result))
	}
	if _, err := target.Session(ctx, tmux.SessionID(output.SessionID)); err != nil {
		t.Fatalf("created session %s did not survive its setup failure: %v", output.SessionID, err)
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
