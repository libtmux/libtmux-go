package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
)

// TestAgentWorkflowPrintsItsPublishedOutput runs the program against a real
// tmux on a socket with nothing on it, and holds what it prints to output.txt,
// which its page and the package page publish.
//
// It takes about a second: the program starts work that sleeps for one, so
// that there is something to inspect while it runs.
func TestAgentWorkflowPrintsItsPublishedOutput(t *testing.T) {
	// The program drives whatever tmux it is pointed at, with that tmux's own
	// configuration and shell. A home with neither, and a plain shell, is the
	// server its page describes.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SHELL", "/bin/sh")
	// Unset rather than empty: the server refuses input when it cannot tell
	// whether it is running in a pane, and an empty TMUX is that.
	for _, name := range []string{"TMUX", "TMUX_PANE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(tmuxmcp.ToolsetsEnvironmentVariable, "inspect,manage,execute")
	// t.TempDir names the test, and a socket path may hold only 103 bytes.
	sockets, err := os.MkdirTemp("", "aw") //nolint:usetesting // see above
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
	runErr := run(ctx, "agent-workflow")
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
