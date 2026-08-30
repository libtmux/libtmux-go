package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Kill tools are destructive and resolve targets exactly; tmux prefix and
// pattern matching must never select a kill target.

type killSessionInput struct {
	SessionName string `json:"sessionName" jsonschema:"the exact name of the session to kill"`
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
	if err := t.confirmCallerLoss(ctx, request, holdsCaller,
		"session "+input.SessionName); err != nil {
		return nil, killSessionOutput{}, err
	}
	if err := t.tmux(ctx).KillSession(ctx, "="+input.SessionName); err != nil {
		return nil, killSessionOutput{}, err
	}
	return nil, killSessionOutput{Killed: input.SessionName}, nil
}

type killWindowInput struct {
	WindowID string `json:"windowId" jsonschema:"the tmux window id to kill, such as @1"`
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
	if err := t.confirmCallerLoss(ctx, request, holdsCaller,
		"window "+input.WindowID); err != nil {
		return nil, killWindowOutput{}, err
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
	PaneID string `json:"paneId" jsonschema:"the tmux pane id to kill, such as %1"`
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
	if err := t.confirmCallerWrite(ctx, request, pane, "ending it", false); err != nil {
		return nil, killPaneOutput{}, err
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

type killServerInput struct {
	Confirm bool `json:"confirm" jsonschema:"must be true; this ends every session on the server"`
}

type killServerOutput struct {
	SessionsKilled int `json:"sessionsKilled"`
}

func (t *tools) killServer(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input killServerInput,
) (*mcp.CallToolResult, killServerOutput, error) {
	if !input.Confirm {
		return nil, killServerOutput{}, errors.New("confirm must be true: this ends every session on the tmux server")
	}
	_, holdsCaller, err := t.callerPaneOnThisServer(ctx)
	if err != nil {
		return nil, killServerOutput{}, err
	}
	if err := t.confirmCallerLoss(ctx, request, holdsCaller,
		"this tmux server"); err != nil {
		return nil, killServerOutput{}, err
	}
	sessions, _ := t.tmux(ctx).Sessions(ctx)
	if err := t.tmux(ctx).Kill(ctx); err != nil {
		return nil, killServerOutput{}, err
	}
	return nil, killServerOutput{SessionsKilled: len(sessions)}, nil
}

type respawnPaneInput struct {
	PaneID      string `json:"paneId,omitempty" jsonschema:"the tmux pane id to restart; empty uses the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to restart when paneId is empty"`
	Command     string `json:"command,omitempty" jsonschema:"a command to run instead; empty restarts what the pane ran before"`
	Kill        bool   `json:"kill,omitempty" jsonschema:"end a program that is still running first"`
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
	// Replace tmux's generic exit status with the actionable missing flag.
	if !input.Kill {
		if dead, ok := pane.Formats().PaneDead(); ok && !dead {
			return nil, respawnPaneOutput{}, fmt.Errorf(
				"pane %s is still running %s, and tmux refuses to respawn a live "+
					"pane; pass kill to replace what is running, or leave it alone",
				pane.ID(), currentCommandOf(pane))
		}
	}
	respawn := tmux.RespawnRequest{Kill: input.Kill}
	if input.Command != "" {
		command := input.Command
		respawn.Command = &command
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

func currentCommandOf(pane tmux.Pane) string {
	if command, ok := pane.Formats().PaneCurrentCommand(); ok && command != "" {
		return command
	}
	return "a program"
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

func addLifecycleTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityTmuxDestroy, &mcp.Tool{
		Name:        "kill_session",
		Annotations: destructive("Kill a tmux Session"),
		Description: "End one session by its exact name, and every window and " +
			"program in it. Nothing brings it back.",
	}, t.killSession)
	register(server, t, CapabilityTmuxDestroy, &mcp.Tool{
		Name:        "kill_window",
		Annotations: destructive("Kill a tmux Window"),
		Description: "End one window and its panes. A window that was its " +
			"session's last takes the session with it, which the reply says.",
	}, t.killWindow)
	register(server, t, CapabilityTmuxDestroy, &mcp.Tool{
		Name:        "kill_pane",
		Annotations: destructive("Kill a tmux Pane"),
		Description: "End one pane and the program running in it. A pane that " +
			"was its window's last takes the window with it, which the reply says.",
	}, t.killPane)
	register(server, t, CapabilityTmuxDestroy, &mcp.Tool{
		Name:        "kill_server",
		Annotations: destructive("Kill the tmux Server"),
		Description: "End the whole tmux server: every session, every window, " +
			"every program. Requires confirm to be true.",
	}, t.killServer)
	register(server, t, CapabilityWorkspaceCreate, &mcp.Tool{
		Name:        "respawn_pane",
		Annotations: mutating("Restart a Pane's Program"),
		Description: "Restart what a pane runs, keeping the pane and its place " +
			"in the layout. Use it on a pane whose program exited rather than " +
			"killing the pane and splitting a new one. A command that exits " +
			"takes the pane with it, and the window if it was the last one: " +
			"set remain-on-exit on the window first to keep it as a dead pane " +
			"list_panes can report.",
	}, t.respawnPane)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "rename_session",
		Annotations: settling("Rename a tmux Session"),
		Description: "Give a session a name a person will recognise. Its id does " +
			"not change.",
	}, t.renameSession)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "rename_window",
		Annotations: settling("Rename a tmux Window"),
		Description: "Name a window, which also stops tmux renaming it after " +
			"whatever is running in it.",
	}, t.renameWindow)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "set_pane_title",
		Annotations: settling("Title a tmux Pane"),
		Description: "Set the title tmux draws on a pane's border, which is how " +
			"to label which pane is which in a layout someone else will read.",
	}, t.setPaneTitle)
}
