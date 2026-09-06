package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestPaneInputCallerEnvironmentIsStrict(t *testing.T) {
	tests := []struct {
		name        string
		tmux        string
		tmuxPresent bool
		pane        string
		panePresent bool
		want        paneInputCaller
		wantError   bool
	}{
		{name: "detached", want: paneInputCaller{state: paneInputCallerDetached}},
		{
			name: "socket commas", tmux: "/tmp/a,b.sock,123,0", tmuxPresent: true,
			pane: "%7", panePresent: true, want: paneInputCaller{
				state: paneInputCallerUnresolved, socket: "/tmp/a,b.sock",
				serverPID: 123, sessionID: "$0", paneID: "%7",
			},
		},
		{name: "tmux only", tmux: "/tmp/a.sock,123,0", tmuxPresent: true, wantError: true},
		{name: "pane only", pane: "%7", panePresent: true, wantError: true},
		{name: "empty tmux", tmuxPresent: true, pane: "%7", panePresent: true, wantError: true},
		{
			name: "empty pane", tmux: "/tmp/a.sock,123,0", tmuxPresent: true,
			panePresent: true, wantError: true,
		},
		{
			name: "missing session", tmux: "/tmp/a.sock,123", tmuxPresent: true,
			pane: "%7", panePresent: true, wantError: true,
		},
		{
			name: "prefixed session", tmux: "/tmp/a.sock,123,$0", tmuxPresent: true,
			pane: "%7", panePresent: true, wantError: true,
		},
		{
			name: "noncanonical session", tmux: "/tmp/a.sock,123,00", tmuxPresent: true,
			pane: "%7", panePresent: true, wantError: true,
		},
		{
			name: "zero pid", tmux: "/tmp/a.sock,0,0", tmuxPresent: true,
			pane: "%7", panePresent: true, wantError: true,
		},
		{
			name: "noncanonical pid", tmux: "/tmp/a.sock,0123,0", tmuxPresent: true,
			pane: "%7", panePresent: true, wantError: true,
		},
		{
			name: "noncanonical pane", tmux: "/tmp/a.sock,123,0", tmuxPresent: true,
			pane: "%07", panePresent: true, wantError: true,
		},
		{
			name: "nul in socket", tmux: "/tmp/a\x00.sock,123,0", tmuxPresent: true,
			pane: "%7", panePresent: true, wantError: true,
		},
		{
			name: "del in socket", tmux: "/tmp/a\x7f.sock,123,0", tmuxPresent: true,
			pane: "%7", panePresent: true, wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePaneInputCaller(
				test.tmux, test.tmuxPresent, test.pane, test.panePresent,
			)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("parsePaneInputCaller() = (%#v, %v), want (%#v, error=%t)",
					got, err, test.want, test.wantError)
			}
		})
	}
}

func TestPaneInputCallerClassifiesEndpointBeforePID(t *testing.T) {
	directory := t.TempDir()
	selected := filepath.Join(directory, "selected.sock")
	foreign := filepath.Join(directory, "foreign.sock")
	if err := os.WriteFile(selected, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "selected-alias.sock")
	if err := os.Symlink(selected, alias); err != nil {
		t.Fatal(err)
	}
	panes := []paneInputCallerPane{{sessionID: "$0", paneID: "%7"}}

	got, err := classifyPaneInputCaller(paneInputCaller{
		state: paneInputCallerUnresolved, socket: foreign, serverPID: 999,
		sessionID: "$9", paneID: "%99",
	}, paneInputServerIdentity{endpoint: selected, serverPID: 123, serverStartTime: 456}, panes)
	if err != nil || got.state != paneInputCallerForeign {
		t.Fatalf("foreign caller = (%#v, %v), want foreign", got, err)
	}

	for _, caller := range []paneInputCaller{
		{
			state: paneInputCallerUnresolved, socket: alias, serverPID: 999,
			sessionID: "$0", paneID: "%7",
		},
		{
			state: paneInputCallerUnresolved, socket: alias, serverPID: 123,
			sessionID: "$1", paneID: "%7",
		},
		{
			state: paneInputCallerUnresolved, socket: alias, serverPID: 123,
			sessionID: "$0", paneID: "%8",
		},
	} {
		if _, err := classifyPaneInputCaller(
			caller,
			paneInputServerIdentity{endpoint: selected, serverPID: 123, serverStartTime: 456},
			panes,
		); err == nil {
			t.Fatalf("stale selected-endpoint caller %#v was accepted", caller)
		}
	}

	got, err = classifyPaneInputCaller(paneInputCaller{
		state: paneInputCallerUnresolved, socket: alias, serverPID: 123,
		sessionID: "$0", paneID: "%7",
	}, paneInputServerIdentity{endpoint: selected, serverPID: 123, serverStartTime: 456}, panes)
	if err != nil || got.state != paneInputCallerSelected || got.socket != selected ||
		got.serverStartTime != 456 {
		t.Fatalf("selected caller = (%#v, %v), want selected", got, err)
	}
}

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
