package mcp_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

const executableFixtureEnvironment = "LIBTMUX_MCP_TEST_EXECUTABLE"

const (
	fixtureVersion31   = "version-3.1"
	fixtureVersion32a  = "version-3.2a"
	fixtureVersion33a  = "version-3.3a"
	fixtureVersion36   = "version-3.6"
	fixtureUnavailable = "unavailable"
	fixtureNoServer    = "no-server"
	fixtureHang        = "hang"
	// fixtureOldDaemonAppears is a 3.3a executable on a socket with no
	// server until the file named by flipFileEnvironment exists, then a 3.2a
	// daemon answers there.
	fixtureOldDaemonAppears = "old-daemon-appears"
)

const flipFileEnvironment = "LIBTMUX_MCP_TEST_FLIP_FILE"

func runExecutableFixture() {
	mode := os.Getenv(executableFixtureEnvironment)
	if mode == "" {
		return
	}
	if mode == fixtureOldDaemonAppears {
		runOldDaemonAppears()
	}
	if slices.Equal(os.Args[1:], []string{"-V"}) {
		version := "3.6"
		switch mode {
		case fixtureVersion31:
			version = "3.1"
		case fixtureVersion32a:
			version = "3.2a"
		case fixtureVersion33a:
			version = "3.3a"
		}
		fmt.Println("tmux " + version)
		os.Exit(0)
	}
	if mode == fixtureVersion31 || mode == fixtureVersion32a ||
		mode == fixtureVersion33a || mode == fixtureVersion36 {
		for _, argument := range os.Args[1:] {
			if strings.HasPrefix(argument, "libtmux-version-") && strings.Contains(argument, "#{q:pid}") {
				fmt.Fprintln(os.Stderr, "no server running on version fixture socket")
				os.Exit(1)
			}
		}
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

// runOldDaemonAppears answers the version probe the way a 3.2a daemon does
// once the flip file exists, and as an empty socket before.
func runOldDaemonAppears() {
	if slices.Equal(os.Args[1:], []string{"-V"}) {
		fmt.Println("tmux 3.3a")
		os.Exit(0)
	}
	if _, err := os.Stat(os.Getenv(flipFileEnvironment)); err == nil {
		for _, argument := range os.Args[1:] {
			marker, _, found := strings.Cut(argument, "#{")
			if !strings.HasPrefix(argument, "libtmux-version-") || !found {
				continue
			}
			record := "123|456|3.2a|/tmp/fixture.sock="
			if strings.Index(argument, "#{q:version}") < strings.Index(argument, "#{q:pid}") {
				record = "3.2a|123|456|/tmp/fixture.sock="
			}
			fmt.Fprintln(os.Stderr, "unknown command: "+marker+record)
			os.Exit(1)
		}
	}
	fmt.Fprintln(os.Stderr, "no server running on test socket")
	os.Exit(1)
}
