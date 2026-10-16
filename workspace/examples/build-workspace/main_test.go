package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// hostileConfig is a tmux configuration unlike the one the page was written
// against. The program runs with it as the user's own, so output that
// depends on what a reader has configured fails here rather than for them.
const hostileConfig = "set -g base-index 5\n" +
	"set -g pane-base-index 5\n" +
	"set-hook -g after-new-session 'new-window -d'\n"

// TestBuildWorkspacePrintsItsPublishedOutput runs the program against a real
// tmux and holds what it prints to output.txt, which its page and the package
// page publish.
func TestBuildWorkspacePrintsItsPublishedOutput(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(hostileConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// t.TempDir names the test, and a socket path may hold only 103 bytes.
	sockets, err := os.MkdirTemp("", "ws") //nolint:usetesting // see above
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", sockets)
	t.Cleanup(func() {
		left, _ := filepath.Glob(filepath.Join(sockets, "tmux-*", "*"))
		for _, socket := range left {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
			cancel()
		}
		_ = os.RemoveAll(sockets)
	})

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = stdout })

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	runErr := run(ctx)
	os.Stdout = stdout
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	printed, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("run() error = %v\nit printed:\n%s", runErr, printed)
	}

	want, err := os.ReadFile("output.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(printed) != string(want) {
		t.Errorf("the program printed:\n%s\noutput.txt says it prints:\n%s", printed, want)
	}
}
