package mcp

import (
	"context"
	"net/url"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// complete offers live values for resource-template and prompt arguments; MCP
// does not define tool-argument completion.
func (t *tools) complete(
	ctx context.Context,
	request *mcp.CompleteRequest,
) (*mcp.CompleteResult, error) {
	argument := request.Params.Argument
	var already map[string]string
	if request.Params.Context != nil {
		already = request.Params.Context.Arguments
	}

	forURI := request.Params.Ref == nil || request.Params.Ref.Type != "ref/prompt"
	values, err := t.completionValues(ctx, argument.Name, already, forURI)
	if err != nil {
		if t.runtime.isTerminalError(err) {
			return nil, err
		}
		return &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{
			Values: []string{},
		}}, nil
	}

	matching := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, argument.Value) {
			matching = append(matching, value)
		}
	}
	// MCP caps a completion page at 100 values.
	const limit = 100
	details := mcp.CompletionResultDetails{Total: len(matching), Values: matching}
	if len(matching) > limit {
		details.Values = matching[:limit]
		details.HasMore = true
	}
	return &mcp.CompleteResult{Completion: details}, nil
}

// completionValues emits URI-safe values for resource slots and tmux-native
// identifiers for prompt arguments.
func (t *tools) completionValues(
	ctx context.Context,
	name string,
	already map[string]string,
	forURI bool,
) ([]string, error) {
	switch name {
	case "session":
		_, sessions, err := t.listSessions(ctx, nil, listSessionsInput{})
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(sessions.Sessions))
		for _, session := range sessions.Sessions {
			values = append(values, forPath(session.Name, forURI))
		}
		return values, nil
	case "window":
		_, windows, err := t.listWindows(ctx, nil, listWindowsInput{})
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(windows.Windows))
		for _, window := range windows.Windows {
			if session := already["session"]; session != "" && window.Session != session {
				continue
			}
			values = append(values, withoutSigil(window.ID, "@", forURI))
		}
		return values, nil
	case "pane":
		_, panes, err := t.listPanes(ctx, nil, listPanesInput{})
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(panes.Panes))
		for _, pane := range panes.Panes {
			if session := already["session"]; session != "" && pane.Session != session {
				continue
			}
			values = append(values, withoutSigil(pane.ID, "%", forURI))
		}
		return values, nil
	default:
		return []string{}, nil
	}
}

// withoutSigil avoids treating a pane's percent sigil as a URI escape.
func withoutSigil(id, sigil string, forURI bool) string {
	if !forURI {
		return id
	}
	return strings.TrimPrefix(id, sigil)
}

func forPath(name string, forURI bool) string {
	if !forURI {
		return name
	}
	return url.PathEscape(name)
}

func (t *tools) completeObserved(
	ctx context.Context,
	request *mcp.CompleteRequest,
) (*mcp.CompleteResult, error) {
	requestCtx, acquired, err := t.acquireRequestRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer acquired.release()
	result, err := t.complete(requestCtx, request)
	t.runtime.observe(err)
	return result, err
}
