package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AdvertisedTools returns caller-owned descriptions of the startup-selected
// tools. It performs an in-memory MCP handshake without opening tmux or
// allocating runtime-owned audit resources.
//
// It describes a server that leaves the choice of tmux server to tmux.
// [AdvertisedToolsFor] answers for a specific target.
func AdvertisedTools(ctx context.Context) ([]*sdk.Tool, error) {
	return advertisedToolsFor(ctx, socketProfile{
		Selector:                "default",
		SelectionProvenance:     string(tmux.SocketSourceTmux),
		ServerState:             "unknown",
		ConfigurationProvenance: "unknown",
		NamespaceBoundary:       "tmux-objects-only",
		AttachCommand:           "tmux -N attach",
	})
}

// AdvertisedToolsFor returns the tools a server started now against target
// would advertise. It asks whether a tmux server is answering and starts
// nothing.
func AdvertisedToolsFor(ctx context.Context, target tmux.Server) ([]*sdk.Tool, error) {
	alive, err := target.IsAlive(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect selected tmux socket: %w", explainVersionRefusal(err))
	}
	state := "absent"
	if alive {
		state = "existing"
	}
	profile, err := profileForTarget(target, state)
	if err != nil {
		return nil, err
	}
	return advertisedToolsFor(ctx, profile)
}

func advertisedToolsFor(ctx context.Context, profile socketProfile) (tools []*sdk.Tool, err error) {
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

// ToolDetails returns what the named tool does, without the capability
// disclosure every description opens with. The disclosure is shared across a
// toolset, so a listing that shows only a description's first sentence would
// otherwise show the same sentence for every tool in it.
func ToolDetails(name string) (string, bool) {
	for _, definition := range authoritativeToolManifest {
		if definition.name == name {
			return definition.details, true
		}
	}
	return "", false
}
