package mcp

import (
	"context"
	"net/url"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// complete offers the values a resource template variable or a prompt argument
// can actually take.
//
// This is the one part of the server a client fills in rather than reads. A
// model choosing a pane guesses %1 and finds out by failing; a person filling
// tmux://panes/{pane} in a client's picker is offered the panes that exist.
// MCP has no completion for tool arguments, so this reaches the resources and
// prompts and not the twenty tools, which is worth knowing before expecting it
// everywhere.
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
		// A completion that cannot be computed is an empty list rather than a
		// failure: a client asking what a value could be should not have its
		// picker error because tmux is momentarily unreachable.
		//nolint:nilerr // an unreachable tmux offers nothing to complete, which
		// is not a failure of the picker someone is typing in.
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
	// The protocol caps a page at a hundred, and saying there are more is the
	// difference between a short list and a truncated one.
	const limit = 100
	details := mcp.CompletionResultDetails{Total: len(matching), Values: matching}
	if len(matching) > limit {
		details.Values = matching[:limit]
		details.HasMore = true
	}
	return &mcp.CompleteResult{Completion: details}, nil
}

// completionValues answers what one variable can be.
//
// The names are the ones the resource templates and prompts use, so a variable
// added there is completed here by having been named the same.
//
// The two callers do not speak the same dialect, and a value is only useful in
// the one that asked. A resource slot is pasted into a path, where an id
// carries no sigil and a name has to be escaped. A prompt argument is read
// back by a model and handed to paneId, where tmux's own spelling is the only
// one any tool accepts. Answering both in the URI dialect is what made a
// completed prompt name a pane that every tool rejects.
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
			// A session already chosen narrows the windows offered, which is
			// the point of being told what is filled in so far.
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

// withoutSigil drops the sigil for a URI slot and keeps it everywhere else. A
// percent sign begins an escape, so it is not what a client puts in a path.
func withoutSigil(id, sigil string, forURI bool) string {
	if !forURI {
		return id
	}
	return strings.TrimPrefix(id, sigil)
}

// forPath escapes a name for a URI slot. A session may be called anything, and
// a client pastes what it is offered straight into the path.
func forPath(name string, forURI bool) string {
	if !forURI {
		return name
	}
	return url.PathEscape(name)
}

// completionFor builds the handler the server options take, which is set
// before the tools value exists.
func completionFor(target tmux.Server) func(
	context.Context, *mcp.CompleteRequest,
) (*mcp.CompleteResult, error) {
	completing := &tools{}
	completing.reaching.Store(&target)
	return completing.complete
}
