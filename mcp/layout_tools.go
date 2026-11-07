package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type splitWindowInput struct {
	PaneID         string `json:"paneId,omitempty" jsonschema:"the tmux pane id to split; empty splits the active pane"`
	SessionName    string `json:"sessionName,omitempty" jsonschema:"which session's active pane to split when paneId is empty"`
	Direction      string `json:"direction,omitempty" jsonschema:"where the new pane goes; empty puts it below"`
	Percentage     int    `json:"percentage,omitempty" jsonschema:"the new pane's share of the space, 1 to 100"`
	Command        string `json:"command,omitempty" jsonschema:"a command for the new pane to run instead of a shell"`
	StartDirectory string `json:"startDirectory,omitempty" jsonschema:"the new pane's working directory"`
}

type splitWindowOutput struct {
	PaneID string `json:"paneId"`
}

// splitDirections maps screen-relative words to tmux pane directions.
var splitDirections = map[string]tmux.PaneDirection{
	"":      tmux.PaneDirectionBelow,
	"below": tmux.PaneDirectionBelow,
	"above": tmux.PaneDirectionAbove,
	"right": tmux.PaneDirectionRight,
	"left":  tmux.PaneDirectionLeft,
}

func (t *tools) splitWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input splitWindowInput,
) (*mcp.CallToolResult, splitWindowOutput, error) {
	direction, known := splitDirections[strings.ToLower(strings.TrimSpace(input.Direction))]
	if !known {
		return nil, splitWindowOutput{}, fmt.Errorf(
			"direction %q is not below, above, right, or left", input.Direction)
	}
	if input.Percentage < 0 || input.Percentage > 100 {
		return nil, splitWindowOutput{}, fmt.Errorf(
			"percentage %d is not between 1 and 100", input.Percentage)
	}

	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, splitWindowOutput{}, err
	}
	request := tmux.SplitPaneRequest{
		Direction:      direction,
		Command:        input.Command,
		StartDirectory: input.StartDirectory,
	}
	if input.Percentage > 0 {
		percentage := input.Percentage
		request.Percentage = &percentage
	}
	created, err := pane.Split(ctx, request)
	if err != nil {
		return nil, splitWindowOutput{}, err
	}
	return nil, splitWindowOutput{PaneID: created.ID().String()}, nil
}

type resizePaneInput struct {
	PaneID      string `json:"paneId,omitempty" jsonschema:"the tmux pane id to resize; empty resizes the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to resize when paneId is empty"`
	Width       int    `json:"width,omitempty" jsonschema:"the pane's new width in cells"`
	Height      int    `json:"height,omitempty" jsonschema:"the pane's new height in cells"`
	Zoom        bool   `json:"zoom,omitempty" jsonschema:"toggle the pane between its size and the whole window"`
}

type resizePaneOutput struct {
	PaneID string `json:"paneId"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// resizePane reports tmux's final size, which layout constraints may adjust.
func (t *tools) resizePane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input resizePaneInput,
) (*mcp.CallToolResult, resizePaneOutput, error) {
	if input.Width < 0 || input.Height < 0 {
		return nil, resizePaneOutput{}, errors.New("a size must not be negative")
	}
	if input.Width == 0 && input.Height == 0 && !input.Zoom {
		return nil, resizePaneOutput{}, errors.New("width, height, or zoom is required")
	}

	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, resizePaneOutput{}, err
	}
	request := tmux.ResizePaneRequest{Zoom: input.Zoom}
	if input.Width > 0 {
		request.Width = tmux.PaneCells(input.Width)
	}
	if input.Height > 0 {
		request.Height = tmux.PaneCells(input.Height)
	}
	resized, err := pane.Resize(ctx, request)
	if err != nil {
		return nil, resizePaneOutput{}, err
	}
	width, _ := resized.Formats().PaneWidth()
	height, _ := resized.Formats().PaneHeight()
	return nil, resizePaneOutput{
		PaneID: resized.ID().String(), Width: width, Height: height,
	}, nil
}

type selectLayoutInput struct {
	WindowID    string `json:"windowId,omitempty" jsonschema:"the tmux window id to arrange; empty uses the current window"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to arrange when windowId is empty"`
	Layout      string `json:"layout,omitempty" jsonschema:"a named layout, a unique abbreviation for the running tmux version, or a saved layout from get_window_info"`
	Spread      bool   `json:"spread,omitempty" jsonschema:"give every pane an equal share of the space"`
}

