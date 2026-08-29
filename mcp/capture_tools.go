package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type capturePaneInput struct {
	PaneID         string `json:"paneId,omitempty" jsonschema:"a tmux pane id such as %1; empty reads the active pane"`
	SessionName    string `json:"sessionName,omitempty" jsonschema:"which session's active pane to read when paneId is empty"`
	IncludeHistory bool   `json:"includeHistory,omitempty" jsonschema:"read scrollback as well as the visible screen"`
	StartLine      *int   `json:"startLine,omitempty" jsonschema:"first row to read; 0 is the top of the screen and negatives are history"`
	EndLine        *int   `json:"endLine,omitempty" jsonschema:"last row to read, on the same scale as startLine"`
	// JoinWrapped delegates to tmux -J; multi-row prompts can be joined
	// incorrectly. Prefer run_command for authored command output.
	JoinWrapped bool `json:"joinWrapped,omitempty" jsonschema:"rejoin lines the terminal wrapped, as tmux does it; a multi-row shell prompt can be joined to the line after it, so prefer run_command for output you ran yourself"`
	Styles      bool `json:"styles,omitempty" jsonschema:"keep colour and attribute escape sequences, for a program that says pass or fail in colour rather than in words"`
	MaxLines    int  `json:"maxLines,omitempty" jsonschema:"how many lines to return at most, keeping the last ones"`
	MaxBytes    int  `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

type capturePaneOutput struct {
	PaneID string   `json:"paneId"`
	Lines  []string `json:"lines,omitempty"`
	truncation
}

// capturePane defaults to the visible screen; history must be explicit and
// remains bounded.
func (t *tools) capturePane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input capturePaneInput,
) (*mcp.CallToolResult, capturePaneOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, capturePaneOutput{}, err
	}
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, capturePaneOutput{}, err
	}
	pane, err = t.processPane(ctx, pane)
	if err != nil {
		return nil, capturePaneOutput{}, err
	}

	request := tmux.CapturePaneRequest{
		JoinWrapped:     input.JoinWrapped,
		EscapeSequences: input.Styles,
	}
	switch {
	case input.StartLine != nil || input.EndLine != nil:
		if input.StartLine != nil {
			request.Start = tmux.CaptureLine(*input.StartLine)
		}
		if input.EndLine != nil {
			request.End = tmux.CaptureLine(*input.EndLine)
		}
	case input.IncludeHistory:
		// Bounds retain the most recent lines from history through the screen.
		request.Start = tmux.CaptureBoundary
	}

	lines, err := pane.Capture(ctx, request)
	if err != nil {
		return nil, capturePaneOutput{}, err
	}
	kept, report := limits.apply(lines)
	return textResult(kept), capturePaneOutput{
		PaneID:     pane.ID().String(),
		Lines:      kept,
		truncation: report,
	}, nil
}

type clearPaneInput struct {
	PaneID      string `json:"paneId,omitempty" jsonschema:"the tmux pane id to clear; empty clears the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to clear when paneId is empty"`
	History     bool   `json:"history,omitempty" jsonschema:"also discard the pane's scrollback"`
}

type clearPaneOutput struct {
	PaneID         string `json:"paneId"`
	HistoryCleared bool   `json:"historyCleared"`
}

// clearPane clears screen output; History additionally discards unrecoverable
// scrollback.
func (t *tools) clearPane(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input clearPaneInput,
) (*mcp.CallToolResult, clearPaneOutput, error) {
	pane, err := t.resolvePaneToWrite(
		ctx, request, input.PaneID, input.SessionName, "clearing it")
	if err != nil {
		return nil, clearPaneOutput{}, err
	}
	if err := pane.Clear(ctx); err != nil {
		return nil, clearPaneOutput{}, err
	}
	if input.History {
		if err := pane.ClearHistory(ctx, tmux.ClearHistoryRequest{}); err != nil {
			return nil, clearPaneOutput{}, fmt.Errorf("screen cleared but history was not: %w", err)
		}
	}
	return nil, clearPaneOutput{
		PaneID:         pane.ID().String(),
		HistoryCleared: input.History,
	}, nil
}

type pipePaneInput struct {
	PaneID      string `json:"paneId,omitempty" jsonschema:"the tmux pane id to pipe; empty uses the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to pipe when paneId is empty"`
	Command     string `json:"command,omitempty" jsonschema:"a shell command to write the pane's output to, such as cat >> /tmp/build.log; empty stops piping"`
}

type pipePaneOutput struct {
	PaneID string `json:"paneId"`
	Piping bool   `json:"piping"`
}

// pipePane starts a long-lived shell command as the tmux user; an empty command
// stops it.
func (t *tools) pipePane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input pipePaneInput,
) (*mcp.CallToolResult, pipePaneOutput, error) {
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, pipePaneOutput{}, err
	}
	request := tmux.PipePaneRequest{OutputOnly: true}
	if strings.TrimSpace(input.Command) != "" {
		command := input.Command
		request.Command = &command
	}
	if err := pane.Pipe(ctx, request); err != nil {
		return nil, pipePaneOutput{}, err
	}
	return nil, pipePaneOutput{
		PaneID: pane.ID().String(),
		Piping: request.Command != nil,
	}, nil
}
