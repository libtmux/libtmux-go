//go:build linux

package integration

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestOwnedAdoptionStableObjectsAndClientClose(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServer(ctx, t)
	session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "adopt-session"})
	connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := connection.Session().Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Refresh(ctx); err != nil {
		t.Fatalf("client close destroyed borrowed session: %v", err)
	}
	if _, err := session.Rename(ctx, "renamed"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBTMUX_SOCKET_PATH", filepath.Join(t.TempDir(), "must-not-be-used.sock"))
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
	if _, err := session.Refresh(ctx); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("session survives owner close: %v", err)
	}

	source := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "source"})
	destination := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "destination"})
	window := tmuxtest.NewWindow(ctx, t, source, tmux.NewWindowRequest{Name: new("owned-window")})
	windowOwner, err := window.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := window.Move(ctx, tmux.MoveWindowRequest{TargetSession: destination.ID(), NoSelect: true}); err != nil {
		t.Fatal(err)
	}
	moved, err := window.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := moved.Link(ctx, tmux.LinkWindowRequest{TargetSession: source.ID(), Detach: true}); err != nil {
		t.Fatal(err)
	}
	child, err := moved.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := windowOwner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := window.Refresh(ctx); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("linked window survives owner: %v", err)
	}
	if _, err := child.Refresh(ctx); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("window child survives owner: %v", err)
	}

	first, err := source.ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := destination.ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pane, err := first.SplitPane(ctx, tmux.SplitPaneRequest{Command: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	paneOwner, err := pane.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pane.Move(ctx, tmux.MovePaneRequest{TargetWindow: second}); err != nil {
		t.Fatal(err)
	}
	if err := paneOwner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := pane.Refresh(ctx); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("moved pane survives owner: %v", err)
	}
	if _, err := first.Refresh(ctx); err != nil {
		t.Fatalf("old parent destroyed: %v", err)
	}
	if _, err := second.Refresh(ctx); err != nil {
		t.Fatalf("new parent destroyed: %v", err)
	}
}

func TestOwnedServerRejectsReplacement(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServer(ctx, t)
	stale, err := server.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	killer, err := server.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := killer.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := server.FindOrCreate(ctx, tmux.NewSessionRequest{Name: "replacement"}, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := replacement.Owner.Close(); err != nil {
			t.Error(err)
		}
	})
	if !replacement.Created || replacement.Owner == nil {
		t.Fatal("replacement was not identified as created")
	}
	for range 2 {
		if err := stale.Close(); !errors.Is(err, tmux.ErrDaemonReplaced) {
			t.Fatalf("stale owner close = %v", err)
		}
		if err := replacement.Value.CheckAlive(ctx); err != nil {
			t.Fatalf("replacement killed: %v", err)
		}
	}
}

