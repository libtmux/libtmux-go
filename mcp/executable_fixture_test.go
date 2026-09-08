package mcp_test

import (
	"fmt"
	"os"
	"slices"
	"time"
)

const executableFixtureEnvironment = "LIBTMUX_MCP_TEST_EXECUTABLE"

const (
	fixtureVersion31   = "version-3.1"
	fixtureVersion32a  = "version-3.2a"
	fixtureVersion36   = "version-3.6"
	fixtureUnavailable = "unavailable"
	fixtureNoServer    = "no-server"
	fixtureHang        = "hang"
)

func runExecutableFixture() {
	mode := os.Getenv(executableFixtureEnvironment)
	if mode == "" {
		return
	}
	if slices.Equal(os.Args[1:], []string{"-V"}) {
		version := "3.6"
		switch mode {
		case fixtureVersion31:
			version = "3.1"
		case fixtureVersion32a:
			version = "3.2a"
		}
		fmt.Println("tmux " + version)
		os.Exit(0)
	}
	if mode == fixtureHang {
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == fixtureNoServer {
		fmt.Fprintln(os.Stderr, "no server running on test socket")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "tmux fixture is unavailable")
	os.Exit(1)
}
