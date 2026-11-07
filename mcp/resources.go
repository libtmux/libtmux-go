package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CapabilitiesResourceURI is the static MCP resource that discloses the
// startup-frozen surface. The tmux:// hierarchy below is offered beside it,
// one route per selected read tool.
const CapabilitiesResourceURI = "tmux://capabilities"

// resourceRoute is one read-only view of the hierarchy. It is offered only
// when the tool it reads through is in the startup-frozen effective set, so
// startup selection still governs every readable thing.
type resourceRoute struct {
	// template is the RFC 6570 template; a route without a variable is a
	// static resource.
	template    string
	name        string
	description string
	mime        string
	tool        string
	// argument names the tool argument the single path variable fills.
	argument string
	// subscribable routes accept resources/subscribe and subscriptions/listen.
	subscribable bool
	// kind and tail are what the parsed URI must look like.
	kind string
	tail string
}

var resourceRoutes = []resourceRoute{
	{
		template: "tmux://sessions", name: "sessions", mime: "application/json", tool: "list_sessions",
		kind: "sessions", description: "Every session on the pinned server, as list_sessions returns it.",
	},
	{
		template: "tmux://sessions/{session_id}", name: "session", mime: "application/json", tool: "get_session_info",
		argument: "session_id", kind: "sessions",
		description: "One session, as get_session_info returns it. The id is the session id, percent-encoded: $1 is %241.",
	},
	{
		template: "tmux://windows/{window_id}", name: "window", mime: "application/json", tool: "get_window_info",
		argument: "window_id", kind: "windows",
		description: "One window, as get_window_info returns it. The id is the window id, percent-encoded: @1 is %401.",
	},
	{
		template: "tmux://panes/{pane_id}", name: "pane", mime: "application/json", tool: "get_pane_info",
		argument: "pane_id", kind: "panes",
		description: "One pane, as get_pane_info returns it. The id is the pane id, percent-encoded: %1 is %251.",
	},
	{
		template: "tmux://panes/{pane_id}/content", name: "pane content", mime: "text/plain", tool: "capture_pane",
		argument: "pane_id", kind: "panes", tail: "content", subscribable: true,
		description: "The visible screen of one pane, as capture_pane returns it. " +
			"Subscribable on tmux 3.7 and newer; terminal output, so it may hold secrets and untrusted text.",
	},
}

// effectiveRoutes are the routes whose tool the startup selection granted.
func (surface toolSurface) effectiveRoutes() []resourceRoute {
	var routes []resourceRoute
	for _, route := range resourceRoutes {
		if _, selected := surface.byName[route.tool]; selected {
			routes = append(routes, route)
		}
	}
	return routes
}

// subscribableRoute is the route a client can subscribe to, when the startup
// selection granted its tool.
func (surface toolSurface) subscribableRoute() (resourceRoute, bool) {
	for _, route := range surface.effectiveRoutes() {
		if route.subscribable {
			return route, true
		}
	}
	return resourceRoute{}, false
}

func addResources(server *mcp.Server, t *tools) {
	server.AddResource(&mcp.Resource{
		URI:         CapabilitiesResourceURI,
		Name:        "tmux capabilities",
		Title:       "Startup-frozen tmux MCP capabilities",
		Description: "The selected socket, effective tools, schemas, input literalization, and aggregate authority.",
		MIMEType:    "application/json",
	}, t.readCapabilities)
	for _, route := range t.surface.effectiveRoutes() {
		handler := t.routeHandler(route)
		if !strings.Contains(route.template, "{") {
			server.AddResource(&mcp.Resource{
				URI: route.template, Name: route.name, Description: route.description, MIMEType: route.mime,
			}, handler)
			continue
		}
		server.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: route.template, Name: route.name, Description: route.description, MIMEType: route.mime,
		}, handler)
	}
}

func (t *tools) readCapabilities(
	_ context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	return jsonResource(request.Params.URI, t.surface.capabilityReport())
}

// escapeID percent-encodes a tmux id for a URI path segment. A template
// variable matches only unreserved characters and escapes, so $, @, and % are
// all escaped.
func escapeID(id string) string {
	var out strings.Builder
	for i := 0; i < len(id); i++ {
		switch c := id[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			out.WriteByte(c)
		default:
			fmt.Fprintf(&out, "%%%02X", c)
		}
	}
	return out.String()
}

