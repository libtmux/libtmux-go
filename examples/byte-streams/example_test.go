package main

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestByteStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	archive := filepath.Join(t.TempDir(), "screen.gz")
	printed := exampletest.Output(t, func() error {
		return run(ctx, tmuxtest.NewServer(ctx, t), archive)
	})

	if want := "pasted 42 bytes"; !strings.Contains(printed, want) {
		t.Errorf("printed %q, want it to contain %q", printed, want)
	}

	// The archive proves both directions: the payload reached the pane without
	// a shell quoting it, and the screen came back out compressed.
	file, err := os.Open(archive)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", archive, err)
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	screen, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if want := "'quoted'"; !strings.Contains(string(screen), want) {
		t.Errorf("archived screen = %q, want it to contain %q", screen, want)
	}
}
