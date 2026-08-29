package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/workspace"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type createWindowInput struct {
	SessionName    string `json:"sessionName,omitempty" jsonschema:"the exact session name to add the window to"`
	Name           string `json:"name,omitempty" jsonschema:"the new window's name"`
	Command        string `json:"command,omitempty" jsonschema:"a command for the window to run instead of a shell"`
	StartDirectory string `json:"startDirectory,omitempty" jsonschema:"the window's working directory"`
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

type buildWorkspaceInput struct {
	Document string `json:"document" jsonschema:"a tmuxp-style YAML workspace document"`
}

type buildWorkspaceOutput struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
}

func (t *tools) buildWorkspace(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input buildWorkspaceInput,
) (*mcp.CallToolResult, buildWorkspaceOutput, error) {
	described, err := workspace.Parse([]byte(input.Document))
	if err != nil {
		return nil, buildWorkspaceOutput{}, err
	}
	initial, err := described.InitialSessionRequest()
	if err != nil {
		return nil, buildWorkspaceOutput{}, err
	}
	session, err := t.runtime.createSession(ctx, initial)
	if err == nil {
		err = workspace.BuildInto(ctx, session, described)
	}
	if err != nil {
		t.runtime.observe(err)
		// Workspace builds are non-atomic. Preserve any created session ID so
		// the caller can clean it up.
		if session.ID() == "" {
			return nil, buildWorkspaceOutput{}, err
		}
		return toolFailure(fmt.Errorf(
				"%w; the session %q (%s) was created and is still there with what "+
					"was built before the failure, so building this document again "+
					"will fail on the name: remove that session or use another name",
				err, described.SessionName, session.ID())), buildWorkspaceOutput{
				SessionID:   session.ID().String(),
				SessionName: described.SessionName,
			}, nil
	}
	name, _ := session.Name()
	return nil, buildWorkspaceOutput{
		SessionID:   session.ID().String(),
		SessionName: name,
	}, nil
}

func addCreationTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityWorkspaceCreate, &mcp.Tool{
		Name:        "create_window",
		Annotations: mutating("Create a tmux Window"),
		Description: "Add one window to a session and return its id and the id " +
			"of the pane tmux made with it. Use build_workspace instead when a " +
			"whole session is being laid out from a document.",
	}, t.createWindow)
	register(server, t, CapabilityWorkspaceCreate, &mcp.Tool{
		Name:        "create_session",
		Annotations: mutating("Create a tmux Session"),
		Description: "Start one session and return its id and name. The MCP " +
			"runtime stays attached through its private control connection.",
	}, t.createSession)
	register(server, t, CapabilityWorkspaceCreate, &mcp.Tool{
		Name:        "build_workspace",
		Annotations: mutating("Build a tmuxp Workspace"),
		Description: "Create a session from a tmuxp-style YAML workspace document: " +
			"windows, panes, layouts, and the command each pane runs, in one call.",
	}, t.buildWorkspace)
}
