// Package integration holds the tests that drive a real tmux through the
// public API.
//
// They live here rather than beside the package they exercise for one reason:
// they import it the way any other program does, so nothing about them needs to
// be in its directory. What is left at the root is what has to be — the tests
// that reach unexported identifiers, and the files whose Example functions
// pkg.go.dev renders on the package page.
package integration

import (
	"os"
	"testing"

	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// TestMain gives these tests the harness the root package's own TestMain gives
// the ones that stayed: a short temporary root, and cleanup that verifies each
// tmux server died before removing its socket.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}