func TestOwnedGenerationRejectsEqualProcessIdentity(t *testing.T) {
	const key = "@libtmux_owner_generation"
	for _, kind := range []string{"server", "session", "window", "pane"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			server := tmuxtest.NewServer(ctx, t)
			session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "generation"})
			window, err := session.ResolveActiveWindow(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pane, err := window.ResolveActivePane(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := server.SetOption(ctx, key, strings.Repeat("A", 32), tmux.SetOptionOptions{}); err != nil {
				t.Fatal(err)
			}
			var owner io.Closer
			switch kind {
			case "server":
				owner, err = server.Adopt(ctx, tmux.OwnershipOptions{})
			case "session":
				owner, err = session.Adopt(ctx, tmux.OwnershipOptions{})
			case "window":
				owner, err = window.Adopt(ctx, tmux.OwnershipOptions{})
			case "pane":
				owner, err = pane.Adopt(ctx, tmux.OwnershipOptions{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if token, ok, err := server.RawOption(ctx, key); err != nil || !ok || token != strings.Repeat("A", 32) {
				t.Fatalf("valid generation was replaced: %q %t %v", token, ok, err)
			}
			before, err := server.Cmd(ctx, "display-message", "-p", "#{pid}/#{start_time}/#{socket_path}")
			if err != nil || before.ExitCode != 0 {
				t.Fatalf("initial process identity: %#v %v", before, err)
			}
			if err := server.SetOption(ctx, key, strings.Repeat("b", 32), tmux.SetOptionOptions{}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := owner.Close(); !errors.Is(err, tmux.ErrDaemonReplaced) {
					t.Fatalf("different generation with equal PID/start/socket accepted: %v", err)
				}
			}
			after, err := server.Cmd(ctx, "display-message", "-p", "#{pid}/#{start_time}/#{socket_path}")
			if err != nil || after.ExitCode != 0 || strings.Join(before.Stdout, "") != strings.Join(after.Stdout, "") {
				t.Fatalf("process identity changed: before=%#v after=%#v error=%v", before, after, err)
			}
			if _, err := pane.Refresh(ctx); err != nil {
				t.Fatalf("stale owner destroyed resource: %v", err)
			}
		})
	}
}

func TestOwnedGenerationMalformedStopsAcquisition(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{InitialSession: &tmux.NewSessionRequest{Name: "keeper"}})
	session, err := server.SessionByName(ctx, "keeper")
	if err != nil {
		t.Fatal(err)
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pane, err := window.ResolveActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{"", "bad", strings.Repeat("a", 31), strings.Repeat("g", 32), strings.Repeat("a", 33)} {
		name := malformed
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if err := server.SetOption(ctx, "@libtmux_owner_generation", malformed, tmux.SetOptionOptions{}); err != nil {
				t.Fatal(err)
			}
			calls := []func() error{
				func() error { _, err := server.Adopt(ctx, tmux.OwnershipOptions{}); return err },
				func() error { _, err := session.Adopt(ctx, tmux.OwnershipOptions{}); return err },
				func() error { _, err := window.Adopt(ctx, tmux.OwnershipOptions{}); return err },
				func() error { _, err := pane.Adopt(ctx, tmux.OwnershipOptions{}); return err },
				func() error {
					_, err := server.OwnSession(ctx, tmux.NewSessionRequest{Name: "forbidden"}, tmux.OwnershipOptions{})
					return err
				},
				func() error {
					_, err := session.OwnWindow(ctx, tmux.NewWindowRequest{}, tmux.OwnershipOptions{})
					return err
				},
				func() error {
					_, err := window.OwnPane(ctx, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
					return err
				},
			}
			for i, call := range calls {
				if err := call(); !errors.Is(err, tmux.ErrInvalidOwnerGeneration) {
					t.Fatalf("acquisition %d accepted malformed generation: %v", i, err)
				}
			}
			value, ok, err := server.RawOption(ctx, "@libtmux_owner_generation")
			if err != nil || !ok || value != malformed {
				t.Fatalf("malformed metadata overwritten: %q %t %v", value, ok, err)
			}
			snapshot, err := server.Snapshot(ctx)
			if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 1 || len(snapshot.Panes()) != 1 {
				t.Fatalf("malformed metadata created resources: %#v %v", snapshot, err)
			}
		})
	}
}

func TestOwnedGenerationConcurrentIndependentAdoption(t *testing.T) {
	server := tmuxtest.NewServer(t.Context(), t)
	owners := make(chan *tmux.Owned[tmux.Server], 8)
	var wait sync.WaitGroup
	for range cap(owners) {
		wait.Go(func() {
			independent, err := tmux.NewServer(tmux.ServerOptions{SocketPath: server.SocketPath(), ConfigFile: server.ConfigFile()})
			if err != nil {
				t.Error(err)
				return
			}
			owner, err := independent.Adopt(t.Context(), tmux.OwnershipOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			owners <- owner
		})
	}
	wait.Wait()
	close(owners)
	token, ok, err := server.RawOption(t.Context(), "@libtmux_owner_generation")
	decoded, decodeErr := hex.DecodeString(token)
	if err != nil || !ok || decodeErr != nil || len(decoded) != 16 {
		t.Fatalf("initialized generation: %q %t %v %v", token, ok, err, decodeErr)
	}
	for owner := range owners {
		if err := owner.Value().CheckAlive(t.Context()); err != nil {
			t.Fatalf("independent adoption bound different token: %v", err)
		}
	}
}

func TestOwnedAdoptionDoesNotStartDaemon(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
	if _, err := server.Adopt(t.Context(), tmux.OwnershipOptions{}); !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("missing daemon adoption: %v", err)
	}
	if alive, err := server.IsAlive(t.Context()); err != nil || alive {
		t.Fatalf("adoption started daemon: %t %v", alive, err)
	}
}