type selectLayoutOutput struct {
	WindowID string `json:"windowId"`
	Layout   string `json:"layout"`
}

func (t *tools) selectLayout(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input selectLayoutInput,
) (*mcp.CallToolResult, selectLayoutOutput, error) {
	layout := strings.TrimSpace(input.Layout)
	if layout == "" && !input.Spread {
		return nil, selectLayoutOutput{}, errors.New("layout or spread is required")
	}
	if layout != "" && input.Spread {
		return nil, selectLayoutOutput{}, errors.New(
			"layout and spread are alternatives: spread evens the panes already " +
				"in the window, a layout replaces the arrangement")
	}
	request := tmux.SelectLayoutRequest{Layout: layout, Spread: input.Spread}
	if err := request.Validate(); err != nil {
		return nil, selectLayoutOutput{}, err
	}
	window, err := t.resolveWindow(ctx, input.WindowID, input.SessionName)
	if err != nil {
		return nil, selectLayoutOutput{}, err
	}
	if err := window.SelectLayout(ctx, request); err != nil {
		return nil, selectLayoutOutput{}, err
	}
	applied, err := window.Refresh(ctx)
	if err != nil {
		// Readback failure does not undo the successful layout change.
		//nolint:nilerr // retrying would repeat a change that already succeeded
		return nil, selectLayoutOutput{WindowID: window.ID().String()}, nil
	}
	current, _ := applied.Formats().WindowLayout()
	return nil, selectLayoutOutput{WindowID: applied.ID().String(), Layout: current}, nil
}

type selectPaneInput struct {
	PaneID string `json:"paneId" jsonschema:"the tmux pane id to make active"`
}

type selectPaneOutput struct {
	PaneID string `json:"paneId"`
}

func (t *tools) selectPane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input selectPaneInput,
) (*mcp.CallToolResult, selectPaneOutput, error) {
	pane, err := t.tmux(ctx).Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, selectPaneOutput{}, notFound(err, "pane", input.PaneID, "list_panes")
	}
	selected, err := pane.Select(ctx, tmux.PaneSelectRequest{})
	if err != nil {
		return nil, selectPaneOutput{}, err
	}
	return nil, selectPaneOutput{PaneID: selected.ID().String()}, nil
}

type swapPaneInput struct {
	PaneID     string `json:"paneId" jsonschema:"one of the two panes to exchange"`
	WithPaneID string `json:"withPaneId" jsonschema:"the other pane to exchange it with"`
	KeepFocus  bool   `json:"keepFocus,omitempty" jsonschema:"leave the active pane where it was"`
}

type swapPaneOutput struct {
	PaneID     string `json:"paneId"`
	WithPaneID string `json:"withPaneId"`
}

func (t *tools) swapPane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input swapPaneInput,
) (*mcp.CallToolResult, swapPaneOutput, error) {
	if strings.TrimSpace(input.PaneID) == "" || strings.TrimSpace(input.WithPaneID) == "" {
		return nil, swapPaneOutput{}, errors.New("paneId and withPaneId are both required")
	}
	if input.PaneID == input.WithPaneID {
		return nil, swapPaneOutput{}, errors.New("a pane cannot be swapped with itself")
	}
	server := t.tmux(ctx)
	pane, err := server.Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, swapPaneOutput{}, err
	}
	other, err := server.Pane(ctx, tmux.PaneID(input.WithPaneID))
	if err != nil {
		return nil, swapPaneOutput{}, err
	}
	if _, err := pane.Swap(ctx, tmux.SwapPaneRequest{
		Target: other,
		Detach: input.KeepFocus,
	}); err != nil {
		return nil, swapPaneOutput{}, err
	}
	return nil, swapPaneOutput{PaneID: input.PaneID, WithPaneID: input.WithPaneID}, nil
}

type resizeWindowInput struct {
	WindowID    string `json:"windowId,omitempty" jsonschema:"the tmux window id to resize; empty uses the current window"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to resize when windowId is empty"`
	Width       int    `json:"width,omitempty" jsonschema:"the window's new width in cells"`
	Height      int    `json:"height,omitempty" jsonschema:"the window's new height in cells"`
}

