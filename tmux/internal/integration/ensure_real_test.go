//go:build linux

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestEnsureStartsUsableEmptyServer(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
	if alive, err := server.IsAlive(t.Context()); err != nil || alive {
		t.Fatalf("initial endpoint: alive=%t error=%v", alive, err)
	}
	ensured, err := server.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !ensured.Equal(server) {
		t.Fatal("ensure changed the selected endpoint")
	}
	if err := ensured.CheckAlive(t.Context()); err != nil {
		t.Fatalf("ensured server unavailable: %v", err)
	}
	if rows := ensureRows(t, ensured, "list-sessions", "-F", "#{session_id}"); len(rows) != 0 {
		t.Fatalf("bootstrap session retained: %v", rows)
	}
	if value, ok, err := ensured.RawOption(t.Context(), "exit-empty"); err != nil || !ok || value != "off" {
		t.Fatalf("new server lifetime: %q %t %v", value, ok, err)
	}
	for _, row := range ensureRows(t, ensured, "show-environment", "-g") {
		if strings.HasPrefix(row, "LIBTMUX_START_") {
			t.Fatalf("startup marker retained: %s", row)
		}
	}
	before := ensureState(t, ensured)
	if _, err := ensured.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if after := ensureState(t, ensured); !slices.Equal(before, after) {
		t.Fatalf("repeat ensure changed state:\nbefore=%q\nafter=%q", before, after)
	}
	if _, err := ensured.NewSession(t.Context(), tmux.NewSessionRequest{Name: "after-ensure", Command: "cat"}); err != nil {
		t.Fatalf("subsequent public operation failed: %v", err)
	}
}

func TestEnsureKeepsConfigurationResources(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		Config: []byte("new-session -d -s configured -n original cat\nset -g @configuration loaded\n"),
	})
	ensured, err := server.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rows := ensureRows(t, ensured, "list-sessions", "-F", "#{session_name}"); !slices.Equal(rows, []string{"configured"}) {
		t.Fatalf("startup removed configured resources or retained bootstrap: %v", rows)
	}
	if rows := ensureRows(t, ensured, "show-options", "-gqv", "@configuration"); !slices.Equal(rows, []string{"loaded"}) {
		t.Fatalf("startup bypassed configuration: %v", rows)
	}
}

func TestEnsureSurvivesStartupConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, config string
		scrub, hook  bool
		keepSession  bool
	}{
		{name: "marker removed", scrub: true, keepSession: true},
		{name: "marker removed with empty lifetime", config: "set -s exit-empty off\n", scrub: true},
		{name: "marker removed with configured session", config: "new-session -d -s configured -n original cat\n", scrub: true},
		{name: "renamed bootstrap with configured session", config: "new-session -d -s configured -n original cat\n", hook: true},
		{name: "marker removed and renamed bootstrap", scrub: true, hook: true, keepSession: true},
		{name: "marker removed and renamed with configured session", config: "new-session -d -s configured -n original cat\n", scrub: true, hook: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := test.config
			capture := filepath.Join(t.TempDir(), "configured-before")
			const format = "#{session_id}|#{session_name}|#{window_id}|#{window_name}|#{pane_id}|#{pane_pid}"
			if strings.Contains(config, "new-session") {
				command := fmt.Sprintf("%s -S '#{socket_path}' list-panes -a -F '%s' > %q", lifecycleTmuxShell(t), format, capture)
				config += "run-shell " + strconv.Quote(command) + "\n"
			}
			if test.scrub {
				config += ensureScrubMarkerConfig(t)
			}
			if test.hook {
				config += "set-hook -g after-new-session 'rename-session renamed-by-hook'\n"
			}
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Config: []byte(config)})
			ensured, err := server.Ensure(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := ensured.CheckAlive(t.Context()); err != nil {
				t.Fatalf("successful ensure returned an unavailable daemon: %v", err)
			}
			sessions := ensureRows(t, ensured, "list-sessions", "-F", "#{session_name}")
			if test.keepSession {
				if len(sessions) != 1 || test.hook && sessions[0] != "renamed-by-hook" || !test.hook && !strings.HasPrefix(sessions[0], "libtmux-start-") {
					t.Fatalf("unclassified startup lost its keeper: %v", sessions)
				}
			} else if strings.Contains(config, "new-session -d -s configured") {
				before, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				after := strings.Join(ensureRows(t, ensured, "list-panes", "-a", "-F", format), "\n")
				if strings.TrimSpace(string(before)) != after || !slices.Equal(sessions, []string{"configured"}) {
					t.Fatalf("configuration-created identities changed: before=%q after=%q sessions=%v", before, after, sessions)
				}
			} else if len(sessions) != 0 {
				t.Fatalf("unneeded bootstrap retained: %v", sessions)
			}
			wantExitEmpty := "off"
			if test.scrub && !strings.Contains(config, "set -s exit-empty off") {
				wantExitEmpty = "on"
			}
			if rows := ensureRows(t, ensured, "show-options", "-sqv", "exit-empty"); !slices.Equal(rows, []string{wantExitEmpty}) {
				t.Fatalf("startup changed unproven server lifetime: %v", rows)
			}
			before := ensureState(t, ensured)
			if _, err := ensured.Ensure(t.Context()); err != nil {
				t.Fatal(err)
			}
			if after := ensureState(t, ensured); !slices.Equal(before, after) {
				t.Fatalf("repeat ensure changed configured state: before=%q after=%q", before, after)
			}
			if test.keepSession {
				kept, err := ensured.SessionByName(t.Context(), sessions[0])
				if err != nil {
					t.Fatal(err)
				}
				if _, err := kept.NewWindow(t.Context(), tmux.NewWindowRequest{Name: new("after-ensure"), Command: "cat"}); err != nil {
					t.Fatalf("ordinary operation on retained session: %v", err)
				}
			} else if _, err := ensured.NewSession(t.Context(), tmux.NewSessionRequest{Name: "after-ensure", Command: "cat"}); err != nil {
				t.Fatalf("ordinary operation after configured startup: %v", err)
			}
		})
	}
}

