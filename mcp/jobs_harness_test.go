package mcp

import (
	"context"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectJobSession(
	ctx context.Context,
	t *testing.T,
	target tmux.Server,
) (*Instance, *ServerSession, *sdk.CallToolRequest) {
	t.Helper()
	instance := mustInternalMCPServer(t, target)
	_, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := instance.Connect(ctx, AssumeResponseCommit(serverTransport), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	return instance, serverSession, &sdk.CallToolRequest{Session: serverSession.sdk}
}
