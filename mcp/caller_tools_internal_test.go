package mcp

import (
	"context"
	"errors"
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
		name      string
		transport bool
		want      error
	}{
		{name: "transport failure", transport: true, want: transportFailure},
		{name: "completed failure", want: tmux.ErrCommand},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := tmux.ServerOptions{
				SocketName: "caller-retry-unused",
			}
			if !test.transport {
				options = executableFixtureOptions(t, fixtureUnavailable, options)
			}
			target := mustInternalTmuxServer(t, options)
			registry := &tools{runtime: newRuntime(t.Context(), target, nil)}
			calls := 0
			if test.transport {
				registry.runtime.deps.probeSessions = func(
					context.Context,
					tmux.Server,
				) ([]tmux.Session, error) {
					calls++
					return nil, transportFailure
				}
			}

			for attempt := range 2 {
				if _, err := registry.callerIdentityFor(t.Context()); !errors.Is(err, test.want) {
					t.Fatalf("attempt %d error = %v, want %v", attempt+1, err, test.want)
				}
			}
			if test.transport && calls != 2 {
				t.Fatalf("two discoveries made %d probes, want 2", calls)
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
