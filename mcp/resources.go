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

// Resource URIs mirror tmux containment and omit ID sigils; a pane's percent
// sigil would otherwise begin a URI escape. Subscriptions retain the caller's
// exact URI spelling for SDK routing.
const (
	resourceSessions       = "tmux://sessions"
	templateSession        = "tmux://sessions/{session}"
	templateWindow         = "tmux://windows/{window}"
	templateSessionWindows = "tmux://sessions/{session}/windows"
	templateWindowPanes    = "tmux://windows/{window}/panes"
	templatePane           = "tmux://panes/{pane}"
	templatePaneContent    = "tmux://panes/{pane}/content"
)

// addResources advertises the parts of the hierarchy the capability allowlist
// permits. Pane content is separate from topology because it may hold secrets.
func addResources(server *mcp.Server, t *tools) {
	if t.capabilities.permits(CapabilityMetadataRead) {
		server.AddResource(&mcp.Resource{
			URI:         resourceSessions,
			Name:        "tmux sessions",
			Title:       "Every tmux Session",
			Description: "Every session on this tmux server, with its windows and panes.",
			MIMEType:    "application/json",
		}, t.readSessions)
	}

	metadata := []struct {
		uri, name, title, description string
	}{
		{
			templateSessionWindows, "tmux session windows", "Windows of One Session",
			"The windows of one session, addressed by session name.",
		},
		{
			templateWindowPanes, "tmux window panes", "Panes of One Window",
			"The panes of one window, addressed by window id without its sigil, so @1 is written 1.",
		},
		{
			templateSession, "tmux session", "One tmux Session",
			"One session's windows and what it holds, addressed by name.",
		},
		{
			templateWindow, "tmux window", "One tmux Window",
			"One window's panes and layout, addressed by window id without its sigil, so @1 is written 1.",
		},
		{
			templatePane, "tmux pane", "One tmux Pane",
			"One pane's identity, position, and what it is running, addressed " +
				"by pane id without its sigil, so %1 is written 1.",
		},
	}
	if t.capabilities.permits(CapabilityMetadataRead) {
		for _, template := range metadata {
			server.AddResourceTemplate(&mcp.ResourceTemplate{
				URITemplate: template.uri,
				Name:        template.name,
				Title:       template.title,
				Description: template.description,
				MIMEType:    "application/json",
			}, t.readTemplated)
		}
	}
	if t.capabilities.permits(CapabilityContentRead) {
		server.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: templatePaneContent,
			Name:        "tmux pane content",
			Title:       "Contents of One Pane",
			Description: "What one pane is showing, as text, addressed by pane id without " +
				"its sigil, so %1 is written 1.",
			MIMEType: "text/plain",
		}, t.readTemplated)
	}
}

func (t *tools) readSessions(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	requestCtx, acquired, err := t.acquireRequestRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer acquired.release()
	_, sessions, err := t.listSessions(requestCtx, nil, listSessionsInput{})
	if err != nil {
		t.runtime.observe(err)
		return nil, err
	}
	result, err := jsonResource(request.Params.URI, sessions)
	t.runtime.observe(err)
	return result, err
}

// The SDK supplies only the concrete URI, so readTemplated parses its variables.
func (t *tools) readTemplated(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	requestCtx, acquired, err := t.acquireRequestRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer acquired.release()
	result, err := t.readOne(requestCtx, request.Params.URI)
	err = resourceError(request.Params.URI, err)
	t.runtime.observe(err)
	return result, err
}

// resourceError maps missing objects to MCP's resource-not-found JSON-RPC code.
func resourceError(uri string, err error) error {
	if errors.Is(err, tmux.ErrNoServer) {
		// Unlike listings, a resource read has no object to return.
		return errors.New(noServerNote)
	}
	if err == nil || !errors.Is(err, tmux.ErrSnapshotNotFound) {
		return err
	}
	return &jsonrpc.Error{
		Code:    mcp.CodeResourceNotFound,
		Message: err.Error(),
		Data:    json.RawMessage(fmt.Sprintf(`{"uri":%q}`, uri)),
	}
}

