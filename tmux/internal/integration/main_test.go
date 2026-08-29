// Package integration tests the public tmux API against a real server.
package integration

import (
	"os"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// TestMain gives these tests the harness the root package's own TestMain gives
// the ones that stayed: a short temporary root, and cleanup that verifies each
// tmux server died before removing its socket.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}
