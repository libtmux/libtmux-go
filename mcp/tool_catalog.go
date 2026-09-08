package mcp

import (
	"context"
	"errors"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AdvertisedTools returns caller-owned descriptions of the startup-selected
// tools. It performs an in-memory MCP handshake without opening tmux or
// allocating runtime-owned audit resources.
func AdvertisedTools(ctx context.Context) (tools []*sdk.Tool, err error) {
	profile := socketProfile{
		Selector:                "name:libtmux-mcp",
		SelectionProvenance:     "default-dedicated",
		ServerState:             "absent",
		ConfigurationProvenance: "minimal",
		NamespaceBoundary:       "tmux-objects-only",
		AttachCommand:           "tmux -N -L 'libtmux-mcp' attach",
		defaultTeardown:         true,
	}
	surface, err := resolveToolSurface(profile)
	if err != nil {
		return nil, err
	}
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "libtmux",
		Version: Version,
	}, nil)
	if err := registerToolManifest(server, newToolRegistry(surface)); err != nil {
		return nil, err
	}

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