func (t *tools) readOne(
	ctx context.Context,
	uri string,
) (*mcp.ReadResourceResult, error) {
	switch {
	case strings.HasPrefix(uri, "tmux://sessions/") && strings.HasSuffix(uri, "/windows"):
		name := strings.TrimSuffix(strings.TrimPrefix(uri, "tmux://sessions/"), "/windows")
		return t.readSessionWindows(ctx, uri, decodeSegment(name))
	case strings.HasPrefix(uri, "tmux://windows/") && strings.HasSuffix(uri, "/panes"):
		id := strings.TrimSuffix(strings.TrimPrefix(uri, "tmux://windows/"), "/panes")
		return t.readWindowPanes(ctx, uri, withSigil(decodeSegment(id), "@"))
	case strings.HasPrefix(uri, "tmux://panes/") && strings.HasSuffix(uri, "/content"):
		id := strings.TrimSuffix(strings.TrimPrefix(uri, "tmux://panes/"), "/content")
		return t.readPaneContent(ctx, uri, withSigil(decodeSegment(id), "%"))
	case strings.HasPrefix(uri, "tmux://panes/"):
		id := decodeSegment(strings.TrimPrefix(uri, "tmux://panes/"))
		return t.readPane(ctx, uri, withSigil(id, "%"))
	// Keep unsuffixed forms after the prefixes they would otherwise swallow.
	case strings.HasPrefix(uri, "tmux://sessions/"):
		name := decodeSegment(strings.TrimPrefix(uri, "tmux://sessions/"))
		return t.readSession(ctx, uri, name)
	case strings.HasPrefix(uri, "tmux://windows/"):
		id := decodeSegment(strings.TrimPrefix(uri, "tmux://windows/"))
		return t.readWindow(ctx, uri, withSigil(id, "@"))
	default:
		return nil, fmt.Errorf("%q is not a tmux resource this server serves", uri)
	}
}

// decodeSegment unescapes URI path text and preserves malformed input for
// exact-name fallback.
func decodeSegment(segment string) string {
	decoded, err := url.PathUnescape(segment)
	if err != nil {
		return segment
	}
	return decoded
}

func (t *tools) readSessionWindows(
	ctx context.Context,
	uri, name string,
) (*mcp.ReadResourceResult, error) {
	_, windows, err := t.listWindows(ctx, nil, listWindowsInput{SessionName: name})
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, windows)
}

func (t *tools) readWindowPanes(
	ctx context.Context,
	uri, id string,
) (*mcp.ReadResourceResult, error) {
	window, err := t.tmux(ctx).Window(ctx, tmux.WindowID(id))
	if err != nil {
		return nil, notFound(err, "window", id, "list_windows")
	}
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		return nil, err
	}
	summaries := make([]listedPane, 0, len(panes))
	for _, pane := range panes {
		summary, err := t.summarize(ctx, pane)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, listedPane{paneSummary: summary})
	}
	return jsonResource(uri, listPanesOutput{Panes: summaries, Total: len(panes)})
}

func (t *tools) readSession(ctx context.Context, uri, name string) (*mcp.ReadResourceResult, error) {
	_, info, err := t.getSessionInfo(ctx, nil, getSessionInfoInput{SessionName: name})
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, info)
}

func (t *tools) readWindow(ctx context.Context, uri, id string) (*mcp.ReadResourceResult, error) {
	_, info, err := t.getWindowInfo(ctx, nil, getWindowInfoInput{WindowID: id})
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, info)
}

func (t *tools) readPane(ctx context.Context, uri, id string) (*mcp.ReadResourceResult, error) {
	pane, err := t.tmux(ctx).Pane(ctx, tmux.PaneID(id))
	if err != nil {
		return nil, notFound(err, "pane", id, "list_panes")
	}
	summary, err := t.summarize(ctx, pane)
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, summary)
}

func (t *tools) readPaneContent(
	ctx context.Context,
	uri, id string,
) (*mcp.ReadResourceResult, error) {
	_, shown, err := t.capturePane(ctx, nil, capturePaneInput{PaneID: id})
	if err != nil {
		return nil, err
	}
	// Pane content is text, not JSON. Always append a newline because the SDK
	// omits an empty Text field and clients reject content with no text or blob.
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      uri,
		MIMEType: "text/plain",
		Text:     strings.Join(shown.Lines, "\n") + "\n",
	}}}, nil
}

// withSigil accepts bare and percent-decoded tmux identifiers.
func withSigil(id, sigil string) string {
	if id == "" || strings.HasPrefix(id, sigil) {
		return id
	}
	return sigil + id
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
