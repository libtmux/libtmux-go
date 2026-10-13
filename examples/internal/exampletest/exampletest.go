// Package exampletest captures output from executable examples.
package exampletest

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
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
