package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Kill tools are destructive and resolve targets exactly; tmux prefix and
// pattern matching must never select a kill target.

type killSessionInput struct {
	SessionName string `json:"sessionName" jsonschema:"the exact name of the session to kill"`
	ConfirmSelf bool
}

type killSessionOutput struct {
	Killed string `json:"killed"`
}

func (t *tools) killSession(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input killSessionInput,
) (*mcp.CallToolResult, killSessionOutput, error) {
	// tmux treats an empty target as current, so require an explicit name.
	if strings.TrimSpace(input.SessionName) == "" {
		return nil, killSessionOutput{}, errors.New("sessionName is required")
	}
	// The "=" prefix anchors the target against tmux prefix and pattern matching.
	holdsCaller := false
	caller, inside, err := t.callerPaneOnThisServer(ctx)
	if err != nil {
		return nil, killSessionOutput{}, err
	}
	if inside {
		name, _ := caller.Formats().SessionName()
		holdsCaller = name == input.SessionName
	}
	if !input.ConfirmSelf {
		if err := t.confirmCallerLoss(ctx, request, holdsCaller,
			"session "+input.SessionName); err != nil {
			return nil, killSessionOutput{}, err
		}
	}
	if err := t.tmux(ctx).KillSession(ctx, "="+input.SessionName); err != nil {
		return nil, killSessionOutput{}, err
	}
	return nil, killSessionOutput{Killed: input.SessionName}, nil
}

type killWindowInput struct {
	WindowID    string `json:"windowId" jsonschema:"the tmux window id to kill, such as @1"`
	ConfirmSelf bool
}

type killWindowOutput struct {
	Killed       string `json:"killed"`
	SessionEnded bool   `json:"sessionEnded"`
}

// killWindow reports when tmux also ends the window's now-empty session.
func (t *tools) killWindow(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input killWindowInput,
) (*mcp.CallToolResult, killWindowOutput, error) {
	if strings.TrimSpace(input.WindowID) == "" {
		return nil, killWindowOutput{}, errors.New("windowId is required")
	}
	window, err := t.tmux(ctx).Window(ctx, tmux.WindowID(input.WindowID))
	if err != nil {
		return nil, killWindowOutput{}, notFound(err, "window", input.WindowID, "list_windows")
	}
	holdsCaller := false
	caller, inside, err := t.callerPaneOnThisServer(ctx)
	if err != nil {
		return nil, killWindowOutput{}, err
	}
	if inside {
		holdsCaller = caller.WindowID() == window.ID()
	}
	if !input.ConfirmSelf {
		if err := t.confirmCallerLoss(ctx, request, holdsCaller,
			"window "+input.WindowID); err != nil {
			return nil, killWindowOutput{}, err
		}
	}
	sessionID := window.SessionID()
	if err := window.Kill(ctx); err != nil {
		return nil, killWindowOutput{}, err
	}
	output := killWindowOutput{Killed: input.WindowID}
	// Any failed post-kill lookup is reported as the source session ending; the
	// requested window kill has already succeeded.
	if _, err := t.tmux(ctx).Session(ctx, sessionID); err != nil {
		output.SessionEnded = true
	}
	return nil, output, nil
}

type killPaneInput struct {
	PaneID      string `json:"paneId" jsonschema:"the tmux pane id to kill, such as %1"`
	ConfirmSelf bool
}

type killPaneOutput struct {
	Killed      string `json:"killed"`
	WindowEnded bool   `json:"windowEnded"`
}

func (t *tools) killPane(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input killPaneInput,
) (*mcp.CallToolResult, killPaneOutput, error) {
	if strings.TrimSpace(input.PaneID) == "" {
		return nil, killPaneOutput{}, errors.New("paneId is required")
	}
	pane, err := t.tmux(ctx).Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, killPaneOutput{}, notFound(err, "pane", input.PaneID, "list_panes")
	}
	// Killing the caller pane always requires fresh consent; ordinary write
	// consent does not cover destroying it.
	if !input.ConfirmSelf {
		if err := t.confirmCallerWrite(ctx, request, pane, "ending it", false); err != nil {
			return nil, killPaneOutput{}, err
		}
	}
	windowID := pane.WindowID()
	if err := pane.Kill(ctx); err != nil {
		return nil, killPaneOutput{}, err
	}
	output := killPaneOutput{Killed: input.PaneID}
	if _, err := t.tmux(ctx).Window(ctx, windowID); err != nil {
		output.WindowEnded = true
	}
	return nil, output, nil
}