type resizeWindowOutput struct {
	WindowID string `json:"windowId"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

func (t *tools) resizeWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input resizeWindowInput,
) (*mcp.CallToolResult, resizeWindowOutput, error) {
	if input.Width < 0 || input.Height < 0 {
		return nil, resizeWindowOutput{}, errors.New("a size must not be negative")
	}
	if input.Width == 0 && input.Height == 0 {
		return nil, resizeWindowOutput{}, errors.New("width or height is required")
	}
	window, err := t.resolveWindow(ctx, input.WindowID, input.SessionName)
	if err != nil {
		return nil, resizeWindowOutput{}, err
	}
	resized, err := window.Resize(ctx, tmux.ResizeWindowRequest{
		Width:  input.Width,
		Height: input.Height,
	})
	if err != nil {
		return nil, resizeWindowOutput{}, err
	}
	width, _ := resized.Formats().WindowWidth()
	height, _ := resized.Formats().WindowHeight()
	return nil, resizeWindowOutput{
		WindowID: resized.ID().String(), Width: width, Height: height,
	}, nil
}

type moveWindowInput struct {
	WindowID    string `json:"windowId" jsonschema:"the tmux window id to move"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"the session to move it to; empty keeps it in its own"`
	Index       *int   `json:"index,omitempty" jsonschema:"the index to move it to; omit to use the next free one"`
}

type moveWindowOutput struct {
	WindowID string `json:"windowId"`
	Session  string `json:"session"`
	Index    int    `json:"index"`
}

func (t *tools) moveWindow(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input moveWindowInput,
) (*mcp.CallToolResult, moveWindowOutput, error) {
	if strings.TrimSpace(input.WindowID) == "" {
		return nil, moveWindowOutput{}, errors.New("windowId is required")
	}
	window, err := t.tmux(ctx).Window(ctx, tmux.WindowID(input.WindowID))
	if err != nil {
		return nil, moveWindowOutput{}, notFound(err, "window", input.WindowID, "list_windows")
	}
	request := tmux.MoveWindowRequest{TargetIndex: input.Index}
	if name := strings.TrimSpace(input.SessionName); name != "" {
		session, err := t.resolveSession(ctx, name)
		if err != nil {
			return nil, moveWindowOutput{}, err
		}
		request.TargetSession = session.ID()
	}
	moved, err := window.Move(ctx, request)
	if err != nil {
		return nil, moveWindowOutput{}, err
	}
	formats := moved.Formats()
	session, _ := formats.SessionName()
	index, _ := formats.WindowIndex()
	return nil, moveWindowOutput{
		WindowID: moved.ID().String(), Session: session, Index: index,
	}, nil
}

type movePaneInput struct {
	PaneID       string `json:"paneId" jsonschema:"the pane to move"`
	TargetPaneID string `json:"targetPaneId,omitempty" jsonschema:"the pane to place it beside"`
	TargetWinID  string `json:"targetWindowId,omitempty" jsonschema:"the window to move it into"`
	Direction    string `json:"direction,omitempty" jsonschema:"below, above, right, or left of the target"`
	Attach       bool   `json:"attach,omitempty" jsonschema:"make the moved pane active in its new window"`
}

type movePaneOutput struct {
	PaneID    string `json:"paneId"`
	WindowID  string `json:"windowId"`
	SessionID string `json:"sessionId"`
}

// movePane relocates a pane with tmux's move-pane (join-pane is the same
// command, so it gets no second name). Moving a window's only pane would
// destroy the window, which is a teardown, so it is refused here. The check
// and the move are separate tmux commands, so a client that removes a sibling
// between them still gets the move.
//
// The caller's own pane needs no confirmation: its process and id survive.
func (t *tools) movePane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input movePaneInput,
) (*mcp.CallToolResult, movePaneOutput, error) {
	if strings.TrimSpace(input.PaneID) == "" {
		return nil, movePaneOutput{}, errors.New("paneId is required")
	}
	hasPane := strings.TrimSpace(input.TargetPaneID) != ""
	hasWindow := strings.TrimSpace(input.TargetWinID) != ""
	if hasPane == hasWindow {
		return nil, movePaneOutput{}, errors.New("exactly one of targetPaneId and targetWindowId is required")
	}
	direction, known := splitDirections[strings.ToLower(strings.TrimSpace(input.Direction))]
	if !known {
		return nil, movePaneOutput{}, fmt.Errorf(
			"direction %q is not below, above, right, or left", input.Direction)
	}
	server := t.tmux(ctx)
	pane, err := server.Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, movePaneOutput{}, notFound(err, "pane", input.PaneID, "list_panes")
	}
	request := tmux.MovePaneRequest{Attach: input.Attach, Direction: direction}
	var destination tmux.WindowID
	if hasPane {
		target, err := server.Pane(ctx, tmux.PaneID(input.TargetPaneID))
		if err != nil {
			return nil, movePaneOutput{}, notFound(err, "pane", input.TargetPaneID, "list_panes")
		}
		request.TargetPane = target
		destination = target.WindowID()
	} else {
		target, err := server.Window(ctx, tmux.WindowID(input.TargetWinID))
		if err != nil {
			return nil, movePaneOutput{}, notFound(err, "window", input.TargetWinID, "list_windows")
		}
		request.TargetWindow = target
		destination = target.ID()
	}
	if destination == pane.WindowID() {
		return nil, movePaneOutput{}, errors.New("the pane is already in the target window; use swap_pane or select_layout to rearrange it")
	}
	panes, err := server.Panes(ctx)
	if err != nil {
		return nil, movePaneOutput{}, err
	}
	siblings := 0
	for _, other := range panes {
		if other.WindowID() == pane.WindowID() {
			siblings++
		}
	}
	if siblings < 2 {
		return nil, movePaneOutput{}, errors.New(
			"the pane is the only one in its window; moving it would destroy the window, so use break_pane or kill_window instead")
	}
	moved, err := pane.Move(ctx, request)
	if err != nil {
		return nil, movePaneOutput{}, err
	}
	return nil, movePaneOutput{
		PaneID: moved.ID().String(), WindowID: moved.WindowID().String(),
		SessionID: moved.SessionID().String(),
	}, nil
}

type breakPaneInput struct {
	PaneID string `json:"paneId" jsonschema:"the pane to move into a new window"`
	Name   string `json:"name,omitempty" jsonschema:"the new window's name"`
	Attach bool   `json:"attach,omitempty" jsonschema:"make the new window current"`
}

type breakPaneOutput struct {
	WindowID  string `json:"windowId"`
	SessionID string `json:"sessionId"`
}

// breakPane moves a pane into a new window of its own session. A window's only
// pane is allowed: tmux keeps the window and its id and only renumbers and
// renames it, so nothing is destroyed. Exact tmux 3.7 misnames a window broken
// out without a name, so the core refuses there and the refusal is reworded
// for this tool's arguments.
func (t *tools) breakPane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input breakPaneInput,
) (*mcp.CallToolResult, breakPaneOutput, error) {
	if strings.TrimSpace(input.PaneID) == "" {
		return nil, breakPaneOutput{}, errors.New("paneId is required")
	}
	pane, err := t.tmux(ctx).Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, breakPaneOutput{}, notFound(err, "pane", input.PaneID, "list_panes")
	}
	window, err := pane.BreakPane(ctx, tmux.BreakPaneRequest{Name: input.Name, Attach: input.Attach})
	if err != nil {
		return nil, breakPaneOutput{}, breakPaneNameRefusal(err)
	}
	return nil, breakPaneOutput{
		WindowID: window.ID().String(), SessionID: window.SessionID().String(),
	}, nil
}

// breakPaneNameRefusal words the core's refusal of an unnamed break-pane on
// exact tmux 3.7 as a remedy for this tool's caller, and leaves any other
// error alone.
func breakPaneNameRefusal(err error) error {
	refused, ok := errors.AsType[*tmux.VersionTooLowError](err)
	if !ok || refused.Subcommand != "break-pane" || refused.Feature != "automatic_window_name" {
		return err
	}
	return fmt.Errorf(
		"break_pane needs name on tmux %s, which misnames a window broken out without one; "+
			"give name, or run tmux %s or newer",
		refused.Current, refused.Minimum)
}
