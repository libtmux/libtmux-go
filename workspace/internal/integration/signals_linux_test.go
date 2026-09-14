//go:build linux

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// readyMarker is what the setup script writes into the readiness pipe before
// it execs the command under test.
const readyMarker = "ready"

// scriptPipe reports the setup script's liveness without naming a process. The
// script holds the write end and execs a command that inherits it, so the read
// end delivers the marker once the script runs and end of file once every
// descendant holding it is gone. A process id would be ambiguous here: the
// script is a grandchild this test cannot reap, so it reads as running while a
// zombie and as running again if its number is reused.
func scriptPipe(t *testing.T, path string) *os.File {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	pipe, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pipe.Close() })
	return pipe
}

// waitForScript reads the readiness marker, treating end of file as the script
// not having opened the pipe yet.
func waitForScript(ctx context.Context, pipe *os.File) error {
	pending := []byte(readyMarker)
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := pipe.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			return err
		}
		n, err := pipe.Read(pending)
		pending = pending[n:]
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrDeadlineExceeded) {
			return err
		}
	}
	return nil
}

func TestCLISignalsCancelScriptsBeforeExit(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux-workspace")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/tmux-workspace")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			server := tmuxtest.NewServer(t.Context(), t)
			pid := daemonPID(t, server)
			before, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(before.Sessions()) != 1 || len(before.Windows()) != 1 || len(before.Panes()) != 1 {
				t.Fatal("signal fixture requires a nonempty keeper")
			}
			directory := t.TempDir()
			pipe := scriptPipe(t, filepath.Join(directory, "alive"))
			script := write(t, directory, "before.sh", "exec 3> "+strconv.Quote(filepath.Join(directory, "alive"))+"\nprintf '%s' "+readyMarker+" >&3\nexec sleep 30\n")
			config, err := json.Marshal(map[string]any{
				"session_name": "cancelled", "before_script": "sh " + strconv.Quote(script),
				"windows": []map[string]any{{"panes": []any{nil}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			path := write(t, directory, "workspace.json", string(config))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "load", "-d", "--ndjson", "-S", server.SocketPath(), "-f", server.ConfigFile(), path)
			var out, diagnostic bytes.Buffer
			command.Stdout, command.Stderr = &out, &diagnostic
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
			if err := waitForScript(ctx, pipe); err != nil {
				t.Fatalf("script readiness: %v", err)
			}
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			waited = true
			if err := pipe.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if n, err := pipe.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Errorf("%s left the setup script holding the pipe: read %d, %v", signal, n, err)
			}
			if command.ProcessState.ExitCode() != 130 || !strings.Contains(out.String(), `"event":"failed"`) || !strings.Contains(diagnostic.String(), `"code":"interrupted"`) {
				t.Errorf("signal result: %s stdout=%s stderr=%s", command.ProcessState, out.String(), diagnostic.String())
			}
			after, err := server.Snapshot(t.Context())
			if err != nil || daemonPID(t, server) != pid || len(after.Sessions()) != 1 || len(after.Windows()) != 1 || len(after.Panes()) != 1 {
				t.Fatalf("signal changed keeper topology: %v", err)
			}
			if after.Sessions()[0].ID() != before.Sessions()[0].ID() || after.Windows()[0].ID() != before.Windows()[0].ID() || after.Panes()[0].ID() != before.Panes()[0].ID() {
				t.Fatal("signal replaced keeper identity")
			}
		})
	}
}
