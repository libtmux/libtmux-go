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

// splitWindowInput divides one pane in two.
type splitWindowInput struct {
	// PaneID is the tmux pane id to divide, such as %1. Empty divides the
	// active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to split; empty splits the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to split when paneId is empty"`
	// Direction places the new pane relative to that one: below, above, right,
	// or left. Empty places it below, as tmux does.
	Direction string `json:"direction,omitempty" jsonschema:"where the new pane goes: below, above, right, or left"`
	// Percentage gives the new pane that share of the space, from 1 to 100.
	// Zero lets tmux halve the pane.
	Percentage int `json:"percentage,omitempty" jsonschema:"the new pane's share of the space, 1 to 100"`
	// Command starts the new pane running this instead of a shell.
	Command string `json:"command,omitempty" jsonschema:"a command for the new pane to run instead of a shell"`
	// StartDirectory is the new pane's working directory.
	StartDirectory string `json:"startDirectory,omitempty" jsonschema:"the new pane's working directory"`
}

// splitWindowOutput identifies the pane that was created.
type splitWindowOutput struct {
	// PaneID is the new pane's tmux id.
	PaneID string `json:"paneId"`
}

// splitDirections maps the words a client uses to tmux's sides. The words are
// the ones a person says about a screen, rather than tmux's flag letters,
// because a client choosing between -h and -v has to know which axis each
// names before it can ask for the pane it wants.
var splitDirections = map[string]tmux.PaneDirection{
	"":      tmux.PaneDirectionBelow,
	"below": tmux.PaneDirectionBelow,
	"above": tmux.PaneDirectionAbove,
	"right": tmux.PaneDirectionRight,
	"left":  tmux.PaneDirectionLeft,
}

// movePaneInput moves a pane between windows, or out into one of its own.
type movePaneInput struct {
	// PaneID is the pane to move.
	PaneID string `json:"paneId" jsonschema:"the tmux pane id to move, such as %1"`
	// ToWindowID is the window to move it into. Empty breaks the pane out
	// into a new window of its own.
	ToWindowID string `json:"toWindowId,omitempty" jsonschema:"the window to move the pane into, such as @2; empty breaks it out into a new window"`
	// Direction places the moved pane relative to the destination's active
	// pane: below, above, right, or left. Empty places it below, as tmux does.
	Direction string `json:"direction,omitempty" jsonschema:"where the moved pane goes in the destination: below, above, right, or left"`
	// Percentage gives the moved pane that share of the destination, 1 to 100.
	Percentage int `json:"percentage,omitempty" jsonschema:"the moved pane's share of the destination window, 1 to 100"`
	// Name is the new window's name when the pane is broken out.
	Name string `json:"name,omitempty" jsonschema:"the new window's name when breaking the pane out"`
	// Focus makes the pane active where it lands, and the new window current
	// when it was broken out. Off by default, because moving what a person is
	// looking at is a bigger change than moving a pane.
	Focus bool `json:"focus,omitempty" jsonschema:"make the pane active where it lands, moving what a person sees"`
}

// movePaneOutput reports where the pane ended up.
type movePaneOutput struct {
	// PaneID is the pane that moved. It keeps its id: a pane somewhere else is
	// the same pane, so anything addressing it still can.
	PaneID string `json:"paneId"`
	// WindowID is the window it is in now.
	WindowID string `json:"windowId"`
	// BrokenOut reports that the window it is in was made for it.
	BrokenOut bool `json:"brokenOut"`
}

// movePane moves a pane into another window, or out into a new one.
//
// Rearranging across windows was the gap between splitting and killing: a
// caller that put a pane in the wrong window could only kill it and split
// again, losing whatever it was running. Both directions are one tool because
// they are one question with the destination left out -- naming a window moves
// the pane there, naming none gives it a window of its own.
//
// Moving the last pane out of a window destroys that window, and the session
// with it if it held nothing else. That is tmux's own behaviour rather than
// this tool's, and it is why the pane's new window is reported rather than
// assumed.
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
		return nil, movePaneOutput{}, err
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
		return nil, movePaneOutput{}, err
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

