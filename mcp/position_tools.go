package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// paneGeometry uses tmux terminal-cell coordinates; pane indexes do not encode
// spatial order.
type paneGeometry struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type findPaneByPositionInput struct {
	PaneID      string `json:"paneId,omitempty" jsonschema:"the tmux pane id to look from; empty looks from the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to look from when paneId is empty"`
	Direction   string `json:"direction" jsonschema:"the side to look toward"`
}

type findPaneByPositionOutput struct {
	PaneID   string       `json:"paneId"`
	Found    bool         `json:"found"`
	Geometry paneGeometry `json:"geometry"`
}

// findPaneByPosition reads coordinates without changing the active pane.
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

	server := t.tmux()
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

func readPaneGeometry(pane tmux.Pane) paneGeometry {
	formats := pane.Formats()
	left, _ := formats.PaneLeft()
	top, _ := formats.PaneTop()
	width, _ := formats.PaneWidth()
	height, _ := formats.PaneHeight()
	return paneGeometry{Left: left, Top: top, Width: width, Height: height}
}

// tmux leaves a one-cell divider between adjacent panes.
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

func addPositionTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "find_pane_by_position",
		Annotations: readOnly("Find a Neighbouring Pane"),
		Description: "Report the pane bordering one side of another: above, " +
			"below, left, or right. Reads tmux's coordinates, so asking does not " +
			"change which pane is active.",
	}, t.findPaneByPosition)
}
