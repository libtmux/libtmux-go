package mcp

import (
	"context"
	"fmt"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/workspace"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// createWindowInput adds one window to a session.
type createWindowInput struct {
	// SessionName is the exact session to add the window to. Empty uses the
	// server's only session, and is refused when it has more than one.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the exact session name to add the window to"`
	// Name is the new window's name. Empty lets tmux choose.
	Name string `json:"name,omitempty" jsonschema:"the new window's name"`
	// Command starts the window running this instead of a shell.
	Command string `json:"command,omitempty" jsonschema:"a command for the window to run instead of a shell"`
	// StartDirectory is the window's working directory.
	StartDirectory string `json:"startDirectory,omitempty" jsonschema:"the window's working directory"`
}

// createWindowOutput identifies the window that was created.
type createWindowOutput struct {
	// WindowID is the new window's tmux id.
	WindowID string `json:"windowId"`
	// PaneID is the id of the pane tmux created with it.
	PaneID string `json:"paneId"`
}

// createWindow adds a window to a session.
//
// build_workspace makes a whole session from a document, which is more than a
// client wants when it needs one more window: authoring a workspace for that
// is a larger door than the job, and the alternative was typing a tmux command
// into a pane and reading the screen to learn what happened.
func (t *tools) createWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input createWindowInput,
) (*mcp.CallToolResult, createWindowOutput, error) {
	server := t.tmux()
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
	window, err := session.NewWindow(ctx, request)
	if err != nil {
		return nil, createWindowOutput{}, err
	}
	created := createWindowOutput{WindowID: window.ID().String()}
	// The pane tmux made with the window is what a caller addresses next, so
	// it is reported here rather than left to a listing.
	if fresh, lookupErr := server.Window(ctx, window.ID()); lookupErr == nil {
		if pane, ok, paneErr := fresh.ResolveActivePane(ctx); paneErr == nil && ok {
			created.PaneID = pane.ID().String()
		}
	}
	return nil, created, nil
}

// createSessionInput starts one session.
type createSessionInput struct {
	// Name is the new session's name. Empty lets tmux choose.
	Name string `json:"name,omitempty" jsonschema:"the new session's name"`
	// Command starts the session's first window running this instead of a shell.
	Command string `json:"command,omitempty" jsonschema:"a command for the first window to run instead of a shell"`
	// StartDirectory is the session's working directory.
	StartDirectory string `json:"startDirectory,omitempty" jsonschema:"the session's working directory"`
}

// createSessionOutput identifies the session that was created.
type createSessionOutput struct {
	// SessionID is the new session's tmux id.
	SessionID string `json:"sessionId"`
	// SessionName is the name tmux gave it.
	SessionName string `json:"sessionName"`
}

// createSession starts a detached session.
//
// It is detached because nothing here is a terminal: a session this server
// creates is one a person or another client attaches to later.
func (t *tools) createSession(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input createSessionInput,
) (*mcp.CallToolResult, createSessionOutput, error) {
	session, err := t.tmux().NewSession(ctx, tmux.NewSessionRequest{
		Name:           input.Name,
		Command:        input.Command,
		StartDirectory: input.StartDirectory,
	})
	if err != nil {
		return nil, createSessionOutput{}, err
	}
	name, _ := session.Name()
	return nil, createSessionOutput{
		SessionID: session.ID().String(), SessionName: name,
	}, nil
}

// buildWorkspaceInput carries the workspace document.
type buildWorkspaceInput struct {
	// Document is a tmuxp-style YAML workspace.
	Document string `json:"document" jsonschema:"a tmuxp-style YAML workspace document"`
}

// buildWorkspaceOutput identifies the created session.
type buildWorkspaceOutput struct {
	// SessionID is the created session's stable tmux identifier.
	SessionID string `json:"sessionId"`
	// SessionName is the created session's name.
	SessionName string `json:"sessionName"`
}

// buildWorkspace lays out a whole session from one document.
func (t *tools) buildWorkspace(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input buildWorkspaceInput,
) (*mcp.CallToolResult, buildWorkspaceOutput, error) {
	described, err := workspace.Parse([]byte(input.Document))
	if err != nil {
		return nil, buildWorkspaceOutput{}, err
	}
	session, err := workspace.Build(ctx, t.tmux(), described)
	if err != nil {
		// Build is not atomic, so report the partial session rather than
		// dropping the identifier a caller needs in order to clean up. Without
		// one there is nothing to report, and returning the error keeps an
		// empty result off the wire entirely.
		if session.ID() == "" {
			return nil, buildWorkspaceOutput{}, err
		}
		// And say so in the text, not only in the fields. A caller reading the
		// reply is told which pane failed and nothing about the session that
		// survived, so the obvious next move -- send the same document again --
		// fails on a name that already exists, for reasons the first reply
		// never mentioned.
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

// addCreationTools advertises the tools that make something new.
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
		Description: "Start one detached session and return its id and name. " +
			"Detached because this server is not a terminal; a person or another " +
			"client attaches to it.",
	}, t.createSession)
	register(server, t, CapabilityWorkspaceCreate, &mcp.Tool{
		Name:        "build_workspace",
		Annotations: mutating("Build a tmuxp Workspace"),
		Description: "Create a session from a tmuxp-style YAML workspace document: " +
			"windows, panes, layouts, and the command each pane runs, in one call.",
	}, t.buildWorkspace)
}
