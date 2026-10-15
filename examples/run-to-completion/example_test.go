package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestRunToCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	printed := exampletest.Output(t, func() error { return run(ctx, server) })
	// The streamed lines are the terminal's bytes and end "\r\n"; the page
	// shows them as a terminal draws them.
	printed = strings.ReplaceAll(printed, "\r\n", "\n")

	published, err := os.ReadFile("output.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := string(published)
	// tmux before 3.3 does not say which signal ended a pane, so the last
	// line the program prints is shorter there than the one published.
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if named, _ := tmux.ParseVersion("3.3"); !version.AtLeast(named) {
		want = strings.Replace(want, "stopped: killed\n", "stopped\n", 1)
	}
	if printed != want {
		t.Errorf("printed:\n%s\nwant the contents of output.txt:\n%s", printed, want)
	}
}