// splitWindow divides a pane and reports the new one's id.
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

// resizePaneInput sets one pane's size.
type resizePaneInput struct {
	// PaneID is the tmux pane id to resize. Empty resizes the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to resize; empty resizes the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to resize when paneId is empty"`
	// Width is the pane's new width in cells. Zero leaves it alone.
	Width int `json:"width,omitempty" jsonschema:"the pane's new width in cells"`
	// Height is the pane's new height in cells. Zero leaves it alone.
	Height int `json:"height,omitempty" jsonschema:"the pane's new height in cells"`
	// Zoom toggles the pane between its layout size and the whole window.
	Zoom bool `json:"zoom,omitempty" jsonschema:"toggle the pane between its size and the whole window"`
}

// resizePaneOutput reports the size tmux settled on.
type resizePaneOutput struct {
	// Width is the pane's width in cells after the change.
	Width int `json:"width"`
	// Height is the pane's height in cells after the change.
	Height int `json:"height"`
}

// resizePane sets a pane's size and reports what tmux settled on, which is not
// always what was asked: a layout constrains its panes, so a request larger
// than the window allows is honored as far as it can be.
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
	return nil, resizePaneOutput{Width: width, Height: height}, nil
}

// selectLayoutInput arranges a window's panes.
type selectLayoutInput struct {
	// WindowID is the tmux window id. Empty arranges the current window.
	WindowID string `json:"windowId,omitempty" jsonschema:"the tmux window id to arrange; empty uses the current window"`
	// SessionName picks the session when WindowID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to arrange when windowId is empty"`
	// Layout is one of tmux's presets, or a layout string read from
	// get_window_info. Empty with Spread set redistributes the space instead.
	Layout string `json:"layout,omitempty" jsonschema:"even-horizontal, even-vertical, main-horizontal, main-vertical, tiled, main-horizontal-mirrored or main-vertical-mirrored from tmux 3.5, or a layout string from get_window_info"`
	// Spread gives every pane an equal share without changing the arrangement.
	Spread bool `json:"spread,omitempty" jsonschema:"give every pane an equal share of the space"`
}

// selectLayoutOutput reports the layout in force.
type selectLayoutOutput struct {
	// WindowID is the window that was arranged.
	WindowID string `json:"windowId"`
	// Layout is tmux's description of the arrangement now, which this tool
	// accepts back: a layout worth keeping is saved by storing this string.
	Layout string `json:"layout"`
}

// layoutPresets are the arrangements tmux names.
//
// tmux takes an unrecognised name and, on some versions, dies of it: 3.3a
// crashes the whole server, taking every session on the socket with it. So a
// name is checked here before it is sent, and anything that is not a preset is
// only accepted when it looks like tmux's own layout string.
var layoutPresets = map[string]bool{
	"even-horizontal": true,
	"even-vertical":   true,
	"main-horizontal": true,
	"main-vertical":   true,
	"tiled":           true,
	// tmux added these at 3.5. They are passed on rather than gated here,
	// because the tmux module refuses them below that with the version it
	// found, which is the answer a client can act on.
	"main-horizontal-mirrored": true,
	"main-vertical-mirrored":   true,
}

// layoutString matches tmux's own description of an arrangement, which
// get_window_info returns and which begins with a checksum.
var layoutString = regexp.MustCompile(`^[0-9a-f]{4},[0-9x,\[\]{}]+$`)