type respawnPaneInput struct {
	PaneID         string `json:"paneId,omitempty" jsonschema:"the tmux pane id to restart; empty uses the active pane"`
	SessionName    string `json:"sessionName,omitempty" jsonschema:"which session's active pane to restart when paneId is empty"`
	StartDirectory string
}

type respawnPaneOutput struct {
	PaneID string `json:"paneId"`
	// Gone reports a successful respawn whose replacement process exited before
	// readback, allowing tmux to reap the pane.
	Gone bool `json:"gone,omitempty"`
}

// respawnPane preserves the pane ID; capture_since detects the process change.
func (t *tools) respawnPane(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input respawnPaneInput,
) (*mcp.CallToolResult, respawnPaneOutput, error) {
	pane, err := t.resolvePaneToWrite(ctx, request, input.PaneID, input.SessionName, "restarting the pane")
	if err != nil {
		return nil, respawnPaneOutput{}, err
	}
	respawn := tmux.RespawnRequest{Kill: true}
	if input.StartDirectory != "" {
		startDirectory := input.StartDirectory
		respawn.StartDirectory = &startDirectory
	}
	respawned, err := pane.Respawn(ctx, respawn)
	if err != nil {
		// The mutation succeeded even when a short-lived replacement exits
		// before readback.
		if errors.Is(err, tmux.ErrSnapshotNotFound) {
			return nil, respawnPaneOutput{PaneID: pane.ID().String(), Gone: true}, nil
		}
		return nil, respawnPaneOutput{}, err
	}
	return nil, respawnPaneOutput{PaneID: respawned.ID().String()}, nil
}

type renameSessionInput struct {
	SessionName string `json:"sessionName,omitempty" jsonschema:"the exact session to rename; empty uses the only session"`
	Name        string `json:"name" jsonschema:"the new session name"`
}

type renameSessionOutput struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

func (t *tools) renameSession(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input renameSessionInput,
) (*mcp.CallToolResult, renameSessionOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, renameSessionOutput{}, errors.New("name is required")
	}
	session, err := t.resolveSession(ctx, input.SessionName)
	if err != nil {
		return nil, renameSessionOutput{}, err
	}
	renamed, err := session.Rename(ctx, input.Name)
	if err != nil {
		return nil, renameSessionOutput{}, err
	}
	name, _ := renamed.Formats().SessionName()
	return nil, renameSessionOutput{SessionID: renamed.ID().String(), Name: name}, nil
}

type renameWindowInput struct {
	WindowID    string `json:"windowId,omitempty" jsonschema:"the tmux window id to rename; empty uses the current window"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to rename when windowId is empty"`
	Name        string `json:"name" jsonschema:"the new window name"`
}

type renameWindowOutput struct {
	WindowID string `json:"windowId"`
	Name     string `json:"name"`
}

func (t *tools) renameWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input renameWindowInput,
) (*mcp.CallToolResult, renameWindowOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, renameWindowOutput{}, errors.New("name is required")
	}
	window, err := t.resolveWindow(ctx, input.WindowID, input.SessionName)
	if err != nil {
		return nil, renameWindowOutput{}, err
	}
	renamed, err := window.Rename(ctx, input.Name)
	if err != nil {
		return nil, renameWindowOutput{}, err
	}
	name, _ := renamed.Formats().WindowName()
	return nil, renameWindowOutput{WindowID: renamed.ID().String(), Name: name}, nil
}

type setPaneTitleInput struct {
	PaneID      string `json:"paneId,omitempty" jsonschema:"the tmux pane id to title; empty uses the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to title when paneId is empty"`
	Title       string `json:"title" jsonschema:"the new pane title"`
}

type setPaneTitleOutput struct {
	PaneID string `json:"paneId"`
	Title  string `json:"title"`
}

// setPaneTitle reports tmux's final title, which the pane process may replace.
func (t *tools) setPaneTitle(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input setPaneTitleInput,
) (*mcp.CallToolResult, setPaneTitleOutput, error) {
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, setPaneTitleOutput{}, err
	}
	titled, err := pane.SetTitle(ctx, input.Title)
	if err != nil {
		return nil, setPaneTitleOutput{}, err
	}
	title, _ := titled.Formats().PaneTitle()
	return nil, setPaneTitleOutput{PaneID: titled.ID().String(), Title: title}, nil
}
