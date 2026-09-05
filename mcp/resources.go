package mcp

import (
	"context"
	"encoding/json"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CapabilitiesResourceURI is the only MCP resource exposed by this server.
const CapabilitiesResourceURI = "tmux://capabilities"

func addResources(server *mcp.Server, t *tools) {
	server.AddResource(&mcp.Resource{
		URI:         CapabilitiesResourceURI,
		Name:        "tmux capabilities",
		Title:       "Startup-frozen tmux MCP capabilities",
		Description: "The selected socket, effective tools, schemas, input literalization, and aggregate authority.",
		MIMEType:    "application/json",
	}, t.readCapabilities)
}

func (t *tools) readCapabilities(
	_ context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	return jsonResource(request.Params.URI, t.surface.capabilityReport())
}

func jsonResource(uri string, value any) (*mcp.ReadResourceResult, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      uri,
		MIMEType: "application/json",
		Text:     string(encoded),
	}}}, nil
}