func TestEnsureUnclassifiedFailureRetainsOnlySessionCleanup(t *testing.T) {
	path, flag := lifecycleFailureWrapper(t, "exit-empty*|*kill-session")
	config := "new-session -d -s configured -n original cat\n" + ensureScrubMarkerConfig(t)
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path, Config: []byte(config)})
	t.Cleanup(func() {
		if err := os.Remove(flag); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove failure injection: %v", err)
		}
	})
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := server.Ensure(t.Context())
	failure, ok := errors.AsType[*tmux.AcquisitionError](err)
	if !ok || failure.Unknown || failure.Cause == nil || failure.Rollback == nil || failure.Cleanup == nil || !strings.HasPrefix(failure.ResourceID, "$") {
		t.Fatalf("unclassified startup lost accepted session cleanup: %#v %v", failure, err)
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	const format = "#{session_id}|#{window_id}|#{pane_id}|#{pane_pid}"
	before := ensureRows(t, server, "list-panes", "-t", "=configured", "-F", format)
	for range 2 {
		if err := failure.Cleanup.Close(); err != nil {
			t.Fatalf("retry accepted session cleanup: %v", err)
		}
	}
	if after := ensureRows(t, server, "list-panes", "-t", "=configured", "-F", format); !slices.Equal(before, after) {
		t.Fatalf("retry gained server destruction authority: before=%v after=%v", before, after)
	}
	if rows := ensureRows(t, server, "list-sessions", "-F", "#{session_name}"); !slices.Equal(rows, []string{"configured"}) {
		t.Fatalf("retry did not remove only its own bootstrap: %v", rows)
	}
}

