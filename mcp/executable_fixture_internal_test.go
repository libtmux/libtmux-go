package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

const (
	fixtureVersion31   = "version-3.1"
	fixtureVersion32a  = "version-3.2a"
	fixtureVersion33a  = "version-3.3a"
	fixtureVersion36   = "version-3.6"
	fixtureUnavailable = "unavailable"
	fixtureNoServer    = "no-server"
	fixtureHang        = "hang"

	fixtureOldDaemonAppears = "old-daemon-appears"
	flipFileEnvironment     = "LIBTMUX_MCP_TEST_FLIP_FILE"
)

func executableFixtureOptions(
	t testing.TB,
	mode string,
	options tmux.ServerOptions,
) tmux.ServerOptions {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options.Binary = executable
	options.ProcessEnvironment = append(
		options.ProcessEnvironment,
		"LIBTMUX_MCP_TEST_EXECUTABLE="+mode,
		"GOCOVERDIR="+t.TempDir(),
	)
	return options
}

func testTmuxExecutable(t testing.TB) string {
	t.Helper()
	candidate := "tmux"
	for _, variable := range []string{"LIBTMUX_TEST_TMUX", "LIBTMUX_TMUX"} {
		if value := os.Getenv(variable); strings.TrimSpace(value) != "" {
			candidate = value
			break
		}
	}
	resolved, err := exec.LookPath(candidate)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
