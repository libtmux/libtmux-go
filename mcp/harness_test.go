package mcp_test

import (
	"os"
	"testing"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	runExecutableFixture()
	if err := os.Setenv(tmuxmcp.ToolsetsEnvironmentVariable, "inspect,manage,execute,teardown"); err != nil {
		panic(err)
	}
	os.Exit(tmuxtest.Main(m))
}
