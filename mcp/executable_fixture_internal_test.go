package mcp

import (
	"os"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

const (
	fixtureVersion35   = "version-3.5"
	fixtureVersion36   = "version-3.6"
	fixtureUnavailable = "unavailable"
	fixtureNoServer    = "no-server"
	fixtureHang        = "hang"
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
	)
	return options
}