func TestEnsureUnclassifiedCancellationPreservesConfiguration(t *testing.T) {
	config := "new-session -d -s configured -n original cat\n" + ensureScrubMarkerConfig(t)
	fixture := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Config: []byte(config)})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: fixture.SocketPath(), ConfigFile: fixture.ConfigFile(),
		CommandObserver: func(trace tmux.CommandTrace) {
			if trace.Subcommand == "show-options" && trace.ExitCode == 0 {
				cancel()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Ensure(ctx)
	failure, ok := errors.AsType[*tmux.AcquisitionError](err)
	if !ok || !errors.Is(err, context.Canceled) || failure.Unknown || failure.Rollback != nil || !strings.HasPrefix(failure.ResourceID, "$") {
		t.Fatalf("canceled unclassified startup lost session rollback: %#v %v", failure, err)
	}
	if rows := ensureRows(t, fixture, "list-sessions", "-F", "#{session_name}"); !slices.Equal(rows, []string{"configured"}) {
		t.Fatalf("cancellation removed configuration or retained bootstrap: %v", rows)
	}
	if rows := ensureRows(t, fixture, "show-options", "-sqv", "exit-empty"); !slices.Equal(rows, []string{"on"}) {
		t.Fatalf("cancellation changed unproven server options: %v", rows)
	}
}

func TestEnsureExternalStarterPreservesOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux-wrapper")
	flag := filepath.Join(dir, "compete")
	beforePath := filepath.Join(dir, "before-options")
	script := fmt.Sprintf(`#!/bin/sh
real=%s
case "$*" in *new-session*)
  if [ -f %q ]; then
    rm %q
    for argument do
      case "$argument" in -S*) socket=${argument#-S};; -f*) config=${argument#-f};; esac
    done
    env -i PATH="$PATH" "$real" -S "$socket" -f "$config" new-session -d -s unrelated cat || exit $?
    "$real" -S "$socket" set-option -s @libtmux_owner_generation 0123456789abcdef0123456789abcdef || exit $?
    "$real" -S "$socket" show-options -s > %q || exit $?
  fi;;
esac
exec "$real" "$@"
`, lifecycleTmuxShell(t), flag, flag, beforePath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
	ensured, err := server.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Join(ensureRows(t, ensured, "show-options", "-s"), "\n")
	if strings.TrimSpace(string(before)) != after {
		t.Fatalf("unrelated startup options changed: before=%q after=%q", before, after)
	}
	if rows := ensureRows(t, ensured, "list-sessions", "-F", "#{session_name}"); !slices.Equal(rows, []string{"unrelated"}) {
		t.Fatalf("unrelated startup lost its session or retained bootstrap: %v", rows)
	}
}

func ensureScrubMarkerConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scrub-startup-marker")
	script := fmt.Sprintf("#!/bin/sh\nenv | sed -n 's/^\\(LIBTMUX_START_[^=]*\\)=.*/\\1/p' | while IFS= read -r name; do %s -S \"$1\" set-environment -gu \"$name\"; done\n", lifecycleTmuxShell(t))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return "run-shell " + strconv.Quote(fmt.Sprintf("%q '#{socket_path}'", path)) + "\n"
}

func TestEnsurePreservesExistingState(t *testing.T) {
	for _, token := range []string{"", "0123456789abcdef0123456789abcdef", "malformed"} {
		t.Run("token="+token, func(t *testing.T) {
			server := tmuxtest.NewServer(t.Context(), t)
			ensureRows(t, server, "set-option", "-s", "exit-empty", "on")
			ensureRows(t, server, "set-option", "-g", "@user-setting", "keep this value")
			ensureRows(t, server, "set-option", "-g", "base-index", "7")
			ensureRows(t, server, "set-environment", "-g", "USER_SETTING", "preserved")
			ensureRows(t, server, "new-window", "-d", "-t", "work", "-n", "notes", "cat")
			ensureRows(t, server, "split-window", "-d", "-t", "work:notes", "cat")
			if token != "" {
				ensureRows(t, server, "set-option", "-s", "@libtmux_owner_generation", token)
			}
			before := ensureState(t, server)
			for range 2 {
				ensured, err := server.Ensure(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if !ensured.Equal(server) {
					t.Fatal("reuse changed endpoint")
				}
			}
			if after := ensureState(t, server); !slices.Equal(before, after) {
				t.Fatalf("reuse changed state:\nbefore=%q\nafter=%q", before, after)
			}
		})
	}
}

func TestEnsureConcurrentSharedHandles(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
	var wait sync.WaitGroup
	for range 5 {
		wait.Go(func() {
			ensured, err := server.Ensure(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			if rows := ensureRows(t, ensured, "list-sessions", "-F", "#{session_id}"); len(rows) != 0 {
				t.Errorf("ensure returned before bootstrap removal: %v", rows)
			}
		})
	}
	wait.Wait()
}

func TestEnsurePreCanceledDoesNotStart(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := server.Ensure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled ensure: %v", err)
	}
	if alive, err := server.IsAlive(t.Context()); err != nil || alive {
		t.Fatalf("pre-canceled ensure started a daemon: %t %v", alive, err)
	}
}

func TestEnsureFailedStartupRetainsCleanup(t *testing.T) {
	for _, boundary := range []string{"exit-empty", "kill-session", "list-sessions"} {
		for _, failRollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rollback=%t", boundary, failRollback), func(t *testing.T) {
				dir := t.TempDir()
				flag := filepath.Join(dir, "fail")
				path := filepath.Join(dir, "tmux-wrapper")
				// Only the Ensure-specific initialization starts failure injection.
				// Earlier FindOrCreate acquisition and identity reads stay native.
				script := fmt.Sprintf(`#!/bin/sh
case "$*" in *exit-empty*) touch %q;; esac
if [ -f %q ]; then
  case "$*" in *%s*) echo 'injected ensure failure' >&2; exit 77;; esac
  if [ %q = true ]; then
    case "$*" in *kill-server*) echo 'injected rollback failure' >&2; exit 78;; esac
  fi
fi
exec %s "$@"
`, flag, flag, boundary, strconv.FormatBool(failRollback), lifecycleTmuxShell(t))
				if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
				t.Cleanup(func() {
					if err := os.Remove(flag); err != nil && !errors.Is(err, os.ErrNotExist) {
						t.Errorf("remove failure injection: %v", err)
					}
				})
				_, err := server.Ensure(t.Context())
				failure, ok := errors.AsType[*tmux.AcquisitionError](err)
				if !ok || failure.Unknown || failure.Cause == nil || (failure.Rollback != nil) != failRollback || (failure.Cleanup != nil) != failRollback {
					t.Fatalf("startup failure lost recovery: %#v %v", failure, err)
				}
				if err := os.Remove(flag); err != nil {
					t.Fatal(err)
				}
				if failRollback {
					if alive, err := server.IsAlive(t.Context()); err != nil || !alive {
						t.Fatalf("failed rollback state: %t %v", alive, err)
					}
					for range 2 {
						if err := failure.Cleanup.Close(); err != nil {
							t.Fatalf("retry accepted cleanup: %v", err)
						}
					}
				}
				if alive, err := server.IsAlive(t.Context()); err != nil || alive {
					t.Fatalf("startup failure left daemon: %t %v", alive, err)
				}
			})
		}
	}
}