func TestOwnedCreationRetainsResponseGeneration(t *testing.T) {
	for _, kind := range []string{"session", "window", "pane"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			flag := filepath.Join(dir, "replace-generation")
			path := filepath.Join(dir, "tmux-wrapper")
			command := map[string]string{"session": "new-session", "window": "new-window", "pane": "split-window"}[kind]
			script := fmt.Sprintf(`#!/bin/sh
real=%s
capture=$(mktemp %q)
trap 'rm -f "$capture" "$capture.err"' EXIT
"$real" "$@" >"$capture" 2>"$capture.err"
status=$?
case "$*" in *%s*)
  if [ -f %q ] && [ "$status" -eq 0 ]; then
    rm %q
    for argument do
      case "$argument" in -S*) socket=${argument#-S};; -f*) config=${argument#-f};; esac
    done
    "$real" -S "$socket" -f "$config" set-option -s @libtmux_owner_generation bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb || exit $?
  fi;;
esac
cat "$capture"
cat "$capture.err" >&2
exit "$status"
`, lifecycleTmuxShell(t), filepath.Join(dir, "capture.XXXXXX"), command, flag, flag)
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
				Binary: path, InitialSession: &tmux.NewSessionRequest{Name: "keeper"},
			})
			session, err := server.SessionByName(t.Context(), "keeper")
			if err != nil {
				t.Fatal(err)
			}
			window, err := session.ResolveActiveWindow(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(flag, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "session":
				_, err = server.OwnSession(t.Context(), tmux.NewSessionRequest{Name: "captured"}, tmux.OwnershipOptions{})
			case "window":
				_, err = session.OwnWindow(t.Context(), tmux.NewWindowRequest{}, tmux.OwnershipOptions{})
			case "pane":
				_, err = window.OwnPane(t.Context(), tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
			}
			failure, ok := errors.AsType[*tmux.AcquisitionError](err)
			if !ok || failure.Unknown || failure.ResourceID == "" || !errors.Is(failure.Cause, tmux.ErrDaemonReplaced) ||
				!errors.Is(failure.Rollback, tmux.ErrDaemonReplaced) || failure.Cleanup == nil {
				t.Fatalf("creation adopted a later generation: %#v %v", failure, err)
			}
			if err := failure.Cleanup.Close(); !errors.Is(err, tmux.ErrDaemonReplaced) {
				t.Fatalf("retry adopted a later generation: %v", err)
			}
			snapshot, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{"session": len(snapshot.Sessions()), "window": len(snapshot.Windows()), "pane": len(snapshot.Panes())}
			if counts[kind] != 2 {
				t.Fatalf("uncertain generation resource destroyed: %#v", counts)
			}
		})
	}
}

func TestOwnedSessionLiteralArguments(t *testing.T) {
	server := tmuxtest.NewServer(t.Context(), t)
	owner, err := server.OwnSession(t.Context(), tmux.NewSessionRequest{Name: "literal;", WindowName: ";"}, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	if name, ok := owner.Value().Name(); !ok || name != "literal;" {
		t.Fatalf("session argument changed: %q", name)
	}
	window, err := owner.Value().ResolveActiveWindow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := window.Name(); !ok || name != ";" {
		t.Fatalf("window argument changed: %q", name)
	}
}

func lifecycleFailureWrapper(t *testing.T, command string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	flag := filepath.Join(dir, "fail")
	path := filepath.Join(dir, "tmux-wrapper")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *%s*) if [ -f '%s' ]; then echo 'injected lifecycle failure' >&2; exit 19; fi;; esac\nexec %s \"$@\"\n", command, flag, lifecycleTmuxShell(t))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, flag
}

