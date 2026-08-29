// Package exampletest captures output from executable examples.
package exampletest

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

// RequireTmux skips a real-tmux example below its stated feature floor.
func RequireTmux(
	t *testing.T,
	ctx context.Context,
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

	if workErr != nil {
		t.Fatalf("run() error = %v; printed so far:\n%s", workErr, printed.String())
	}
	return printed.String()
}