// selectLayout arranges a window's panes.
//
// A client that split a window three times has three panes in whatever shape
// the splits left. This is how it gets a shape someone can read, and how a
// layout that worked is restored later: get_window_info reports the layout
// string, and this accepts it back.
func (t *tools) selectLayout(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input selectLayoutInput,
) (*mcp.CallToolResult, selectLayoutOutput, error) {
	layout := strings.TrimSpace(input.Layout)
	if layout == "" && !input.Spread {
		return nil, selectLayoutOutput{}, errors.New("layout or spread is required")
	}
	// tmux refuses the pair. Saying so here keeps its parser's wording, which
	// names modes this tool does not offer, from reaching a client.
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
	// tmux settles on an arrangement that may differ from the request, because
	// a layout is fitted to the window it is applied to.
	applied, err := window.Refresh(ctx)
	if err != nil {
		// The layout was applied. Failing the call because the read-back did
		// not work would invite a client to apply it again, so the window is
		// reported without the string that could not be read.
		//nolint:nilerr // the change succeeded; only reporting the result of it
		// failed, and a retry would repeat a change that already happened.
		return nil, selectLayoutOutput{WindowID: window.ID().String()}, nil
	}
	current, _ := applied.Formats().WindowLayout()
	return nil, selectLayoutOutput{WindowID: applied.ID().String(), Layout: current}, nil
}

// selectPaneInput chooses the pane a window is focused on.
type selectPaneInput struct {
	// PaneID is the tmux pane id to make active.
	PaneID string `json:"paneId" jsonschema:"the tmux pane id to make active"`
}

// selectPaneOutput reports the pane that is now active.
type selectPaneOutput struct {
	// PaneID is the pane that is now its window's active one.
	PaneID string `json:"paneId"`
}

// selectPane makes a pane its window's active one.
//
// The active pane is where a person's keystrokes go and what tmux's own
// commands mean by "this pane", so a client that built a layout puts the
// person in front of the pane they should be looking at.
func (t *tools) selectPane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input selectPaneInput,
) (*mcp.CallToolResult, selectPaneOutput, error) {
	pane, err := t.tmux().Pane(ctx, tmux.PaneID(input.PaneID))
	if err != nil {
		return nil, selectPaneOutput{}, err
	}
	selected, err := pane.Select(ctx, tmux.PaneSelectRequest{})
	if err != nil {
		return nil, selectPaneOutput{}, err
	}
	return nil, selectPaneOutput{PaneID: selected.ID().String()}, nil
}

// swapPaneInput exchanges two panes.
type swapPaneInput struct {
	// PaneID is one of the panes.
	PaneID string `json:"paneId" jsonschema:"one of the two panes to exchange"`
	// WithPaneID is the other.
	WithPaneID string `json:"withPaneId" jsonschema:"the other pane to exchange it with"`
	// KeepFocus leaves the active pane where it was rather than following the
	// pane that moved.
	KeepFocus bool `json:"keepFocus,omitempty" jsonschema:"leave the active pane where it was"`
}

// swapPaneOutput reports the exchange.
type swapPaneOutput struct {
	// PaneID and WithPaneID are the panes that changed places. Both keep their
	// ids: a pane that moved is the same pane in a different position, so
	// anything addressing it still can.
	PaneID string `json:"paneId"`
	// WithPaneID is the pane it changed places with.
	WithPaneID string `json:"withPaneId"`
}

// swapPane exchanges two panes' positions.
//
// A client that split a window and then wanted the new pane on the other side
// would otherwise kill it and split again, losing whatever it started. The
// panes keep their ids, so nothing addressing them has to be updated.
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

// resizeWindowInput sets a window's size.
type resizeWindowInput struct {
	// WindowID is the tmux window id. Empty resizes the current window.
	WindowID string `json:"windowId,omitempty" jsonschema:"the tmux window id to resize; empty uses the current window"`
	// SessionName picks the session when WindowID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to resize when windowId is empty"`
	// Width is the window's new width in cells. Zero leaves it alone.
	Width int `json:"width,omitempty" jsonschema:"the window's new width in cells"`
	// Height is the window's new height in cells. Zero leaves it alone.
	Height int `json:"height,omitempty" jsonschema:"the window's new height in cells"`
}

// resizeWindowOutput reports the size tmux settled on.
type resizeWindowOutput struct {
	// WindowID is the window that was resized.
	WindowID string `json:"windowId"`
	// Width is its width in cells after the change.
	Width int `json:"width"`
	// Height is its height in cells after the change.
	Height int `json:"height"`
}