func lifecycleTmuxShell(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}

func TestOwnedBodyFailureCancellationAndRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	path, flag := lifecycleFailureWrapper(t, "kill-session")
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{Binary: path})
	owner, err := server.OwnSession(ctx, tmux.NewSessionRequest{Name: "retry"}, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bodyErr := errors.New("body failed")
	result := bodyErr
	cancel()
	owner.CloseInto(&result)
	if !errors.Is(result, bodyErr) || !errors.Is(result, tmux.ErrCommand) {
		t.Fatalf("paired errors lost: %v", result)
	}
	if _, err := owner.Value().Refresh(t.Context()); err != nil {
		t.Fatalf("failed cleanup lost object: %v", err)
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("retry after cancellation: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Value().Refresh(t.Context()); !errors.Is(err, tmux.ErrNotFound) && !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("successful retry left session alive: %v", err)
	}
}

func TestOwnedKnownIDRollback(t *testing.T) {
	for _, kind := range []string{"session", "window", "pane"} {
		t.Run(kind, func(t *testing.T) {
			command := map[string]string{"session": "list-sessions", "window": "list-windows", "pane": "list-panes"}[kind]
			path, flag := lifecycleFailureWrapper(t, command)
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
				Binary:         path,
				InitialSession: &tmux.NewSessionRequest{Name: "keeper"},
			})
			parent, err := server.SessionByName(t.Context(), "keeper")
			if err != nil {
				t.Fatal(err)
			}
			window, err := parent.ResolveActiveWindow(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(flag, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "session":
				_, err = server.OwnSession(t.Context(), tmux.NewSessionRequest{Name: "rollback"}, tmux.OwnershipOptions{})
			case "window":
				_, err = parent.OwnWindow(t.Context(), tmux.NewWindowRequest{Name: new("rollback")}, tmux.OwnershipOptions{})
			case "pane":
				_, err = window.OwnPane(t.Context(), tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
			}
			failure, ok := errors.AsType[*tmux.AcquisitionError](err)
			if !ok || failure.Unknown || failure.ResourceID == "" || failure.Rollback != nil {
				t.Fatalf("known rollback = %#v, %v", failure, err)
			}
			if err := os.Remove(flag); err != nil {
				t.Fatal(err)
			}
			snapshot, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 1 || len(snapshot.Panes()) != 1 {
				t.Fatalf("rollback left objects: sessions=%d windows=%d panes=%d", len(snapshot.Sessions()), len(snapshot.Windows()), len(snapshot.Panes()))
			}
		})
	}
}

func TestFindOrCreateAllResourcesAndConcurrentCalls(t *testing.T) {
	ctx := t.Context()
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{Config: []byte("set -g @startup_config preserved\n")})
	root, err := server.FindOrCreate(ctx, tmux.NewSessionRequest{Name: "keeper"}, tmux.OwnershipOptions{})
	if err != nil || !root.Created || root.Owner == nil {
		t.Fatalf("server create = %#v, %v", root, err)
	}
	t.Cleanup(func() {
		if err := root.Owner.Close(); err != nil {
			t.Error(err)
		}
	})
	value, ok, err := root.Value.GlobalSessionScope().RawOption(ctx, "@startup_config")
	if err != nil || !ok || value != "preserved" {
		t.Fatalf("config lost: %q %t %v", value, ok, err)
	}
	again, err := server.FindOrCreate(ctx, tmux.NewSessionRequest{Name: "ignored"}, tmux.OwnershipOptions{})
	if err != nil || again.Created || again.Owner != nil {
		t.Fatalf("server reuse = %#v, %v", again, err)
	}
	first, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{Name: "work"}, tmux.OwnershipOptions{})
	if err != nil || !first.Created {
		t.Fatalf("session create = %#v, %v", first, err)
	}
	second, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{Name: "work"}, tmux.OwnershipOptions{})
	if err != nil || second.Created || second.Owner != nil || second.Value.ID() != first.Value.ID() {
		t.Fatalf("session reuse = %#v, %v", second, err)
	}
	w, err := first.Value.FindOrCreateWindow(ctx, tmux.NewWindowRequest{Name: new("tools")}, tmux.OwnershipOptions{})
	if err != nil || !w.Created {
		t.Fatalf("window create = %#v, %v", w, err)
	}
	w2, err := first.Value.FindOrCreateWindow(ctx, tmux.NewWindowRequest{Name: new("tools")}, tmux.OwnershipOptions{})
	if err != nil || w2.Created || w2.Owner != nil || w.Value.ID() != w2.Value.ID() {
		t.Fatalf("window reuse = %#v, %v", w2, err)
	}
	identity := tmux.PaneIdentity{Key: "@test_app_role", Value: "worker"}
	p, err := w.Value.FindOrCreatePane(ctx, identity, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
	if err != nil || !p.Created {
		t.Fatalf("pane create = %#v, %v", p, err)
	}
	p2, err := w.Value.FindOrCreatePane(ctx, identity, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
	if err != nil || p2.Created || p2.Owner != nil || p.Value.ID() != p2.Value.ID() {
		t.Fatalf("pane reuse = %#v, %v", p2, err)
	}
	if err := p.Owner.Close(); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"server", "session", "window", "pane"} {
		t.Run("competing-"+kind, func(t *testing.T) {
			const count = 5
			var wait sync.WaitGroup
			created := make(chan bool, count)
			for range count {
				wait.Go(func() {
					switch kind {
					case "server":
						r, err := server.FindOrCreate(ctx, tmux.NewSessionRequest{}, tmux.OwnershipOptions{})
						if err != nil {
							t.Error(err)
						}
						created <- r.Created
					case "session":
						r, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{Name: "concurrent"}, tmux.OwnershipOptions{})
						if err != nil {
							t.Error(err)
						}
						created <- r.Created
					case "window":
						r, err := first.Value.FindOrCreateWindow(ctx, tmux.NewWindowRequest{Name: new("concurrent")}, tmux.OwnershipOptions{})
						if err != nil {
							t.Error(err)
						}
						created <- r.Created
					case "pane":
						r, err := w.Value.FindOrCreatePane(ctx, identity, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
						if err != nil {
							t.Error(err)
						}
						created <- r.Created
					}
				})
			}
			wait.Wait()
			close(created)
			n := 0
			for value := range created {
				if value {
					n++
				}
			}
			want := 1
			if kind == "server" {
				want = 0
			}
			if n != want {
				t.Fatalf("created %d, want %d", n, want)
			}
		})
	}

	if _, err := first.Value.NewWindow(ctx, tmux.NewWindowRequest{Name: new("tools")}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Value.FindOrCreateWindow(ctx, tmux.NewWindowRequest{Name: new("tools")}, tmux.OwnershipOptions{}); !errors.Is(err, tmux.ErrSnapshotAmbiguous) {
		t.Fatalf("window ambiguity = %v", err)
	}
	panes, err := w.Value.SearchPanes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range panes {
		if err := pane.SetOption(ctx, identity.Key, identity.Value, tmux.SetOptionOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Value.FindOrCreatePane(ctx, identity, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{}); !errors.Is(err, tmux.ErrSnapshotAmbiguous) {
		t.Fatalf("pane ambiguity = %v", err)
	}
	if _, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{Name: "invalid.name"}, tmux.OwnershipOptions{}); !errors.Is(err, tmux.ErrInvalidRequest) {
		t.Fatalf("session invalid = %v", err)
	}
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"}); !errors.Is(err, tmux.ErrSessionExists) {
		t.Fatalf("native duplicate = %v", err)
	}
}

func TestOwnedInitialUnknownResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux-wrapper")
	script := fmt.Sprintf("#!/bin/sh\nreal=%s\ncase \"$*\" in *new-session*) \"$real\" \"$@\" >/dev/null; echo 'lost creation result' >&2; exit 19;; esac\nexec \"$real\" \"$@\"\n", lifecycleTmuxShell(t))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
	owner, err := server.OwnSession(t.Context(), tmux.NewSessionRequest{Name: "unknown"}, tmux.OwnershipOptions{})
	failure, ok := errors.AsType[*tmux.AcquisitionError](err)
	if owner != nil || !ok || !failure.Unknown || failure.Rollback != nil {
		t.Fatalf("unknown = %#v %v", owner, err)
	}
	if _, err := server.SessionByName(t.Context(), "unknown"); err != nil {
		t.Fatalf("unknown create was guessed away: %v", err)
	}
}

