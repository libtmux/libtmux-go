package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Ending something, and naming it.
//
// Everything here that ends something is annotated destructive, so an operator
// running at the mutating level does not offer any of it, and a client that
// can create a session cannot by that fact remove one. Renaming is grouped
// with them because it is the other half of the same job: a client that made a
// window is the one that wants to label it.
//
// Nothing here resolves a target loosely. tmux matches a bare name by prefix
// and by pattern, so "test" names "test-production" when the first does not
// exist, and a kill is not the place to find that out. Every name is anchored,
// and every id is an id.

// killSessionInput selects the session to kill.
type killSessionInput struct {
	// SessionName is the exact session name. tmux would otherwise accept a
	// prefix or a pattern, so a name that does not match a session exactly is
	// refused rather than resolved to a neighbour.
	SessionName string `json:"sessionName" jsonschema:"the exact name of the session to kill"`
}

// killSessionOutput reports the killed session.
type killSessionOutput struct {
	// Killed is the session name that was killed.
	Killed string `json:"killed"`
}

// killSession ends one session and everything running in it.
func (t *tools) killSession(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input killSessionInput,
) (*mcp.CallToolResult, killSessionOutput, error) {
	// An empty name is what a model sends when it does not know one, and tmux
	// reads it as "the current session", so it would destroy a session nobody
	// named.
	if strings.TrimSpace(input.SessionName) == "" {
		return nil, killSessionOutput{}, fmt.Errorf("sessionName is required")
	}
	// tmux resolves a bare target by prefix and pattern, so "alph" kills
	// "alpha". The "=" prefix anchors it to an exact name, which is what this
	// tool documents and what a model expects when it repeats a name it read.
	if err := t.target.KillSession(ctx, "="+input.SessionName); err != nil {
		return nil, killSessionOutput{}, err
	}
	return nil, killSessionOutput{Killed: input.SessionName}, nil
}

// killWindowInput selects the window to kill.
type killWindowInput struct {
	// WindowID is the tmux window id, such as @1. An id rather than a name,
	// because window names are not unique and a kill must not guess.
	WindowID string `json:"windowId" jsonschema:"the tmux window id to kill, such as @1"`
}

// killWindowOutput reports the killed window.
type killWindowOutput struct {
	// Killed is the window id that was killed.
	Killed string `json:"killed"`
	// SessionEnded reports that the window was its session's last, so tmux
	// ended the session with it.
	SessionEnded bool `json:"sessionEnded"`
}

// killWindow ends one window and the panes in it.
//
// A window that was its session's last takes the session with it, which tmux
// does without saying so. The reply says so, because a client that killed one
// window of what it believed were several has just ended the session it was
// working in.
func (t *tools) killWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input killWindowInput,
) (*mcp.CallToolResult, killWindowOutput, error) {
	if strings.TrimSpace(input.WindowID) == "" {
		return nil, killWindowOutput{}, fmt.Errorf("windowId is required")
	}
	window, err := t.target.Window(ctx, tmux.WindowID(input.WindowID))
	if err != nil {
		return nil, killWindowOutput{}, err
	}
	sessionID := window.SessionID()
	if err := window.Kill(ctx); err != nil {
		return nil, killWindowOutput{}, err
	}
	output := killWindowOutput{Killed: input.WindowID}
	// A session that is gone cannot be looked up, which is the answer rather
	// than a failure.
	if _, err := t.target.Session(ctx, sessionID); err != nil {
		output.SessionEnded = true
	}
	return nil, output, nil
}

// killPaneInput selects the pane to kill.
type killPaneInput struct {
	// PaneID is the tmux pane id, such as %1.
	PaneID string `json:"paneId" jsonschema:"the tmux pane id to kill, such as %1"`
}

// killPaneOutput reports the killed pane.
type killPaneOutput struct {
	// Killed is the pane id that was killed.
	Killed string `json:"killed"`
	// WindowEnded reports that the pane was its window's last, so tmux ended
	// the window with it.
	WindowEnded bool `json:"windowEnded"`
}

