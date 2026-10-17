package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestOlderCoreRuntimeKeepsItsProcessLane(t *testing.T) {
	old := os.Getenv("LIBTMUX_OPTIONAL_OLD_TMUX")
	if old == "" {
		t.Skip("set LIBTMUX_OPTIONAL_OLD_TMUX to exact tmux3.3a")
	}
	record := filepath.Join(t.TempDir(), "control-attempt")
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(proxy, []byte(`#!/bin/sh
case "$*" in *' run-shell '*) exec "$MCP_OPTIONAL_BINARY" "$@" ;; esac
for argument do
    if [ "$argument" = -C ]; then printf 'unsafe\n' >> "$MCP_OPTIONAL_RECORD"; fi
done
exec "$MCP_OPTIONAL_BINARY" "$@"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	target := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		Binary: proxy, FixedShell: true,
		InitialSession:     &tmux.NewSessionRequest{Name: "keeper"},
		ProcessEnvironment: append(os.Environ(), "MCP_OPTIONAL_RECORD="+record, "MCP_OPTIONAL_BINARY="+old),
	})
	runtime := newRuntime(t.Context(), target)
	t.Cleanup(func() { _ = runtime.Close() })
	server, err := runtime.command(t.Context())
	if err != nil || server.ConnectionBound() {
		t.Fatalf("older runtime command lane: %v bound=%t", err, server.ConnectionBound())
	}
	if err := server.SetBuffer(t.Context(), tmux.SetBufferRequest{Name: new("permanent"), Data: "ordinary"}); err != nil {
		t.Fatalf("ordinary core write: %v", err)
	}
	created, err := runtime.createSession(t.Context(), tmux.NewSessionRequest{Name: "second"})
	if err != nil || created.ID() == "" || created.Server().ConnectionBound() {
		t.Fatalf("older runtime creation: %v %v", created, err)
	}
	if got := runtime.current(); !got.Equal(created.Server()) || got.ConnectionBound() {
		t.Fatalf("current lost process lane: %v", got)
	}
	pane, err := created.ResolveActivePane(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	observation, err := runtime.openObservation(t.Context(), pane)
	if observation != nil {
		_ = observation.Close()
	}
	if !errors.Is(err, tmux.ErrVersionTooLow) {
		t.Fatalf("older control-only observation: %v", err)
	}
	if data, err := os.ReadFile(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("older runtime attempted control: %q %v", data, err)
	}
	sessions, err := server.Sessions(t.Context())
	if err != nil || len(sessions) != 2 {
		t.Fatalf("older runtime baseline sessions: %d %v", len(sessions), err)
	}
}

func TestOlderCoreRuntimeBootstrapsWithoutControl(t *testing.T) {
	old := os.Getenv("LIBTMUX_OPTIONAL_OLD_TMUX")
	if old == "" {
		t.Skip("set LIBTMUX_OPTIONAL_OLD_TMUX to exact tmux3.3a")
	}
	target := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: old, FixedShell: true})
	runtime := newRuntime(t.Context(), target)
	t.Cleanup(func() { _ = runtime.Close() })
	created, err := runtime.createSession(t.Context(), tmux.NewSessionRequest{Name: "first"})
	if err != nil || created.ID() == "" || created.Server().ConnectionBound() {
		t.Fatalf("older cold bootstrap: %v %v", created, err)
	}
	server, err := runtime.command(t.Context())
	if err != nil || server.ConnectionBound() {
		t.Fatalf("older cold command lane: %v bound=%t", err, server.ConnectionBound())
	}
	if !server.Equal(created.Server()) {
		t.Fatal("cold bootstrap changed the selected endpoint")
	}
}

func TestOlderPasteStagingRefusesBeforeTheFirstChunk(t *testing.T) {
	old := os.Getenv("LIBTMUX_OPTIONAL_OLD_TMUX")
	if old == "" {
		t.Skip("set LIBTMUX_OPTIONAL_OLD_TMUX to exact tmux3.3a")
	}
	target := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		Binary: old, InitialSession: &tmux.NewSessionRequest{Name: "keeper"},
	})
	runtime := newRuntime(t.Context(), target)
	t.Cleanup(func() { _ = runtime.Close() })
	chunks := 0
	runtime.deps.setBuffer = func(_ context.Context, _ tmux.Server, _ tmux.SetBufferRequest) error { chunks++; return nil }
	registry := &tools{runtime: runtime}
	err := registry.stageBuffer(t.Context(), target, "never-created", "text")
	if !errors.Is(err, tmux.ErrVersionTooLow) || chunks != 0 {
		t.Fatalf("unsafe staging = %v, wrote %d chunks", err, chunks)
	}
}