func TestOwnedCleanupDeadline(t *testing.T) {
	path, flag := lifecycleFailureWrapper(t, "never-match")
	_ = flag
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *kill-session*) sleep 1;; esac\nexec %s \"$@\"\n", lifecycleTmuxShell(t))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
	owner, err := server.OwnSession(t.Context(), tmux.NewSessionRequest{Name: "deadline"}, tmux.OwnershipOptions{CleanupTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	err = owner.Close()
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(fmt.Sprint(err), "deadline") {
		t.Fatalf("deadline = %v", err)
	}
}

func TestFindOrCreateExternalStarterRemainsBorrowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux-wrapper")
	flag := filepath.Join(dir, "compete")
	script := fmt.Sprintf(`#!/bin/sh
real=%s
case "$*" in *new-session*)
  if [ -f %q ]; then
    rm %q
    for argument do
      case "$argument" in -S*) socket=${argument#-S};; -f*) config=${argument#-f};; esac
    done
    env -i PATH="$PATH" "$real" -S "$socket" -f "$config" new-session -d -s unrelated || exit $?
  fi;;
esac
exec "$real" "$@"
`, lifecycleTmuxShell(t), flag, flag)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
	result, err := server.FindOrCreate(t.Context(), tmux.NewSessionRequest{Name: "ours"}, tmux.OwnershipOptions{})
	if err != nil || result.Created || result.Owner != nil {
		t.Fatalf("external starter adopted: %#v %v", result, err)
	}
	if _, err := server.SessionByName(t.Context(), "unrelated"); err != nil {
		t.Fatalf("external session removed: %v", err)
	}
	if _, err := server.SessionByName(t.Context(), "ours"); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("bootstrap session retained: %v", err)
	}
}

