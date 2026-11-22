// Package integration tests the public tmux API against a real server.
package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// TestMain gives these tests the harness the root package's own TestMain gives
// the ones that stayed: a short temporary root, and cleanup that verifies each
// tmux server died before removing its socket.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func lookupTestTmux() (string, error) {
	binary := "tmux"
	for _, variable := range []string{"LIBTMUX_TEST_TMUX", "LIBTMUX_TMUX"} {
		if value := os.Getenv(variable); strings.TrimSpace(value) != "" {
			binary = value
			break
		}
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func testTmuxBinary(t testing.TB) string {
	t.Helper()
	binary, err := lookupTestTmux()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}
