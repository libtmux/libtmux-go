package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/examples/internal/exampletest"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

// TestFastPath runs the example itself against a real tmux.
//
// It builds the server from options rather than taking one from the harness,
// because the example counts the processes it starts and that runner has to be
// installed before the server exists. The socket still belongs to this test:
// tmuxtest.Main points TMPDIR at a short root of its own, so t.TempDir sits
// inside it and stays under the length a tmux socket path is allowed.
func TestFastPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	options := tmux.ServerOptions{SocketPath: filepath.Join(t.TempDir(), "tmux.sock")}
	printed := exampletest.Output(t, func() error { return run(ctx, options) })

	// The counts themselves move between tmux releases, so what is asserted is
	// the claim the example makes rather than the numbers it happened to print:
	// the same ten reads cost processes one way and fewer the other.
	overProcesses := countStarted(t, printed, "over tmux processes")
	overConnection := countStarted(t, printed, "over a connection")
	if overProcesses == 0 {
		t.Errorf("a process per command started none; printed:\n%s", printed)
	}
	if overConnection >= overProcesses {
		t.Errorf(
			"a connection started %d and a process per command started %d, "+
				"want the connection to start fewer; printed:\n%s",
			overConnection, overProcesses, printed,
		)
	}
}

// countStarted reads back the number the example printed on the labelled line.
func countStarted(t *testing.T, printed, label string) int {
	t.Helper()
	pattern := regexp.MustCompile(regexp.QuoteMeta(label) + `:\s+(\d+) started`)
	match := pattern.FindStringSubmatch(printed)
	if match == nil {
		t.Fatalf("printed no %q line; printed:\n%s", label, printed)
	}
	count, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("%q count %q is not a number: %v", label, match[1], err)
	}
	return count
}
