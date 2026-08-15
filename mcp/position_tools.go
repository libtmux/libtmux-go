package mcp

import (
	"context"
	"fmt"
	"strings"

	tmux "github.com/libtmux/libtmux-go"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// paneGeometry is where a pane sits in its window, in terminal cells.
//
// A client that knows only a pane's index cannot say which pane is above
// another, because an index is creation order rather than position. These are
// tmux's own coordinates: Left and Top are the pane's first column and row,
// counted from the window's top left.
type paneGeometry struct {
	// Left is the pane's first column.
	Left int `json:"left"`
	// Top is the pane's first row.
	Top int `json:"top"`
	// Width is the pane's width in cells.
	Width int `json:"width"`
	// Height is the pane's height in cells.
	Height int `json:"height"`
}

// findPaneByPositionInput selects the pane on one side of another.
type findPaneByPositionInput struct {
	// PaneID is the pane to look from. Empty looks from the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to look from; empty looks from the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to look from when paneId is empty"`
	// Direction is the side to look toward: above, below, left, or right.
	Direction string `json:"direction" jsonschema:"the side to look toward: above, below, left, or right"`
}

// findPaneByPositionOutput reports the neighbour, if there is one.
type findPaneByPositionOutput struct {
	// PaneID is the neighbour's tmux id, empty when nothing is on that side.
	PaneID string `json:"paneId"`
	// Found reports whether a pane borders that side.
	Found bool `json:"found"`
	// Geometry is where the neighbour sits, when one was found.
	Geometry paneGeometry `json:"geometry"`
}

// findPaneByPosition reports the pane bordering one side of another.
//
// It reads tmux's coordinates rather than moving the active pane, so asking
// what is above a pane does not change which pane a person is looking at. A
// neighbour is a pane whose facing edge meets this one's and whose span across
// the other axis overlaps, which is how a person reads a split screen.
func (t *tools) findPaneByPosition(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input findPaneByPositionInput,
) (*mcp.CallToolResult, findPaneByPositionOutput, error) {
	direction := strings.ToLower(strings.TrimSpace(input.Direction))
	switch direction {
	case "above", "below", "left", "right":
	default:
		return nil, findPaneByPositionOutput{}, fmt.Errorf(
			"direction %q is not above, below, left, or right", input.Direction)
	}

	server := t.strict()
	origin, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, findPaneByPositionOutput{}, err
	}
	window, err := server.Window(ctx, origin.WindowID())
	if err != nil {
		return nil, findPaneByPositionOutput{}, err
	}
	siblings, err := window.SearchPanes(ctx, nil)
	if err != nil {
		return nil, findPaneByPositionOutput{}, err
	}

	from := readPaneGeometry(origin)
	for _, sibling := range siblings {
		if sibling.ID() == origin.ID() {
			continue
		}
		at := readPaneGeometry(sibling)
		if bordersOn(direction, from, at) {
			return nil, findPaneByPositionOutput{
				PaneID: sibling.ID().String(), Found: true, Geometry: at,
			}, nil
		}
	}
	return nil, findPaneByPositionOutput{}, nil
}

// readPaneGeometry reads a pane's coordinates. A pane whose formats are missing
// reports zeroes, which borders nothing.
func readPaneGeometry(pane tmux.Pane) paneGeometry {
	formats := pane.Formats()
	left, _ := formats.PaneLeft()
	top, _ := formats.PaneTop()
	width, _ := formats.PaneWidth()
	height, _ := formats.PaneHeight()
	return paneGeometry{Left: left, Top: top, Width: width, Height: height}
}

// bordersOn reports whether at is the pane on one side of from.
//
// tmux leaves a one-cell divider between panes, so the far edge and the near
// edge differ by one rather than meeting exactly.
func bordersOn(direction string, from, at paneGeometry) bool {
	overlapsHorizontally := at.Left <= from.Left+from.Width && from.Left <= at.Left+at.Width
	overlapsVertically := at.Top <= from.Top+from.Height && from.Top <= at.Top+at.Height
	switch direction {
	case "above":
		return at.Top+at.Height+1 == from.Top && overlapsHorizontally
	case "below":
		return from.Top+from.Height+1 == at.Top && overlapsHorizontally
	case "left":
		return at.Left+at.Width+1 == from.Left && overlapsVertically
	case "right":
		return from.Left+from.Width+1 == at.Left && overlapsVertically
	}
	return false
}

// addPositionTools advertises the tool that answers where a pane sits.
func addPositionTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "find_pane_by_position",
		Annotations: readOnly("Find a Neighbouring Pane"),
		Description: "Report the pane bordering one side of another: above, " +
			"below, left, or right. Reads tmux's coordinates, so asking does not " +
			"change which pane is active.",
	}, t.findPaneByPosition)
}
