package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func rejectedTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("LIBTMUX_REJECTED_TMUX")
	if binary == "" {
		t.Skip("set LIBTMUX_REJECTED_TMUX to an exact 3.2a or 3.3 binary for negative version-check coverage")
	}
	output, err := exec.CommandContext(t.Context(), binary, "-V").Output()
	if err != nil {
		t.Fatal(err)
	}
	if version := strings.TrimSpace(string(output)); version != "tmux 3.2a" && version != "tmux 3.3" {
		t.Fatalf("rejection fixture reports %q, want tmux 3.2a or tmux 3.3", version)
	}
	return binary
}

func versionNativeCommand(t *testing.T, binary, socket string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{"-f" + os.DevNull, "-S" + socket}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native fixture %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func TestCoreMinimumRejectsExplicitOlderDaemon(t *testing.T) {
	old := rejectedTmuxBinary(t)
	socket := filepath.Join(t.TempDir(), "s")
	versionNativeCommand(t, old, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() {
		versionNativeCommand(t, old, socket, "kill-server")
	})
	versionNativeCommand(t, old, socket, "set-buffer", "-b", "sentinel", "original")
	server, err := tmux.NewServer(tmux.ServerOptions{SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"raw read": func() error {
			_, err := server.Cmd(t.Context(), "list-sessions")
			return err
		},
		"raw batch": func() error {
			_, err := server.Cmd(t.Context(), "set-buffer", "-b", "sentinel", "changed", ";", "new-session", "-d", "-s", "forbidden")
			return err
		},
		"typed write": func() error {
			return server.SetBuffer(t.Context(), tmux.SetBufferRequest{Name: new("sentinel"), Data: "changed"})
		},
		"plan": func() error {
			var plan tmux.Plan
			plan.SetBuffer("sentinel", "changed")
			_, err := plan.Run(t.Context(), server)
			return err
		},
		"new connection": func() error {
			_, connection, err := server.NewSessionConnection(t.Context(), tmux.NewSessionRequest{Name: "forbidden"}, tmux.ConnectionOptions{})
			if connection != nil {
				_ = connection.Close()
				t.Fatal("unsupported daemon acquired a control connection")
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Fatalf("operation error = %v, want unsupported daemon refusal", err)
			}
			if got := versionNativeCommand(t, old, socket, "save-buffer", "-b", "sentinel", "-"); got != "original" {
				t.Fatalf("unsupported operation changed the sentinel to %q", got)
			}
			if got := versionNativeCommand(t, old, socket, "list-sessions", "-F", "#{session_name}"); got != "keeper\n" {
				t.Fatalf("unsupported operation changed the sessions to %q", got)
			}
		})
	}
}

func TestCoreMinimumFencesDaemonAppearingAtColdStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the race fixture uses a POSIX executable proxy")
	}
	old := rejectedTmuxBinary(t)
	modern, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.CommandContext(t.Context(), modern, "-V").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	socket := filepath.Join(root, "s")
	proxy := filepath.Join(root, "tmux-proxy")
	seen := filepath.Join(root, "probed")
	script := "#!/bin/sh\n" +
		"if [ \"$#\" = 1 ] && [ \"$1\" = -V ]; then\n" +
		strconv.Quote(old) + " -f /dev/null -S " + strconv.Quote(socket) + " new-session -d -s keeper 'sleep 300'\n" +
		"printf '%s\\n' " + strconv.Quote(strings.TrimSpace(string(version))) + "\nexit 0\nfi\n" +
		"if [ ! -e " + strconv.Quote(seen) + " ]; then\n" +
		"touch " + strconv.Quote(seen) + "\nprintf '%s\\n' 'no server running on fixture' >&2\nexit 1\nfi\n" +
		"exec " + strconv.Quote(modern) + " \"$@\"\n"
	if err := os.WriteFile(proxy, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		versionNativeCommand(t, old, socket, "kill-server")
	})
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: proxy, SocketPath: socket, ConfigFile: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Cmd(t.Context(), "new-session", "-d", "-s", "forbidden")
	if !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("cold-start race error = %v, want atomic refusal", err)
	}
	if got := versionNativeCommand(t, old, socket, "list-sessions", "-F", "#{session_name}"); got != "keeper\n" {
		t.Fatalf("cold-start race created a session: %q", got)
	}
}

func TestCoreMinimumBoundHandleDoesNotStartOlderExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the configuration marker uses a POSIX shell")
	}
	old := rejectedTmuxBinary(t)
	modern := os.Getenv("LIBTMUX_FLOOR_TMUX")
	if modern == "" {
		var err error
		modern, err = exec.LookPath("tmux")
		if err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	socket := filepath.Join(root, "s")
	marker := filepath.Join(root, "unexpected-startup")
	config := filepath.Join(root, "tmux.conf")
	contents := "run-shell " + strconv.Quote("printf started > "+strconv.Quote(marker)) + "\n"
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	versionNativeCommand(t, modern, socket, "new-session", "-d", "-s", "keeper", "sleep 300")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, old, "-N", "-S"+socket, "kill-server").Run()
	})
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: old, SocketPath: socket, ConfigFile: config})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stale := snapshot.Sessions()[0]
	bound := stale.Server()
	versionNativeCommand(t, modern, socket, "kill-server")
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := exec.CommandContext(t.Context(), modern, "-N", "-S"+socket, "list-sessions").Run()
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture daemon did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := bound.Cmd(t.Context(), "new-session", "-d", "-s", "forbidden")
	if err == nil && result.ExitCode == 0 {
		t.Fatal("a materialized handle started another daemon")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an older executable loaded configuration before refusal: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := bound.OpenControl(ctx, stale)
	if client != nil {
		_ = client.Close()
		t.Fatal("a stale handle acquired control on another daemon")
	}
	if err == nil {
		t.Fatal("control attachment to an absent daemon succeeded")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control startup loaded an older executable's configuration: %v", err)
	}
}
