package tmuxcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRunnerStreamsOutputToCallerFilesWithoutCapturing(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	stdout := createStreamTestFile(t, filepath.Join(directory, "stdout"))
	stderr := createStreamTestFile(t, filepath.Join(directory, "stderr"))
	request := helperRequest("lines")
	request.Stdio = &Stdio{Stdout: stdout, Stderr: stderr}

	result, err := (Runner{}).Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("Run() exit code = %d, want 7", result.ExitCode)
	}
	if result.Stdout != nil || result.Stderr != nil || result.RawStdout != nil {
		t.Fatalf("streamed result captured output: %#v", result)
	}
	if err := stdout.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Sync(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(stdout.Name()); err != nil ||
		!slices.Equal(got, []byte("one\n\nthree\n\n")) {
		t.Fatalf("streamed stdout = (%q, %v)", got, err)
	}
	if got, err := os.ReadFile(stderr.Name()); err != nil ||
		!slices.Equal(got, []byte("warning\n\nsecond\n")) {
		t.Fatalf("streamed stderr = (%q, %v)", got, err)
	}
}

func TestRunnerStreamingHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	output := createStreamTestFile(t, filepath.Join(t.TempDir(), "output"))
	request := helperRequest("block")
	request.Stdio = &Stdio{Stdout: output, Stderr: output}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	result, err := (Runner{}).Run(ctx, request)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("Run() exit code = %d after cancellation", result.ExitCode)
	}
}

func createStreamTestFile(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close %s: %v", filepath.Base(path), err)
		}
	})
	return file
}
