// Package exampletest captures output from executable examples.
package exampletest

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// RequireTmux skips a real-tmux example below its stated feature floor.
func RequireTmux(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	minimum string,
) {
	t.Helper()
	want, err := tmux.ParseVersion(minimum)
	if err != nil {
		t.Fatalf("parse required tmux version %q: %v", minimum, err)
	}
	got, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("query tmux version: %v", err)
	}
	if !got.AtLeast(want) {
		t.Skipf("example requires tmux %s or newer; installed %s", want, got)
	}
}

// Program runs a self-contained example the way its reader does and holds what
// it prints to output.txt, the file its page publishes as the program's
// output. The comparison is exact, so the page cannot say more or less than
// the program does.
func Program(t *testing.T, run func(context.Context) error) {
	t.Helper()
	PrivateSockets(t)

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	printed := Output(t, func() error { return run(ctx) })
	MatchesFile(t, printed, "output.txt")
}

// Server runs an example that takes its tmux server from its caller, on a
// harness-owned server holding nothing else, and holds what it prints to
// output.txt. A reader running the program reaches their own server, which
// may hold more, so the page publishes what a server with nothing else on it
// prints.
func Server(t *testing.T, run func(context.Context, tmux.Server) error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	printed := Output(t, func() error { return run(ctx, server) })
	MatchesFile(t, printed, "output.txt")
}

// PrivateSockets gives the rest of the test a tmux socket directory of its
// own. A program that builds its server itself, rather than taking one from
// the harness, then reaches neither the developer's tmux nor another test's.
// Whatever server the program left running is killed before the directory is
// removed.
func PrivateSockets(t *testing.T) {
	t.Helper()

	// t.TempDir names the test, and a socket path may hold only 103 bytes.
	directory, err := os.MkdirTemp("", "ex") //nolint:usetesting // see above
	if err != nil {
		t.Fatalf("exampletest: create socket directory: %v", err)
	}
	t.Setenv("TMUX_TMPDIR", directory)
	t.Cleanup(func() {
		sockets, _ := filepath.Glob(filepath.Join(directory, "tmux-*", "*"))
		for _, socket := range sockets {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
			cancel()
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("exampletest: remove socket directory: %v", err)
		}
	})
}

// Output returns work's stdout and fails with partial output when work fails.
func Output(t *testing.T, work func() error) string {
	t.Helper()

	printed, err := OutputAndError(t, work)
	if err != nil {
		t.Fatalf("run() error = %v; printed so far:\n%s", err, printed)
	}
	return printed
}

// OutputAndError returns work's stdout together with its error, for a test
// whose subject is how an example fails.
func OutputAndError(t *testing.T, work func() error) (string, error) {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("exampletest: create pipe: %v", err)
	}
	os.Stdout = writer

	// Drain concurrently so output larger than the pipe buffer cannot block.
	var printed bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&printed, reader)
	}()

	workErr := work()

	os.Stdout = original
	if err := writer.Close(); err != nil {
		t.Fatalf("exampletest: close writer: %v", err)
	}
	<-drained
	if err := reader.Close(); err != nil {
		t.Fatalf("exampletest: close reader: %v", err)
	}
	return printed.String(), workErr
}

// SlowShell returns tmux configuration under which every pane's shell takes a
// moment to start, as a login shell with a heavy profile does. The shell then
// prints its first prompt after an example has typed, so the reply shares the
// prompt's line. A machine whose shell starts at once never produces that
// order, and an example that only works in the other one passes there.
func SlowShell(t *testing.T) []byte {
	t.Helper()
	// The delay is the condition under test, not a wait for one.
	return shellConfig(t, "sleep 0.2\nexec /bin/sh \"$@\"\n")
}

// MuteShell returns tmux configuration under which a pane's shell reads what
// is typed and never answers. The terminal still echoes the typing, so an
// example that finishes here took its own echo for a reply.
func MuteShell(t *testing.T) []byte {
	t.Helper()
	return shellConfig(t, "exec cat >/dev/null\n")
}

func shellConfig(t *testing.T, body string) []byte {
	t.Helper()
	script := filepath.Join(t.TempDir(), "shell")
	//nolint:gosec // tmux runs the fixture, so it has to be executable.
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("exampletest: write shell fixture: %v", err)
	}
	return []byte("set -g default-shell \"" + script + "\"\n")
}

// MatchesFile fails unless printed equals the file at path. The README quotes
// that file as what the program prints, so this comparison is what keeps the
// quoted output true.
func MatchesFile(t *testing.T, printed, path string) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("exampletest: read expected output: %v", err)
	}
	if printed != string(want) {
		t.Errorf("printed:\n%s\nwant the contents of %s:\n%s", printed, path, want)
	}
}
