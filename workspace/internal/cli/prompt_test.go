//go:build unix

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromptCancellationPreservesInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := &invocation{ctx: ctx, in: reader, err: io.Discard}
	done := make(chan error, 1)
	go func() {
		_, err := r.prompt("Confirm", "no")
		done <- err
	}()
	if _, err := io.WriteString(writer, "partial"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled prompt: %v", err)
		}
	case <-time.After(time.Second):
		_ = writer.Close()
		<-done
		t.Fatal("prompt ignored cancellation while input remained open")
	}
	if _, err := reader.Stat(); err != nil {
		t.Fatalf("prompt closed caller input: %v", err)
	}
	if r.in != reader {
		t.Fatal("prompt replaced caller input with a buffered reader")
	}
}

func TestPromptPreservesFollowingInput(t *testing.T) {
	input := strings.NewReader("yes\nnext\n")
	r := &invocation{ctx: t.Context(), in: input, err: io.Discard}
	answer, err := r.prompt("Confirm", "no")
	if err != nil || answer != "yes" {
		t.Fatalf("prompt answer: %q %v", answer, err)
	}
	remaining, err := io.ReadAll(input)
	if err != nil || string(remaining) != "next\n" || r.in != input {
		t.Fatalf("prompt consumed later input: %q %v", remaining, err)
	}
}

func TestPromptFileAnswerBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers")
	if err := os.WriteFile(path, []byte("yes\n\nlast"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	r := &invocation{ctx: t.Context(), in: input, err: io.Discard}
	for _, want := range []string{"yes", "default", "last"} {
		answer, err := r.prompt("Answer", "default")
		if err != nil || answer != want {
			t.Fatalf("answer = %q, %v; want %q", answer, err, want)
		}
	}
	if _, err := r.prompt("Answer", "default"); err == nil {
		t.Fatal("empty input silently supplied a default answer")
	}
}
