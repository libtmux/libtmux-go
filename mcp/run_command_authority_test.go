package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestRunCommandRouteRejectsASCIIControls(t *testing.T) {
	base := runCommandRoute{
		executable: "/usr/bin/tmux",
		socketPath: "/tmp/libtmux.sock",
		paneID:     "%1",
	}
	for _, field := range []string{"executable", "socket", "pane"} {
		for value := byte(0); value <= 0x7f; value++ {
			if value >= 0x20 && value != 0x7f {
				continue
			}
			route := base
			switch field {
			case "executable":
				route.executable += string(value)
			case "socket":
				route.socketPath += string(value)
			case "pane":
				route.paneID += string(value)
			}
			if err := validateRunCommandRoute(route); err == nil ||
				!strings.Contains(err.Error(), "ASCII control") {
				t.Fatalf("%s byte %#x route error = %v", field, value, err)
			}
		}
	}
}

func TestRunCommandRoutePreservesOpaqueSafeBytes(t *testing.T) {
	route := runCommandRoute{
		executable: "/tmp/tmux'" + string([]byte{0xff}),
		socketPath: "/tmp/socket'" + string([]byte{0xfe}),
		paneID:     "%42",
	}
	if err := validateRunCommandRoute(route); err != nil {
		t.Fatalf("safe opaque route = %v", err)
	}
}

func TestRunCommandRouteRejectsConfiguredControlBeforeTmux(t *testing.T) {
	executable, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(t.TempDir(), "tmux\nroute")
	if err := os.Symlink(executable, malformed); err != nil {
		t.Fatal(err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary: malformed, SocketPath: filepath.Join(t.TempDir(), "absent.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolveRunCommandRoute(context.Background(), server, "%1")
	if err == nil || !strings.Contains(err.Error(), "ASCII control") {
		t.Fatalf("configured control route error = %v", err)
	}
}
