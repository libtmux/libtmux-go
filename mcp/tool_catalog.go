package mcp

import (
	"context"
	"errors"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AdvertisedTools returns caller-owned descriptions of the tools permitted by
// the current safety and capability environment. It performs an in-memory MCP
// handshake without constructing a tmux target or allocating runtime-owned
// jobs, watchers, or audit resources.
func AdvertisedTools(ctx context.Context) (tools []*sdk.Tool, err error) {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "libtmux",
		Version: Version,
	}, nil)
	registerToolGroups(server, newToolRegistry())

	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, serverSession.Close()) }()

	client := sdk.NewClient(&sdk.Implementation{
		Name:    "libtmux-toolcatalog",
		Version: "1",
	}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, clientSession.Close()) }()

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	return slices.Clone(listed.Tools), nil
}