// resizeWindow sets a window's size in cells.
//
// A window this server created is sized for a client that is not attached to
// it, so a pane's width is whatever tmux guessed. That matters to anything
// whose output is laid out in columns: a test runner or a table renders to the
// width it is given, and a client reading it back gets what fitted.
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

// moveWindowInput moves a window to another place in the order.
type moveWindowInput struct {
	// WindowID is the tmux window id to move.
	WindowID string `json:"windowId" jsonschema:"the tmux window id to move"`
	// SessionName is the session to move it to. Empty keeps it where it is.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the session to move it to; empty keeps it in its own"`
	// Index is the position to move it to. Omit to let tmux pick the next
	// free one.
	Index *int `json:"index,omitempty" jsonschema:"the index to move it to; omit to use the next free one"`
}

// moveWindowOutput reports where the window went.
type moveWindowOutput struct {
	// WindowID is the window that moved, which keeps its id.
	WindowID string `json:"windowId"`
	// Session is the session it is in now.
	Session string `json:"session"`
	// Index is its position there.
	Index int `json:"index"`
}

// moveWindow moves a window within its session or into another.
//
// Window indexes are what a person types to switch windows, so the order is
// part of the layout rather than bookkeeping. This is also how a window built
// in one session is handed to another.
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
		return nil, moveWindowOutput{}, err
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

// addLayoutTools advertises the tools that arrange what exists.
func addLayoutTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "split_window",
		Annotations: mutating("Split a tmux Pane"),
		Description: "Divide one pane in two, placing the new one below, above, " +
			"to the right of, or to the left of it, and return the new pane's id.",
	}, t.splitWindow)
	register(server, t, &mcp.Tool{
		Name:        "resize_pane",
		Annotations: mutating("Resize a tmux Pane"),
		Description: "Set one pane's width or height in cells, or toggle it " +
			"between its layout size and the whole window. Reports the size tmux " +
			"settled on, which a layout may constrain.",
	}, t.resizePane)
	register(server, t, &mcp.Tool{
		Name:        "select_pane",
		Annotations: settling("Select a tmux Pane"),
		Description: "Make one pane its window's active pane, which is where a " +
			"person's keystrokes go.",
	}, t.selectPane)
	register(server, t, &mcp.Tool{
		Name:        "select_layout",
		Annotations: settling("Arrange a Window's Panes"),
		Description: "Arrange a window's panes with one of tmux's presets " +
			"(even-horizontal, even-vertical, main-horizontal, main-vertical, " +
			"tiled, and the mirrored pair from tmux 3.5), spread them evenly, " +
			"or restore a layout string read from " +
			"get_window_info.",
	}, t.selectLayout)
	register(server, t, &mcp.Tool{
		Name:        "swap_pane",
		Annotations: mutating("Swap Two tmux Panes"),
		Description: "Exchange two panes' positions. Both keep their ids, so " +
			"anything addressing them still can.",
	}, t.swapPane)
	register(server, t, &mcp.Tool{
		Name:        "move_pane",
		Annotations: mutating("Move a tmux Pane"),
		Description: "Move a pane into another window, or break it out into a " +
			"window of its own by naming no destination. The pane keeps its id " +
			"and whatever it is running, which killing and splitting again does " +
			"not. Moving a window's last pane destroys that window.",
	}, t.movePane)
	register(server, t, &mcp.Tool{
		Name:        "resize_window",
		Annotations: mutating("Resize a tmux Window"),
		Description: "Set a window's size in cells. Worth doing before running " +
			"something whose output is laid out in columns, since a window no " +
			"client is attached to is whatever size tmux guessed.",
	}, t.resizeWindow)
	register(server, t, &mcp.Tool{
		Name:        "move_window",
		Annotations: settling("Move a tmux Window"),
		Description: "Move a window to another index, or into another session. " +
			"It keeps its id.",
	}, t.moveWindow)
}