// killPane ends one pane and the program in it.
func (t *tools) killPane(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input killPaneInput,
) (*mcp.CallToolResult, killPaneOutput, error) {
	if strings.TrimSpace(input.PaneID) == "" {
		return nil, killPaneOutput{}, fmt.Errorf("paneId is required")
	}
	pane, err := t.target.Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, killPaneOutput{}, err
	}
	// Named rather than resolved, so the guard is asked for here rather than
	// by the resolver every other write goes through.
	if err := t.confirmCallerWrite(ctx, request, pane, "ending it"); err != nil {
		return nil, killPaneOutput{}, err
	}
	windowID := pane.WindowID()
	if err := pane.Kill(ctx); err != nil {
		return nil, killPaneOutput{}, err
	}
	output := killPaneOutput{Killed: input.PaneID}
	if _, err := t.target.Window(ctx, windowID); err != nil {
		output.WindowEnded = true
	}
	return nil, output, nil
}

// killServerInput takes a confirmation rather than nothing.
type killServerInput struct {
	// Confirm must be true. A tool that takes no arguments is one a model can
	// call by naming it, and this one ends every session on the server at
	// once; requiring a field means the call cannot be made without having
	// filled something in.
	Confirm bool `json:"confirm" jsonschema:"must be true; this ends every session on the server"`
}

// killServerOutput reports what the server held when it was killed.
type killServerOutput struct {
	// SessionsKilled is how many sessions ended with it.
	SessionsKilled int `json:"sessionsKilled"`
}

// killServer ends the whole tmux server.
//
// This is the largest thing any tool here does: every session, every window,
// every program in them. It is offered because a client that created a server
// for a piece of work should be able to clean it up, and because the
// alternative is a client killing sessions one at a time and racing tmux's own
// teardown of the last one.
func (t *tools) killServer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input killServerInput,
) (*mcp.CallToolResult, killServerOutput, error) {
	if !input.Confirm {
		return nil, killServerOutput{}, fmt.Errorf(
			"confirm must be true: this ends every session on the tmux server")
	}
	// Counted before rather than after, because after there is nothing to ask.
	sessions, _ := t.target.Sessions(ctx)
	if err := t.target.Kill(ctx); err != nil {
		return nil, killServerOutput{}, err
	}
	return nil, killServerOutput{SessionsKilled: len(sessions)}, nil
}

// respawnPaneInput restarts the program in a pane.
type respawnPaneInput struct {
	// PaneID is the tmux pane id. Empty restarts the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to restart; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to restart when paneId is empty"`
	// Command replaces what the pane runs. Empty restarts what it ran before.
	Command string `json:"command,omitempty" jsonschema:"a command to run instead; empty restarts what the pane ran before"`
	// Kill ends a program that is still running. Without it, tmux refuses to
	// respawn a pane that has not exited.
	Kill bool `json:"kill,omitempty" jsonschema:"end a program that is still running first"`
}

// respawnPaneOutput reports the restarted pane.
type respawnPaneOutput struct {
	// PaneID is the pane that was restarted, which keeps its id.
	PaneID string `json:"paneId"`
}

// respawnPane restarts what a pane runs, in the pane it already has.
//
// A pane whose program exited is dead but still there, holding its output. A
// client that wants the program back would otherwise kill the pane and split a
// new one, which loses the layout and gives it a new id to track. This keeps
// both.
//
// The pane keeps its id and gets a new process. Anything holding a
// capture_since cursor for it will be told the process changed rather than
// quietly reading the new program's output as the old one's.
func (t *tools) respawnPane(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input respawnPaneInput,
) (*mcp.CallToolResult, respawnPaneOutput, error) {
	pane, err := t.resolvePaneToWrite(ctx, request, input.PaneID, input.SessionName, "restarting the pane")
	if err != nil {
		return nil, respawnPaneOutput{}, err
	}
	respawn := tmux.RespawnRequest{Kill: input.Kill}
	if input.Command != "" {
		command := input.Command
		respawn.Command = &command
	}
	respawned, err := pane.Respawn(ctx, respawn)
	if err != nil {
		return nil, respawnPaneOutput{}, err
	}
	return nil, respawnPaneOutput{PaneID: respawned.ID().String()}, nil
}

// renameSessionInput gives a session a new name.
type renameSessionInput struct {
	// SessionName is the exact session to rename. Empty renames the only one.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the exact session to rename; empty uses the only session"`
	// Name is what to call it.
	Name string `json:"name" jsonschema:"the new session name"`
}

// renameSessionOutput reports the renamed session.
type renameSessionOutput struct {
	// SessionID is the session's id, which a rename does not change and which
	// is therefore what to keep hold of.
	SessionID string `json:"sessionId"`
	// Name is what it is called now.
	Name string `json:"name"`
}

