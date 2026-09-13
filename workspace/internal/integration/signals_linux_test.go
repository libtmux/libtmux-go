//go:build linux

package integration

import (
	"bytes"
	"context"
	"encoding/json"
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
			marker := filepath.Join(directory, "script-pid")
			script := write(t, directory, "before.sh", "printf '%s' \"$$\" > "+strconv.Quote(marker)+"\nexec sleep 30\n")
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
			var scriptPID int
			for ctx.Err() == nil {
				content, readErr := os.ReadFile(marker)
				if readErr == nil {
					scriptPID, err = strconv.Atoi(string(content))
					if err == nil && scriptPID > 0 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if scriptPID <= 0 {
				t.Fatalf("script readiness: %v", ctx.Err())
			}
			child, err := os.FindProcess(scriptPID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = child.Kill()
				_ = child.Release()
			})
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			waited = true
			if err := child.Signal(syscall.Signal(0)); err == nil {
				t.Errorf("%s left the setup script running", signal)
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
