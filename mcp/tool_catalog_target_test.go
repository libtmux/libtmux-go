package mcp_test

import (
	"os"
	"slices"
	"testing"

	tmuxmcp "github.com/libtmux/libtmux-go/mcp"
	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// No launch grants teardown by default, whether or not a server answers on the
// socket: a server this process did not start is never this process's to kill,
// and the default launch no longer starts one.
func TestAdvertisedToolsForWithholdsTeardownByDefault(t *testing.T) {
	// TestMain selects every toolset; the default surface is what is under test.
	t.Setenv(tmuxmcp.ToolsetsEnvironmentVariable, "")
	if err := os.Unsetenv(tmuxmcp.ToolsetsEnvironmentVariable); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	running := tmuxtest.NewServer(ctx, t)
	absent := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})

	for _, test := range []struct {
		name   string
		target tmux.Server
	}{
		{name: "socket already served", target: running},
		{name: "socket nothing serves", target: absent},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools, err := tmuxmcp.AdvertisedToolsFor(ctx, test.target)
			if err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(tools, func(tool *sdk.Tool) bool { return tool.Name == "kill_session" }) {
				t.Fatal("kill_session is advertised by default")
			}
		})
	}
	if alive, err := absent.IsAlive(ctx); err != nil || alive {
		t.Fatalf("absent.IsAlive() = (%v, %v), want the probe to start nothing", alive, err)
	}
}
