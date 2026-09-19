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

// --tools reported the teardown tools for any socket, while serving grants
// them only when it creates the tmux server itself.
func TestAdvertisedToolsForGrantsTeardownOnlyWhenServingWouldCreateTmux(t *testing.T) {
	// TestMain selects every toolset; the default surface is what is under test.
	t.Setenv(tmuxmcp.ToolsetsEnvironmentVariable, "")
	if err := os.Unsetenv(tmuxmcp.ToolsetsEnvironmentVariable); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	running := tmuxtest.NewServer(ctx, t)
	absent := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})

	for _, test := range []struct {
		name           string
		target         tmux.Server
		defaultMinimal bool
		wantTeardown   bool
	}{
		{name: "default socket already served", target: running, defaultMinimal: true},
		{name: "default socket nothing serves", target: absent, defaultMinimal: true, wantTeardown: true},
		{name: "operator socket nothing serves", target: absent},
		{name: "operator socket already served", target: running},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools, err := tmuxmcp.AdvertisedToolsFor(ctx, test.target, test.defaultMinimal)
			if err != nil {
				t.Fatal(err)
			}
			teardown := slices.ContainsFunc(tools, func(tool *sdk.Tool) bool {
				return tool.Name == "kill_session"
			})
			if teardown != test.wantTeardown {
				t.Fatalf("kill_session advertised = %v, want %v", teardown, test.wantTeardown)
			}
		})
	}
	if alive, err := absent.IsAlive(ctx); err != nil || alive {
		t.Fatalf("absent.IsAlive() = (%v, %v), want the probe to start nothing", alive, err)
	}
}
