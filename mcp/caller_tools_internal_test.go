package mcp

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestCallerIdentityRetriesTmuxFailure(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	transportFailure := errors.New("caller discovery transport failed")

	tests := []struct {
		name string
		run  func(tmux.CommandRequest) (tmux.CommandResult, error)
		want error
	}{
		{
			name: "transport failure",
			run: func(tmux.CommandRequest) (tmux.CommandResult, error) {
				return tmux.CommandResult{ExitCode: -1}, transportFailure
			},
			want: transportFailure,
		},
		{
			name: "completed failure",
			run: func(request tmux.CommandRequest) (tmux.CommandResult, error) {
				if slices.Contains(request.Arguments, "-V") {
					return tmux.CommandResult{Stdout: []string{"tmux 3.6"}}, nil
				}
				return tmux.CommandResult{
					ExitCode: 1,
					Stderr:   []string{"no server running on test socket"},
				}, nil
			},
			want: tmux.ErrCommand,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			target, err := tmux.NewServer(tmux.ServerOptions{
				SocketName: "caller-retry-unused",
				Runner: tmux.CommandRunnerFunc(func(
					_ context.Context,
					request tmux.CommandRequest,
				) (tmux.CommandResult, error) {
					calls.Add(1)
					return test.run(request)
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			registry := &tools{runtime: newRuntime(t.Context(), target, nil)}

			for attempt := range 2 {
				if _, err := registry.callerIdentityFor(t.Context()); !errors.Is(err, test.want) {
					t.Fatalf("attempt %d error = %v, want %v", attempt+1, err, test.want)
				}
			}
			if calls.Load() < 2 {
				t.Fatalf("two discoveries made %d runner calls, want at least two", calls.Load())
			}
			registry.callerMutex.Lock()
			cached := registry.callerCached
			registry.callerMutex.Unlock()
			if cached {
				t.Fatal("failed caller discovery was cached")
			}

			t.Setenv("TMUX", "/tmp/caller.sock,1,0")
			t.Setenv("TMUX_PANE", "%9")
			caller, err := registry.callerIdentityFor(t.Context())
			if err != nil || caller.paneID != "%9" || !caller.inside {
				t.Fatalf("environment caller = (%#v, %v), want cached %%9", caller, err)
			}
			t.Setenv("TMUX_PANE", "%10")
			again, err := registry.callerIdentityFor(t.Context())
			if err != nil || again != caller {
				t.Fatalf("cached caller = (%#v, %v), want %#v", again, err, caller)
			}
		})
	}
}

//libtmux:real-tmux
func TestCallerIdentityCachesSuccessfulEmptyDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	target := tmuxtest.NewServer(ctx, t)
	registry := &tools{runtime: newRuntime(ctx, target, nil)}

	caller, err := registry.callerIdentityFor(ctx)
	if err != nil || caller.inside {
		t.Fatalf("caller = (%#v, %v), want successful empty identity", caller, err)
	}
	if err := target.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := registry.callerIdentityFor(ctx)
	if err != nil || again != caller {
		t.Fatalf("cached caller after server exit = (%#v, %v), want %#v", again, err, caller)
	}
}