// renameSession renames a session.
//
// A name is how a person finds a session and how kill_session addresses one,
// so a client that created a session named by tmux gives it a name someone
// will recognise. The id does not change, which is why the reply reports it.
func (t *tools) renameSession(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input renameSessionInput,
) (*mcp.CallToolResult, renameSessionOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, renameSessionOutput{}, fmt.Errorf("name is required")
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

// renameWindowInput gives a window a new name.
type renameWindowInput struct {
	// WindowID is the tmux window id. Empty renames the current window.
	WindowID string `json:"windowId,omitempty" jsonschema:"the tmux window id to rename; empty uses the current window"`
	// SessionName picks the session when WindowID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to rename when windowId is empty"`
	// Name is what to call it.
	Name string `json:"name" jsonschema:"the new window name"`
}

// renameWindowOutput reports the renamed window.
type renameWindowOutput struct {
	// WindowID is the window's id, unchanged by the rename.
	WindowID string `json:"windowId"`
	// Name is what it is called now.
	Name string `json:"name"`
}

// renameWindow renames a window.
//
// tmux renames a window to whatever is running in it unless something has
// named it, so a window a client labelled keeps the label and one it did not
// changes under it.
func (t *tools) renameWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input renameWindowInput,
) (*mcp.CallToolResult, renameWindowOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, renameWindowOutput{}, fmt.Errorf("name is required")
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

// setPaneTitleInput gives a pane a title.
type setPaneTitleInput struct {
	// PaneID is the tmux pane id. Empty titles the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to title; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to title when paneId is empty"`
	// Title is what to call it.
	Title string `json:"title" jsonschema:"the new pane title"`
}

// setPaneTitleOutput reports the titled pane.
type setPaneTitleOutput struct {
	// PaneID is the pane that was titled.
	PaneID string `json:"paneId"`
	// Title is what it is called now.
	Title string `json:"title"`
}

// setPaneTitle gives a pane a title.
//
// A pane's title is what tmux draws on its border, so a client laying out
// several panes can label which is which for the person who will read them.
// A program in the pane can overwrite it, which is why the reply reports what
// tmux settled on rather than what was asked for.
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

// addLifecycleTools advertises the tools that end or rename something.
func addLifecycleTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "kill_session",
		Annotations: destructive("Kill a tmux Session"),
		Description: "End one session by its exact name, and every window and " +
			"program in it. Nothing brings it back.",
	}, t.killSession)
	register(server, t, &mcp.Tool{
		Name:        "kill_window",
		Annotations: destructive("Kill a tmux Window"),
		Description: "End one window and its panes. A window that was its " +
			"session's last takes the session with it, which the reply says.",
	}, t.killWindow)
	register(server, t, &mcp.Tool{
		Name:        "kill_pane",
		Annotations: destructive("Kill a tmux Pane"),
		Description: "End one pane and the program running in it. A pane that " +
			"was its window's last takes the window with it, which the reply says.",
	}, t.killPane)
	register(server, t, &mcp.Tool{
		Name:        "kill_server",
		Annotations: destructive("Kill the tmux Server"),
		Description: "End the whole tmux server: every session, every window, " +
			"every program. Requires confirm to be true.",
	}, t.killServer)
	register(server, t, &mcp.Tool{
		Name:        "respawn_pane",
		Annotations: mutating("Restart a Pane's Program"),
		Description: "Restart what a pane runs, keeping the pane and its place " +
			"in the layout. Use it on a pane whose program exited rather than " +
			"killing the pane and splitting a new one.",
	}, t.respawnPane)
	register(server, t, &mcp.Tool{
		Name:        "rename_session",
		Annotations: mutating("Rename a tmux Session"),
		Description: "Give a session a name a person will recognise. Its id does " +
			"not change.",
	}, t.renameSession)
	register(server, t, &mcp.Tool{
		Name:        "rename_window",
		Annotations: mutating("Rename a tmux Window"),
		Description: "Name a window, which also stops tmux renaming it after " +
			"whatever is running in it.",
	}, t.renameWindow)
	register(server, t, &mcp.Tool{
		Name:        "set_pane_title",
		Annotations: mutating("Title a tmux Pane"),
		Description: "Set the title tmux draws on a pane's border, which is how " +
			"to label which pane is which in a layout someone else will read.",
	}, t.setPaneTitle)
}
