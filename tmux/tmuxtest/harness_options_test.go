//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxtest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestNewServerWithOptionsStartsOnlyWhenRequested(t *testing.T) {
	var socketPath string
	var pid int
	t.Run("bare server lifetime", func(t *testing.T) {
		server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{})
		socketPath = server.SocketPath()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		alive, err := server.IsAlive(ctx)
		if err != nil {
			t.Fatalf("IsAlive() error = %v", err)
		}
		if alive {
			t.Fatal("bare server is alive before explicit session creation")
		}
		if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "manual"}); err != nil {
			t.Fatalf("NewSession() error = %v", err)
		}
		pid = mustPID(t, server)
	})

	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after cleanup: %v", err)
	}
	if processExists(pid) {
		t.Fatalf("tmux server pid %d remains alive after cleanup", pid)
	}
}

func TestNewServerWithOptionsAddsConfigToIsolationFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, ".tmux.conf"),
		[]byte("set -g @user_config leaked\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	initial := tmux.NewSessionRequest{Name: "configured"}
	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		Config:         []byte("set -g @request_config loaded\n"),
		InitialSession: &initial,
	})
	if got := mustCmd(t, server, "show-option", "-gv", "@request_config"); got != "loaded" {
		t.Fatalf("request config value = %q, want loaded", got)
	}
	result := mustResult(t, server, "show-option", "-gv", "@user_config")
	if slices.Contains(result.Stdout, "leaked") {
		t.Fatalf("isolated server loaded user config: %#v", result)
	}
}

func TestNewServerWithOptionsCopiesInputsAndPreservesEmptyEnvironment(t *testing.T) {
	config := []byte("set -g @copy_probe before\n")
	environment := []string{"LIBTMUX_COPY_PROBE=before"}
	initial := tmux.NewSessionRequest{Name: "before"}
	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		Config:             config,
		ProcessEnvironment: environment,
		InitialSession:     &initial,
	})
	config[0] = 'X'
	environment[0] = "LIBTMUX_COPY_PROBE=after"
	initial.Name = "after"

	contents, err := os.ReadFile(server.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "set -g @copy_probe before\n") {
		t.Fatalf("config contents = %q, want copied request config", contents)
	}
	if got := server.ProcessEnvironment(); !slices.Equal(got, []string{"LIBTMUX_COPY_PROBE=before"}) {
		t.Fatalf("ProcessEnvironment() = %#v, want copied input", got)
	}
	if got := mustCmd(t, server, "display-message", "-p", "#{session_name}"); got != "before" {
		t.Fatalf("initial session name = %q, want before", got)
	}

	empty := make([]string, 0)
	bare := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{
		ProcessEnvironment: empty,
	})
	if got := bare.ProcessEnvironment(); got == nil || len(got) != 0 {
		t.Fatalf("explicit empty ProcessEnvironment() = %#v, want non-nil empty", got)
	}
}

func TestNewServerWithOptionsScrubsInheritedTmuxEnvironment(t *testing.T) {
	t.Setenv("LIBTMUX_INHERITED_PROBE", "kept")
	t.Setenv("TMUX", "/tmp/foreign,123,0")
	t.Setenv("TMUX_PANE", "%9")
	t.Setenv("LIBTMUX_SOCKET", "foreign")
	t.Setenv("LIBTMUX_SOCKET_PATH", "/tmp/foreign/sock")

	server := tmuxtest.NewServerWithOptions(context.Background(), t, tmuxtest.ServerOptions{})
	environment := server.ProcessEnvironment()
	if !slices.Contains(environment, "LIBTMUX_INHERITED_PROBE=kept") {
		t.Fatalf("ProcessEnvironment() omitted inherited probe: %#v", environment)
	}
	if want := "TMUX_TMPDIR=" + os.Getenv("TMUX_TMPDIR"); !slices.Contains(environment, want) {
		t.Fatalf("ProcessEnvironment() dropped TMUX_TMPDIR, which the suite points at its root")
	}
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key == "TMUX" || key == "TMUX_PANE" || strings.HasPrefix(key, "LIBTMUX_SOCKET") {
			t.Fatalf("ProcessEnvironment() retained selector variable %q", key)
		}
	}
}
