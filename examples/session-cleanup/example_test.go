//go:build unix

package main_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestQuickstartProgram(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	parent := os.Environ()
	for _, source := range []string{"source", "readme"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			target := "."
			var displayed string
			if source == "readme" {
				readme, err := os.ReadFile("README.md")
				if err != nil {
					t.Fatal(err)
				}
				_, block, ok := strings.Cut(string(readme), "<!-- docs:session-cleanup -->")
				if !ok {
					t.Fatal("README has no quickstart source marker")
				}
				_, block, ok = strings.Cut(block, "```go\n")
				if !ok {
					t.Fatal("README quickstart has no Go code fence")
				}
				displayed, _, ok = strings.Cut(block, "\n```")
				if !ok {
					t.Fatal("README quickstart has no closing code fence")
				}
				target = filepath.Join(dir, "main.go")
				if err := os.WriteFile(target, []byte(displayed+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(dir, "quickstart")
			build := exec.CommandContext(ctx, "go", "build", "-o", binary, target)
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build %s program without scaffolding: %v\n%s", source, err, output)
			}
			if source == "readme" {
				program, err := os.ReadFile("main.go")
				if err != nil {
					t.Fatal(err)
				}
				lines := slices.DeleteFunc(strings.Split(string(program), "\n"), func(line string) bool {
					return strings.HasPrefix(strings.TrimSpace(line), "// docs:")
				})
				if strings.TrimSpace(displayed) != strings.TrimSpace(strings.Join(lines, "\n")) {
					t.Fatal("README quickstart differs from the complete ordinary program")
				}
			}
			for _, selector := range []string{"path", "name"} {
				for _, failure := range []string{"none", "body", "cleanup", "body-and-cleanup"} {
					t.Run(selector+"/"+failure, func(t *testing.T) {
						runQuickstartProgram(t, binary, realTmux, selector, failure)
					})
				}
			}
		})
	}
	if !slices.Equal(os.Environ(), parent) {
		t.Fatal("example harness mutated the host environment")
	}
}

func runQuickstartProgram(t *testing.T, binary, realTmux, selector, failure string) {
	t.Helper()
	const base = "/tmp/libtmux-go-test"
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	//nolint:usetesting // Unix sockets need a short path in the private test namespace.
	root, err := os.MkdirTemp(base, "example-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove example root: %v", err)
		}
	})
	environment := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR", "LIBTMUX_SOCKET_PATH", "LIBTMUX_SOCKET_NAME":
			return true
		}
		return false
	})
	environment = append(environment, "TMUX_TMPDIR="+root)
	if selector == "path" {
		environment = append(environment, "LIBTMUX_SOCKET_PATH="+filepath.Join(root, "example.sock"))
	} else {
		environment = append(environment, "LIBTMUX_SOCKET_NAME=example")
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary: realTmux, ConfigFile: "/dev/null", ProcessEnvironment: environment,
	})
	if err != nil {
		t.Fatal(err)
	}
	var daemonPID int
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Kill(ctx); err != nil && !errors.Is(err, tmux.ErrNoServer) {
			t.Errorf("clean up example daemon: %v", err)
		}
		if daemonPID > 0 {
			if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(context.Context) (bool, error) {
				return errors.Is(syscall.Kill(daemonPID, 0), syscall.ESRCH), nil
			}); err != nil {
				t.Errorf("example daemon process survived cleanup: %v", err)
			}
		}
		if alive, err := server.IsAlive(ctx); err != nil || alive {
			t.Errorf("example daemon remains after cleanup: alive=%t, error=%v", alive, err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "keeper", Command: "cat"}); err != nil {
		t.Fatal(err)
	}

	pid, err := server.Cmd(ctx, "display-message", "-p", "#{pid}")
	if err != nil || pid.ExitCode != 0 || len(pid.Stdout) != 1 {
		t.Fatalf("query example daemon PID: %v", err)
	}
	daemonPID, err = strconv.Atoi(pid.Stdout[0])
	if err != nil || daemonPID <= 0 {
		t.Fatalf("invalid example daemon PID: %v", err)
	}

	// Intercept only the failure boundary. The same compiled program creates
	// and removes a real session, and receives selectors only through its child environment.
	wrapper := filepath.Join(root, "tmux")
	if err := os.WriteFile(wrapper, []byte(tmuxBoundaryWrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(root, "trace")
	child := exec.CommandContext(ctx, binary)
	child.Env = append(slices.Clone(environment),
		"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TMUX=ignored,malformed,context", "TMUX_PANE=%999",
		"EXAMPLE_REAL_TMUX="+realTmux, "EXAMPLE_SOCKET="+server.SocketPath(),
		"EXAMPLE_TRACE="+trace, "EXAMPLE_FAILURE="+failure,
	)
	output, runErr := child.CombinedOutput()
	if failure == "none" {
		if runErr != nil || string(output) != "libtmux ready\n" {
			t.Fatalf("ordinary example: %v, output %q", runErr, output)
		}
	} else if failure == "cleanup" {
		if runErr == nil || !strings.Contains(string(output), "libtmux ready") {
			t.Fatalf("successful body with cleanup failure: %v, output %q", runErr, output)
		}
	} else if runErr == nil || !strings.Contains(string(output), "create window") {
		t.Fatalf("body failure was lost: %v, output %q", runErr, output)
	}
	if (failure == "cleanup" || failure == "body-and-cleanup") && !strings.Contains(string(output), "kill-session") {
		t.Fatalf("cleanup failure was lost: %q", output)
	}
	observed, err := os.ReadFile(trace)
	if err != nil || string(observed) != "session-created\nsession-cleanup\n" {
		t.Fatalf("real operation trace = %q, error %v", observed, err)
	}
	found, err := server.HasSession(ctx, tmux.HasSessionRequest{Target: "libtmux-go-quickstart"})
	if err != nil || found != (failure == "cleanup" || failure == "body-and-cleanup") {
		t.Fatalf("example session after return: found=%t, error=%v", found, err)
	}
	if found, err := server.HasSession(ctx, tmux.HasSessionRequest{Target: "keeper"}); err != nil || !found {
		t.Fatalf("example removed the harness session: found=%t, error=%v", found, err)
	}
}

const tmuxBoundaryWrapper = `#!/bin/sh
if [ "${TMUX+x}" = x ] || [ "${TMUX_PANE+x}" = x ]; then
  echo 'tmux context reached child' >&2
  exit 90
fi
for argument do
  case "$argument" in
    *new-window*)
      "$EXAMPLE_REAL_TMUX" -S "$EXAMPLE_SOCKET" has-session -t '=libtmux-go-quickstart' || exit 91
      printf 'session-created\n' >> "$EXAMPLE_TRACE"
      if [ "$EXAMPLE_FAILURE" = body ] || [ "$EXAMPLE_FAILURE" = body-and-cleanup ]; then
        echo 'deliberate example body failure' >&2
        exit 92
      fi
      ;;
    *kill-session*)
      printf 'session-cleanup\n' >> "$EXAMPLE_TRACE"
      if [ "$EXAMPLE_FAILURE" = cleanup ] || [ "$EXAMPLE_FAILURE" = body-and-cleanup ]; then
        echo 'deliberate example cleanup failure' >&2
        exit 93
      fi
      ;;
  esac
done
exec "$EXAMPLE_REAL_TMUX" "$@"
`
