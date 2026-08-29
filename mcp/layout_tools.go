package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

type movePaneInput struct {
	PaneID     string `json:"paneId" jsonschema:"the tmux pane id to move, such as %1"`
	ToWindowID string `json:"toWindowId,omitempty" jsonschema:"the window to move the pane into, such as @2; empty breaks it out into a new window"`
	Direction  string `json:"direction,omitempty" jsonschema:"where the moved pane goes in the destination; empty puts it below"`
	Percentage int    `json:"percentage,omitempty" jsonschema:"the moved pane's share of the destination window, 1 to 100"`
	Name       string `json:"name,omitempty" jsonschema:"the new window's name when breaking the pane out"`
	Focus      bool   `json:"focus,omitempty" jsonschema:"make the pane active where it lands, moving what a person sees"`
}

type movePaneOutput struct {
	PaneID    string `json:"paneId"`
	WindowID  string `json:"windowId"`
	BrokenOut bool   `json:"brokenOut"`
}

// Moving the last pane destroys its source window and possibly its session;
// the reply reports the destination window.
func (t *tools) movePane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input movePaneInput,
) (*mcp.CallToolResult, movePaneOutput, error) {
	direction, known := splitDirections[strings.ToLower(strings.TrimSpace(input.Direction))]
	if !known {
		return nil, movePaneOutput{}, fmt.Errorf(
			"direction %q is not below, above, right, or left", input.Direction)
	}
	if input.Percentage < 0 || input.Percentage > 100 {
		return nil, movePaneOutput{}, fmt.Errorf(
			"percentage %d is not between 1 and 100", input.Percentage)
	}
	pane, err := t.tmux().Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, movePaneOutput{}, notFound(err, "pane", input.PaneID, "list_panes")
	}

	if input.ToWindowID == "" {
		window, breakErr := pane.BreakPane(ctx, tmux.BreakPaneRequest{
			Attach: input.Focus,
			Name:   input.Name,
		})
		if breakErr != nil {
			return nil, movePaneOutput{}, breakErr
		}
		return nil, movePaneOutput{
			PaneID:    pane.ID().String(),
			WindowID:  window.ID().String(),
			BrokenOut: true,
		}, nil
	}

	destination, err := t.tmux().Window(ctx, tmux.WindowID(input.ToWindowID))
	if err != nil {
		return nil, movePaneOutput{}, notFound(err, "window", input.ToWindowID, "list_windows")
	}
	request := tmux.JoinPaneRequest{
		TargetWindow: destination,
		Attach:       input.Focus,
		Direction:    direction,
	}
	if input.Percentage > 0 {
		request.Percentage = tmux.Ptr(input.Percentage)
	}
	moved, err := pane.Join(ctx, request)
	if err != nil {
		return nil, movePaneOutput{}, err
	}
	return nil, movePaneOutput{
		PaneID:   moved.ID().String(),
		WindowID: moved.WindowID().String(),
	}, nil
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
	Layout      string `json:"layout,omitempty" jsonschema:"even-horizontal, even-vertical, main-horizontal, main-vertical, tiled, main-horizontal-mirrored or main-vertical-mirrored from tmux 3.5, or a layout string from get_window_info"`
	Spread      bool   `json:"spread,omitempty" jsonschema:"give every pane an equal share of the space"`
}

type selectLayoutOutput struct {
	WindowID string `json:"windowId"`
	Layout   string `json:"layout"`
}

// tmux 3.3a may crash the server on an unknown layout name. Accept known
// presets or strings shaped like tmux's serialized layouts.
var layoutPresets = map[string]bool{
	"even-horizontal": true,
	"even-vertical":   true,
	"main-horizontal": true,
	"main-vertical":   true,
	"tiled":           true,
	// The tmux module version-gates the mirrored layouts added in 3.5.
	"main-horizontal-mirrored": true,
	"main-vertical-mirrored":   true,
}

// layoutString matches tmux's checksum-prefixed serialized layouts.
var layoutString = regexp.MustCompile(`^[0-9a-f]{4},[0-9x,\[\]{}]+$`)

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
	if layout != "" && !layoutPresets[layout] && !layoutString.MatchString(layout) {
		return nil, selectLayoutOutput{}, fmt.Errorf(
			"%q is neither a tmux layout preset nor a layout string from get_window_info",
			input.Layout)
	}
	window, err := t.resolveWindow(ctx, input.WindowID, input.SessionName)
	if err != nil {
		return nil, selectLayoutOutput{}, err
	}
	if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{
		Layout: layout,
		Spread: input.Spread,
	}); err != nil {
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
	pane, err := t.tmux().Pane(ctx, tmux.PaneID(input.PaneID))
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
	server := t.tmux()
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
	window, err := t.tmux().Window(ctx, tmux.WindowID(input.WindowID))
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

func addLayoutTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityWorkspaceCreate, &mcp.Tool{
		Name:        "split_window",
		Annotations: mutating("Split a tmux Pane"),
		Description: "Divide one pane in two, placing the new one below, above, " +
			"to the right of, or to the left of it, and return the new pane's id.",
	}, t.splitWindow)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "resize_pane",
		Annotations: mutating("Resize a tmux Pane"),
		Description: "Set one pane's width or height in cells, or toggle it " +
			"between its layout size and the whole window. Reports the size tmux " +
			"settled on, which a layout may constrain.",
	}, t.resizePane)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "select_pane",
		Annotations: settling("Select a tmux Pane"),
		Description: "Make one pane its window's active pane, which is where a " +
			"person's keystrokes go.",
	}, t.selectPane)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "select_layout",
		Annotations: settling("Arrange a Window's Panes"),
		Description: "Arrange a window's panes with one of tmux's presets " +
			"(even-horizontal, even-vertical, main-horizontal, main-vertical, " +
			"tiled, and the mirrored pair from tmux 3.5), spread them evenly, " +
			"or restore a layout string read from " +
			"get_window_info.",
	}, t.selectLayout)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "swap_pane",
		Annotations: mutating("Swap Two tmux Panes"),
		Description: "Exchange two panes' positions. Both keep their ids, so " +
			"anything addressing them still can.",
	}, t.swapPane)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "move_pane",
		Annotations: mutating("Move a tmux Pane"),
		Description: "Move a pane into another window, or break it out into a " +
			"window of its own by naming no destination. The pane keeps its id " +
			"and whatever it is running, which killing and splitting again does " +
			"not. Moving a window's last pane destroys that window.",
	}, t.movePane)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "resize_window",
		Annotations: mutating("Resize a tmux Window"),
		Description: "Set a window's size in cells. Worth doing before running " +
			"something whose output is laid out in columns, since a window no " +
			"client is attached to is whatever size tmux guessed.",
	}, t.resizeWindow)
	register(server, t, CapabilityTmuxLayout, &mcp.Tool{
		Name:        "move_window",
		Annotations: settling("Move a tmux Window"),
		Description: "Move a window to another index, or into another session. " +
			"It keeps its id.",
	}, t.moveWindow)
}