func TestFindOrCreateConcurrentInitialCreation(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
	results := make(chan tmux.Found[tmux.Server], 5)
	var wait sync.WaitGroup
	for range 5 {
		wait.Go(func() {
			r, err := server.FindOrCreate(t.Context(), tmux.NewSessionRequest{Name: "keeper"}, tmux.OwnershipOptions{})
			if err != nil {
				t.Error(err)
			}
			results <- r
		})
	}
	wait.Wait()
	close(results)
	n := 0
	for r := range results {
		if r.Created {
			n++
			t.Cleanup(func() {
				if err := r.Owner.Close(); err != nil {
					t.Error(err)
				}
			})
		}
	}
	if n != 1 {
		t.Fatalf("created %d daemons, want 1", n)
	}
}

func TestOwnedCancellationDuringAcquisition(t *testing.T) {
	for _, kind := range []string{"server", "session", "window", "pane"} {
		t.Run(kind, func(t *testing.T) {
			fixture := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
			if kind != "server" {
				if _, err := fixture.NewSession(t.Context(), tmux.NewSessionRequest{Name: "keeper"}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			command := map[string]string{"server": "start-server", "session": "start-server", "window": "new-window", "pane": "split-window"}[kind]
			server, err := tmux.NewServer(tmux.ServerOptions{
				SocketPath: fixture.SocketPath(), ConfigFile: fixture.ConfigFile(),
				CommandObserver: func(trace tmux.CommandTrace) {
					if trace.Subcommand == command && trace.ExitCode == 0 {
						cancel()
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "server":
				_, err = server.FindOrCreate(ctx, tmux.NewSessionRequest{Name: "cancel"}, tmux.OwnershipOptions{})
			case "session":
				_, err = server.OwnSession(ctx, tmux.NewSessionRequest{Name: "cancel"}, tmux.OwnershipOptions{})
			case "window", "pane":
				parent, lookupErr := server.SessionByName(ctx, "keeper")
				if lookupErr != nil {
					t.Fatal(lookupErr)
				}
				if kind == "window" {
					_, err = parent.OwnWindow(ctx, tmux.NewWindowRequest{Name: new("cancel")}, tmux.OwnershipOptions{})
				} else {
					window, lookupErr := parent.ResolveActiveWindow(ctx)
					if lookupErr != nil {
						t.Fatal(lookupErr)
					}
					_, err = window.OwnPane(ctx, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
				}
			}
			failure, ok := errors.AsType[*tmux.AcquisitionError](err)
			if !ok || failure.Unknown || failure.Rollback != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel rollback = %#v %v", failure, err)
			}
			if kind == "server" {
				if alive, err := fixture.IsAlive(t.Context()); alive || err != nil {
					t.Fatalf("canceled acquisition left daemon: %t %v", alive, err)
				}
			} else {
				snapshot, err := fixture.Snapshot(t.Context())
				if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 1 || len(snapshot.Panes()) != 1 {
					t.Fatalf("canceled acquisition left objects: %#v %v", snapshot, err)
				}
			}
		})
	}
}

func TestOwnedRollbackFailureIsInspectableAndRetryable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wrapper")
	flag := filepath.Join(dir, "fail")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *list-sessions*|*kill-session*) if [ -f '%s' ]; then echo injected >&2; exit 19; fi;; esac\nexec %s \"$@\"\n", flag, lifecycleTmuxShell(t))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	owner, err := server.OwnSession(t.Context(), tmux.NewSessionRequest{Name: "retry"}, tmux.OwnershipOptions{})
	failure, ok := errors.AsType[*tmux.AcquisitionError](err)
	if !ok || failure.Unknown || failure.Cause == nil || failure.Rollback == nil || failure.Cleanup == nil || owner == nil {
		t.Fatalf("paired acquisition errors = %#v %v", failure, err)
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Value().Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := failure.Cleanup.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFindOrCreateMutationFailures(t *testing.T) {
	for _, kind := range []string{"server", "session", "window", "pane"} {
		t.Run(kind, func(t *testing.T) {
			command := map[string]string{"server": "new-session", "session": "new-session", "window": "new-window", "pane": "split-window"}[kind]
			path, flag := lifecycleFailureWrapper(t, command)
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
			var session tmux.Session
			var window tmux.Window
			var err error
			if kind != "server" {
				session, err = server.NewSession(t.Context(), tmux.NewSessionRequest{Name: "keeper"})
				if err != nil {
					t.Fatal(err)
				}
				window, err = session.ResolveActiveWindow(t.Context())
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(flag, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "server":
				_, err = server.FindOrCreate(t.Context(), tmux.NewSessionRequest{Name: "fail"}, tmux.OwnershipOptions{})
			case "session":
				_, err = server.FindOrCreateSession(t.Context(), tmux.NewSessionRequest{Name: "fail"}, tmux.OwnershipOptions{})
			case "window":
				_, err = session.FindOrCreateWindow(t.Context(), tmux.NewWindowRequest{Name: new("fail")}, tmux.OwnershipOptions{})
			case "pane":
				_, err = window.FindOrCreatePane(t.Context(), tmux.PaneIdentity{Key: "@role", Value: "fail"}, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
			}
			if !errors.Is(err, tmux.ErrCommand) {
				t.Fatalf("creation failure lost: %v", err)
			}
			if err := os.Remove(flag); err != nil {
				t.Fatal(err)
			}
			if kind == "server" {
				if alive, err := server.IsAlive(t.Context()); alive || err != nil {
					t.Fatalf("failed creation started daemon: %t %v", alive, err)
				}
			} else {
				snapshot, err := server.Snapshot(t.Context())
				if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 1 || len(snapshot.Panes()) != 1 {
					t.Fatalf("failed creation mutated resources: %v", err)
				}
			}
		})
	}
}

func TestFindOrCreatePaneIdentityIsLocalAndRollsBackFailure(t *testing.T) {
	path, flag := lifecycleFailureWrapper(t, "set-option*test_app_role")
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		Binary: path, InitialSession: &tmux.NewSessionRequest{Name: "keeper"},
	})
	session, err := server.SessionByName(t.Context(), "keeper")
	if err != nil {
		t.Fatal(err)
	}
	window, err := session.ResolveActiveWindow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	identity := tmux.PaneIdentity{Key: "@test_app_role", Value: "worker"}
	if err := window.SetOption(t.Context(), identity.Key, identity.Value, tmux.SetOptionOptions{}); err != nil {
		t.Fatal(err)
	}
	created, err := window.FindOrCreatePane(t.Context(), identity, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
	if err != nil || !created.Created {
		t.Fatalf("inherited identity reused as pane-local: %#v %v", created, err)
	}
	if err := created.Owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = window.FindOrCreatePane(t.Context(), identity, tmux.SplitPaneRequest{}, tmux.OwnershipOptions{})
	failure, ok := errors.AsType[*tmux.AcquisitionError](err)
	if !ok || failure.Unknown || failure.ResourceID == "" || failure.Rollback != nil {
		t.Fatalf("identity failure rollback = %#v %v", failure, err)
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	panes, err := window.SearchPanes(t.Context(), nil)
	if err != nil || len(panes) != 1 {
		t.Fatalf("failed identity left pane: %d %v", len(panes), err)
	}
}

func TestOwnedMissingEndpointDoesNotClaimLiveDaemonCleanup(t *testing.T) {
	for _, kind := range []string{"server", "session", "window", "pane"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			server := tmuxtest.NewServer(ctx, t)
			session := tmuxtest.NewSession(ctx, t, server, tmux.NewSessionRequest{Name: "temporarily-unreachable"})
			window, err := session.ResolveActiveWindow(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pane, err := window.ResolveActivePane(ctx)
			if err != nil {
				t.Fatal(err)
			}
			options := tmux.OwnershipOptions{CleanupTimeout: 250 * time.Millisecond}
			var owner io.Closer
			var refresh func() error
			switch kind {
			case "server":
				owner, err = server.Adopt(ctx, options)
				refresh = func() error { return server.CheckAlive(ctx) }
			case "session":
				owner, err = session.Adopt(ctx, options)
				refresh = func() error { _, err := session.Refresh(ctx); return err }
			case "window":
				owner, err = window.Adopt(ctx, options)
				refresh = func() error { _, err := window.Refresh(ctx); return err }
			case "pane":
				owner, err = pane.Adopt(ctx, options)
				refresh = func() error { _, err := pane.Refresh(ctx); return err }
			}
			if err != nil {
				t.Fatal(err)
			}
			path := server.SocketPath()
			moved := path + ".moved"
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			restored := false
			defer func() {
				if !restored {
					if err := os.Rename(moved, path); err != nil {
						t.Error(err)
					}
				}
			}()
			if err := owner.Close(); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unreachable live daemon reported clean: %v", err)
			}
			if err := os.Rename(moved, path); err != nil {
				t.Fatal(err)
			}
			restored = true
			if err := refresh(); err != nil {
				t.Fatalf("daemon did not retain owned resource: %v", err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if err := refresh(); !errors.Is(err, tmux.ErrNotFound) && !errors.Is(err, tmux.ErrNoServer) {
				t.Fatalf("retry retained owned resource: %v", err)
			}
			if kind != "server" {
				if err := server.CheckAlive(ctx); err != nil {
					t.Fatalf("child cleanup destroyed daemon: %v", err)
				}
			}
		})
	}
}

func TestOwnedDeferredCleanupDuringPanic(t *testing.T) {
	server := tmuxtest.NewServer(t.Context(), t)
	owner, err := server.OwnSession(t.Context(), tmux.NewSessionRequest{Name: "panic-body"}, tmux.OwnershipOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const sentinel = "panic body"
	var cleanupErr error
	func() {
		defer func() {
			if value := recover(); value != sentinel {
				t.Errorf("panic changed: %v", value)
			}
		}()
		defer owner.CloseInto(&cleanupErr)
		panic(sentinel)
	}()
	if cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
	if _, err := owner.Value().Refresh(t.Context()); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("panic left resource: %v", err)
	}
}