func TestEnsureCancellationAfterStartupRollsBack(t *testing.T) {
	fixture := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: fixture.SocketPath(), ConfigFile: fixture.ConfigFile(),
		CommandObserver: func(trace tmux.CommandTrace) {
			if trace.Subcommand == "set-option" && trace.ExitCode == 0 {
				cancel()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Ensure(ctx)
	failure, ok := errors.AsType[*tmux.AcquisitionError](err)
	if !ok || failure.Unknown || failure.Rollback != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup rollback: %#v %v", failure, err)
	}
	if alive, err := fixture.IsAlive(t.Context()); err != nil || alive {
		t.Fatalf("canceled startup retained daemon: %t %v", alive, err)
	}
}

func TestEnsureAcquisitionFailureRetainsCleanup(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "fail")
	path := filepath.Join(dir, "tmux-wrapper")
	script := fmt.Sprintf(`#!/bin/sh
if [ -f %q ]; then
  case "$*" in *show-environment*LIBTMUX_START_*|*kill-session*)
    echo 'injected acquisition and rollback failure' >&2; exit 79;;
  esac
fi
exec %s "$@"
`, flag, lifecycleTmuxShell(t))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Binary: path})
	t.Cleanup(func() {
		if err := os.Remove(flag); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove failure injection: %v", err)
		}
	})
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := server.Ensure(t.Context())
	failure, ok := errors.AsType[*tmux.AcquisitionError](err)
	if !ok || failure.Unknown || failure.Cause == nil || failure.Rollback == nil || failure.Cleanup == nil {
		t.Fatalf("pre-result acquisition lost accepted cleanup: %#v %v", failure, err)
	}
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	if rows := ensureRows(t, server, "list-sessions", "-F", "#{session_id}"); len(rows) != 1 || rows[0] != failure.ResourceID {
		t.Fatalf("retry owner lost original session: %v, receipt=%q", rows, failure.ResourceID)
	}
	if err := failure.Cleanup.Close(); err != nil {
		t.Fatalf("retry acquired session cleanup: %v", err)
	}
}

func ensureRows(t *testing.T, server tmux.Server, args ...string) []string {
	t.Helper()
	result, err := server.Cmd(t.Context(), args...)
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("%v: %v result=%#v", args, err, result)
	}
	return result.Stdout
}

func ensureState(t *testing.T, server tmux.Server) []string {
	t.Helper()
	var state []string
	hasSessions := len(ensureRows(t, server, "list-sessions", "-F", "#{session_id}")) != 0
	for _, args := range [][]string{
		{"display-message", "-p", "#{pid}|#{start_time}|#{socket_path}"},
		{"list-sessions", "-F", "#{session_id}|#{session_name}|#{session_windows}"},
		{"list-windows", "-a", "-F", "#{session_id}|#{window_id}|#{window_index}|#{window_name}"},
		{"list-panes", "-a", "-F", "#{session_id}|#{window_id}|#{pane_id}|#{pane_pid}"},
		{"show-options", "-s"},
		{"show-options", "-g"},
		{"show-options", "-gw"},
		{"show-environment", "-g"},
	} {
		state = append(state, strings.Join(args, " "))
		if !hasSessions && (args[0] == "list-windows" || args[0] == "list-panes") {
			continue
		}
		state = append(state, ensureRows(t, server, args...)...)
	}
	return state
}