// parseTmuxURI splits tmux://kind/id[/tail]. The id is percent-decoded, which
// is how $1, @1 and %1 survive being URI path segments.
func parseTmuxURI(uri string) (kind, id, tail string, err error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "tmux" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", "", fmt.Errorf("%q is not a tmux:// resource", uri)
	}
	if parsed.Path == "" {
		return parsed.Host, "", "", nil
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	switch len(parts) {
	case 1:
		return parsed.Host, parts[0], "", nil
	case 2:
		return parsed.Host, parts[0], parts[1], nil
	}
	return "", "", "", fmt.Errorf("%q has too many path segments", uri)
}

func (route resourceRoute) matches(uri string) (id string, ok bool) {
	kind, id, tail, err := parseTmuxURI(uri)
	if err != nil || kind != route.kind || tail != route.tail {
		return "", false
	}
	if (route.argument == "") != (id == "") {
		return "", false
	}
	return id, true
}

// routeHandler reads through the tool's own dispatcher, so the resource and
// the tool share runtime acquisition, schema validation, bounds, and version
// remedies. The dispatcher gets the request the tool would have received: the
// read's session and transport details, and the tool's name and arguments.
func (t *tools) routeHandler(route resourceRoute) mcp.ResourceHandler {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := request.Params.URI
		id, ok := route.matches(uri)
		if !ok {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		dispatch, selected := t.dispatchers[route.tool]
		if !selected {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		arguments := map[string]any{}
		if route.argument != "" {
			arguments[route.argument] = id
		}
		encoded, err := json.Marshal(arguments)
		if err != nil {
			return nil, err
		}
		call := &mcp.CallToolRequest{
			Session: request.Session,
			Extra:   request.Extra,
			Params:  &mcp.CallToolParamsRaw{Name: route.tool, Arguments: encoded},
		}
		result, err := dispatch.call(ctx, call, encoded)
		if err != nil {
			return nil, resourceReadError(uri, err)
		}
		if result.NeedsInput() {
			return nil, fmt.Errorf("%s needs an answer a resource read cannot ask for; call %s", uri, route.tool)
		}
		if result.IsError {
			if cause := result.GetError(); cause != nil {
				return nil, resourceReadError(uri, cause)
			}
			return nil, errors.New(toolResultText(result))
		}
		text := toolResultText(result)
		if route.mime != "text/plain" {
			text = structuredText(result)
		}
		return privateRead(&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: route.mime, Text: text,
		}}}), nil
	}
}

// invalidParams is the JSON-RPC error for a request that names something that
// is not there. Protocol 2026-07-28 reports a missing resource as -32602,
// where earlier revisions used -32002.
func invalidParams(uri string, err error) error {
	data := json.RawMessage(fmt.Sprintf(`{"uri":%q}`, uri))
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error(), Data: data}
}

// resourceReadError reports a missing object as invalid params and leaves
// every other failure as the tool reported it.
func resourceReadError(uri string, err error) error {
	if errors.Is(err, tmux.ErrNotFound) {
		return invalidParams(uri, err)
	}
	return err
}

// privateRead marks a read as meant for the caller's own cache. Every tmux://
// read describes one operator's server: pane content can hold secrets, and
// session names, working directories, and running commands are no more public.
// The SDK would otherwise send "public".
func privateRead(result *mcp.ReadResourceResult) *mcp.ReadResourceResult {
	result.CacheScope = "private"
	return result
}

func toolResultText(result *mcp.CallToolResult) string {
	var text []string
	for _, content := range result.Content {
		if part, ok := content.(*mcp.TextContent); ok {
			text = append(text, part.Text)
		}
	}
	return strings.Join(text, "\n")
}

func structuredText(result *mcp.CallToolResult) string {
	raw, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		return toolResultText(result)
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	indented, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(indented)
}

func jsonResource(uri string, value any) (*mcp.ReadResourceResult, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return privateRead(&mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      uri,
		MIMEType: "application/json",
		Text:     string(encoded),
	}}}), nil
}
