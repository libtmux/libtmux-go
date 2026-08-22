package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
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
// unusable.
//
// A read takes the bare form or the encoded one. It cannot take the raw sigil:
// a template is matched by a regexp built from it, and a percent that is not
// followed by two hex digits matches no template, so the SDK answers before
// this package is reached. A subscription takes all three, because it is routed
// by the string itself — and it has to, since every tool hands a pane back as
// %1 and a client that subscribed with one got silence.
const (
	resourceSessions       = "tmux://sessions"
	templateSession        = "tmux://sessions/{session}"
	templateWindow         = "tmux://windows/{window}"
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
	// After the suffixed forms above, which these would otherwise swallow.
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

// decodeSegment turns one path segment back into the text it stands for.
//
// A tmux name is not restricted to what a URI may carry: a session called
// "spaced name" has no spelling but "spaced%20name", and %25 is how a client
// writes the sigil in %0. Comparing the still-encoded string against tmux's
// answer never matches, so such an object is unaddressable without this.
//
// A segment that will not decode is used as it was given, which is what a
// name containing a bare percent sign arrives as.
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
	window, err := t.tmux().Window(ctx, tmux.WindowID(id))
	if err != nil {
		return nil, err
	}
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		return nil, err
	}
	summaries := make([]listedPane, 0, len(panes))
	for _, pane := range panes {
		summaries = append(summaries, listedPane{paneSummary: t.summarize(ctx, pane)})
	}
	return jsonResource(uri, listPanesOutput{Panes: summaries, Total: len(panes)})
}

// readSession answers one session, which the hierarchy offered no way to read
// on its own: the list was there and the leaf was there, and the branch
// between them was not.
func (t *tools) readSession(ctx context.Context, uri, name string) (*mcp.ReadResourceResult, error) {
	_, info, err := t.getSessionInfo(ctx, nil, getSessionInfoInput{SessionName: name})
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, info)
}

// readWindow answers one window, for the same reason.
func (t *tools) readWindow(ctx context.Context, uri, id string) (*mcp.ReadResourceResult, error) {
	_, info, err := t.getWindowInfo(ctx, nil, getWindowInfoInput{WindowID: id})
	if err != nil {
		return nil, err
	}
	return jsonResource(uri, info)
}

func (t *tools) readPane(ctx context.Context, uri, id string) (*mcp.ReadResourceResult, error) {
	pane, err := t.tmux().Pane(ctx, tmux.PaneID(id))
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
