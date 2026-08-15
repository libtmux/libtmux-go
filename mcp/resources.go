package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tmux "github.com/tmux-python/libtmux/golang"
)

// The tmux hierarchy is addressable as well as callable.
//
// A tool is something a model decides to do. A resource is something that can
// be named, attached to a conversation by a person, browsed by a client that
// knows no tool names, and read again without a model choosing to. The reads
// here are the same answers list_panes and capture_pane give; offering them
// both ways costs a handler each and lets a client use whichever fits.
//
// The URIs mirror tmux's own containment, so a reader who knows tmux can guess
// them: sessions hold windows, windows hold panes, and a pane has content.
//
// An id appears without its sigil. tmux writes a pane as %0 and a window as @1,
// and a percent sign begins an escape in a URI, so tmux://panes/%0/content is
// not a URI a client can parse at all. The sigil is redundant inside a path
// already saying panes, and requiring %250 instead would be correct and
// unusable. Both forms are accepted on the way in.
const (
	resourceSessions       = "tmux://sessions"
	templateSessionWindows = "tmux://sessions/{session}/windows"
	templateWindowPanes    = "tmux://windows/{window}/panes"
	templatePane           = "tmux://panes/{pane}"
	templatePaneContent    = "tmux://panes/{pane}/content"
)

// addResources advertises the readable hierarchy.
//
// Every resource here only reads, so the safety level never withholds one: a
// server offering no tools that change tmux can still be browsed.
func addResources(server *mcp.Server, t *tools) {
	server.AddResource(&mcp.Resource{
		URI:         resourceSessions,
		Name:        "tmux sessions",
		Title:       "Every tmux Session",
		Description: "Every session on this tmux server, with its windows and panes.",
		MIMEType:    "application/json",
	}, t.readSessions)

	for _, template := range []struct {
		uri, name, title, description string
	}{
		{
			templateSessionWindows, "tmux session windows", "Windows of One Session",
			"The windows of one session, addressed by session name.",
		},
		{
			templateWindowPanes, "tmux window panes", "Panes of One Window",
			"The panes of one window, addressed by window id such as @1.",
		},
		{
			templatePane, "tmux pane", "One tmux Pane",
			"One pane's identity, position, and what it is running.",
		},
		{
			templatePaneContent, "tmux pane content", "Contents of One Pane",
			"What one pane is showing, as text.",
		},
	} {
		server.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: template.uri,
			Name:        template.name,
			Title:       template.title,
			Description: template.description,
			MIMEType:    "application/json",
		}, t.readTemplated)
	}
}

// readSessions answers the whole hierarchy.
func (t *tools) readSessions(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	_, sessions, err := t.listSessions(ctx, nil, listSessionsInput{})
	if err != nil {
		return nil, err
	}
	return jsonResource(request.Params.URI, sessions)
}

// readTemplated answers one of the templated URIs.
//
// The SDK matches a request to this handler by template, and the request
// carries the concrete URI, so the segment that varies is read back from it
// rather than from a parameter the SDK does not provide.
func (t *tools) readTemplated(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	uri := request.Params.URI
	switch {
	case strings.HasPrefix(uri, "tmux://sessions/") && strings.HasSuffix(uri, "/windows"):
		name := strings.TrimSuffix(strings.TrimPrefix(uri, "tmux://sessions/"), "/windows")
		return t.readSessionWindows(ctx, uri, name)
	case strings.HasPrefix(uri, "tmux://windows/") && strings.HasSuffix(uri, "/panes"):
		id := strings.TrimSuffix(strings.TrimPrefix(uri, "tmux://windows/"), "/panes")
		return t.readWindowPanes(ctx, uri, withSigil(id, "@"))
	case strings.HasPrefix(uri, "tmux://panes/") && strings.HasSuffix(uri, "/content"):
		id := strings.TrimSuffix(strings.TrimPrefix(uri, "tmux://panes/"), "/content")
		return t.readPaneContent(ctx, uri, withSigil(id, "%"))
	case strings.HasPrefix(uri, "tmux://panes/"):
		return t.readPane(ctx, uri, withSigil(strings.TrimPrefix(uri, "tmux://panes/"), "%"))
	default:
		return nil, fmt.Errorf("%q is not a tmux resource this server serves", uri)
	}
}

func (t *tools) readSessionWindows(
	ctx context.Context,
	uri, name string,
) (*mcp.ReadResourceResult, error) {
	_, windows, err := t.listWindows(ctx, nil, listWindowsInput{})
	if err != nil {
		return nil, err
	}
	matching := make([]windowSummary, 0, len(windows.Windows))
	for _, window := range windows.Windows {
		if window.Session == name {
			matching = append(matching, window)
		}
	}
	return jsonResource(uri, listWindowsOutput{Windows: matching})
}

func (t *tools) readWindowPanes(
	ctx context.Context,
	uri, id string,
) (*mcp.ReadResourceResult, error) {
	window, err := t.strict().Window(ctx, tmux.WindowID(id))
	if err != nil {
		return nil, err
	}
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		return nil, err
	}
	summaries := make([]paneSummary, 0, len(panes))
	for _, pane := range panes {
		summaries = append(summaries, t.summarize(ctx, pane))
	}
	return jsonResource(uri, listPanesOutput{Panes: summaries})
}

func (t *tools) readPane(ctx context.Context, uri, id string) (*mcp.ReadResourceResult, error) {
	pane, err := t.strict().Pane(ctx, tmux.PaneID(id))
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, t.summarize(ctx, pane))
}

func (t *tools) readPaneContent(
	ctx context.Context,
	uri, id string,
) (*mcp.ReadResourceResult, error) {
	_, shown, err := t.capturePane(ctx, nil, capturePaneInput{PaneID: id})
	if err != nil {
		return nil, err
	}
	// Text rather than JSON: a pane's contents are what a person would paste
	// into a conversation, and quoting them as a JSON array helps nobody.
	//
	// The trailing newline is what makes an empty pane readable at all. MCP
	// requires a text resource to carry a text field, the SDK omits that field
	// when the text is empty, and a client then rejects contents that are
	// neither text nor binary. A pane showing nothing is a real and ordinary
	// state, so its contents end in a newline as any line-oriented text does,
	// and the empty case becomes one empty line rather than nothing at all.
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      uri,
		MIMEType: "text/plain",
		Text:     strings.Join(shown.Lines, "\n") + "\n",
	}}}, nil
}

// withSigil restores the character tmux writes in front of an id, so a URI can
// carry either form: the bare number a person can type, or the sigil form a
// client copied from a tool result and percent-encoded.
func withSigil(id, sigil string) string {
	if id == "" || strings.HasPrefix(id, sigil) {
		return id
	}
	return sigil + id
}

// jsonResource renders a value as one resource's contents.
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
