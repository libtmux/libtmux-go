package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type createWindowInput struct {
	SessionName    string `json:"sessionName,omitempty" jsonschema:"the exact session name to add the window to"`
	Name           string `json:"name,omitempty" jsonschema:"the new window's name"`
	Command        string `json:"command,omitempty" jsonschema:"a command for the window to run instead of a shell"`
	StartDirectory string `json:"startDirectory,omitempty" jsonschema:"the window's working directory"`
	Attach         bool
	Direction      string
}

type createWindowOutput struct {
	WindowID string `json:"windowId"`
	PaneID   string `json:"paneId"`
}

func (t *tools) createWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input createWindowInput,
) (*mcp.CallToolResult, createWindowOutput, error) {
	server := t.tmux(ctx)
	session, err := t.resolveSession(ctx, input.SessionName)
	if err != nil {
		return nil, createWindowOutput{}, err
	}

	request := tmux.NewWindowRequest{
		Command:        input.Command,
		StartDirectory: input.StartDirectory,
		Attach:         input.Attach,
	}
	switch input.Direction {
	case "", "default":
	case "after":
		request.Direction = tmux.NewWindowDirectionAfter
	case "before":
		request.Direction = tmux.NewWindowDirectionBefore
	default:
		return nil, createWindowOutput{}, fmt.Errorf(
			"direction %q is not before or after", input.Direction,
		)
	}
	if input.Name != "" {
		name := input.Name
		request.Name = &name
	}
	window, err := t.runtime.deps.newWindow(ctx, session, request)
	created := createWindowOutput{WindowID: window.ID().String()}
	if err != nil {
		if created.WindowID == "" {
			return nil, createWindowOutput{}, err
		}
		return t.partialWindowFailure(created, err)
	}
	fresh, lookupErr := t.runtime.deps.refreshWindow(ctx, server, window.ID())
	if lookupErr != nil {
		return t.partialWindowFailure(created, lookupErr)
	}
	pane, ok, paneErr := fresh.ResolveActivePane(ctx)
	if paneErr != nil {
		return t.partialWindowFailure(created, paneErr)
	}
	if !ok {
		return t.partialWindowFailure(
			created,
			errors.New("created window has no active pane"),
		)
	}
	created.PaneID = pane.ID().String()
	return nil, created, nil
}

func (t *tools) partialWindowFailure(
	created createWindowOutput,
	err error,
) (*mcp.CallToolResult, createWindowOutput, error) {
	t.runtime.observe(err)
	return toolFailure(fmt.Errorf(
		"%w; tmux created window %s before setup failed; use the returned ID to "+
			"inspect or remove it before retrying",
		err, created.WindowID,
	)), created, nil
}

type createSessionInput struct {
	Name           string `json:"name,omitempty" jsonschema:"the new session's name"`
	Command        string `json:"command,omitempty" jsonschema:"a command for the first window to run instead of a shell"`
	StartDirectory string `json:"startDirectory,omitempty" jsonschema:"the session's working directory"`
	WindowName     string
	Width          int
	Height         int
}

type createSessionOutput struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
}

// createSession keeps an attached control client as the runtime command lane.
func (t *tools) createSession(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input createSessionInput,
) (*mcp.CallToolResult, createSessionOutput, error) {
	session, err := t.runtime.createSession(ctx, tmux.NewSessionRequest{
		Name:           input.Name,
		Command:        input.Command,
		StartDirectory: input.StartDirectory,
		WindowName:     input.WindowName,
		Width:          input.Width,
		Height:         input.Height,
	})
	if err != nil {
		if session.ID() == "" {
			return nil, createSessionOutput{}, err
		}
		name, ok := session.Name()
		if !ok {
			name = input.Name
		}
		return toolFailure(fmt.Errorf(
				"%w; tmux created session %q (%s) before setup failed; use the "+
					"returned ID to inspect or remove it before retrying",
				err, name, session.ID(),
			)), createSessionOutput{
				SessionID: session.ID().String(), SessionName: name,
			}, nil
	}
	name, _ := session.Name()
	return nil, createSessionOutput{
		SessionID: session.ID().String(), SessionName: name,
	}, nil
}
