package mcp

import "github.com/libtmux/libtmux-go/tmux"

// paneGeometry uses tmux terminal-cell coordinates; pane indexes do not encode
// spatial order.
type paneGeometry struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type findPaneByPositionOutput struct {
	PaneID   string       `json:"paneId"`
	Found    bool         `json:"found"`
	Geometry paneGeometry `json:"geometry"`
}

func readPaneGeometry(pane tmux.Pane) paneGeometry {
	formats := pane.Formats()
	left, _ := formats.PaneLeft()
	top, _ := formats.PaneTop()
	width, _ := formats.PaneWidth()
	height, _ := formats.PaneHeight()
	return paneGeometry{Left: left, Top: top, Width: width, Height: height}
}
