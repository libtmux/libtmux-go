package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// capturePaneInput selects the pane to read and how much of it.
type capturePaneInput struct {
	// PaneID is a stable tmux pane identifier such as %1. Empty reads the
	// active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"a tmux pane id such as %1; empty reads the active pane"`
	// SessionName picks the session whose active pane to read when PaneID is
	// empty and the server has more than one session.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to read when paneId is empty"`
	// IncludeHistory reads the pane's scrollback as well as its screen. The
	// last maxLines of it are returned, so this is how to ask for what
	// scrolled past rather than what is on screen.
	IncludeHistory bool `json:"includeHistory,omitempty" jsonschema:"read scrollback as well as the visible screen"`
	// StartLine and EndLine address tmux's own rows for a caller that wants a
	// particular stretch: 0 is the top visible row, negative rows are history.
	// Most callers want includeHistory and a line count instead.
	StartLine *int `json:"startLine,omitempty" jsonschema:"first row to read; 0 is the top of the screen and negatives are history"`
	// EndLine is the last row to read, on the same scale as StartLine.
	EndLine *int `json:"endLine,omitempty" jsonschema:"last row to read, on the same scale as startLine"`
	// JoinWrapped rejoins a line the terminal wrapped, so a long line arrives
	// as one line rather than as however many the pane is wide.
	//
	// tmux decides which rows were wrapped, from a flag it sets when it wraps
	// one, and this asks it rather than working it out. A prompt that draws
	// several rows without wrapping to reach them can leave a row flagged, so
	// the join can put a prompt's last row and the command typed after it on
	// one line and orphan the command's own wrapped tail on the next. The
	// output of a program is unaffected. Byte-identical to capture-pane -J,
	// which is the point: the seam is tmux's and reproducing it is what keeps
	// a caller's reading of a pane the same as tmux's own.
	JoinWrapped bool `json:"joinWrapped,omitempty" jsonschema:"rejoin lines the terminal wrapped, as tmux does it; a multi-row shell prompt can be joined to the line after it"`
	// Styles keeps the terminal's colour and attribute sequences, which a
	// capture otherwise strips. In a program that reports its state by colour
	// rather than in words, the colour is the state: a red FAILED and a green
	// PASSED are the same six letters without it.
	Styles bool `json:"styles,omitempty" jsonschema:"keep colour and attribute escape sequences, for a program that says pass or fail in colour rather than in words"`
	// MaxLines caps how many lines come back, keeping the last ones. Zero uses
	// the server's default.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines to return at most, keeping the last ones"`
	// MaxBytes caps the reply's size, keeping the last lines. Zero uses the
	// server's default.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

// capturePaneOutput carries the captured text.
type capturePaneOutput struct {
	// PaneID is the pane that was read, which a caller that left it empty did
	// not know.
	PaneID string `json:"paneId"`
	// Lines are the pane's lines, top to bottom.
	Lines []string `json:"lines,omitempty"`
	// truncation reports what the bounds dropped.
	truncation
}

// capturePane reads what a pane holds.
//
// The default is the visible screen, which is what a person looking at the
// terminal sees. Scrollback is a flag rather than the default because a pane
// that has been running for a day holds far more than an answer needs, and the
// bounds keep the end of whatever was asked for.
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
		// From the start of history through the bottom of the screen, which
		// the bounds then trim to the most recent lines.
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

// clearPaneInput selects the pane to clear.
type clearPaneInput struct {
	// PaneID is the tmux pane id to clear. Empty clears the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to clear; empty clears the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to clear when paneId is empty"`
	// History also drops the pane's scrollback, which no later capture can
	// read back and which capture_since reports as lines it missed.
	History bool `json:"history,omitempty" jsonschema:"also discard the pane's scrollback"`
}

// clearPaneOutput reports the pane that was cleared.
type clearPaneOutput struct {
	// PaneID is the pane this call cleared.
	PaneID string `json:"paneId"`
	// HistoryCleared reports whether the scrollback went with the screen.
	HistoryCleared bool `json:"historyCleared"`
}

// clearPane wipes a pane's screen, and its scrollback when asked.
//
// An agent that has filled a pane with output it has already read clears it so
// the next capture is only the next thing, rather than paying for the old
// output on every read. Clearing the screen alone leaves the scrollback, so
// nothing is lost; clearing the history discards it, which is why that is a
// separate flag rather than part of the same word.
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

// pipePaneInput sends a pane's output to a command.
type pipePaneInput struct {
	// PaneID is the tmux pane id. Empty pipes the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to pipe; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to pipe when paneId is empty"`
	// Command is the shell command tmux writes the pane's output to, such as
	// "cat >> /tmp/build.log". Empty stops an existing pipe.
	Command string `json:"command,omitempty" jsonschema:"a shell command to write the pane's output to, such as cat >> /tmp/build.log; empty stops piping"`
}

// pipePaneOutput reports the pipe.
type pipePaneOutput struct {
	// PaneID is the pane that was piped.
	PaneID string `json:"paneId"`
	// Piping reports whether output is now going somewhere.
	Piping bool `json:"piping"`
}

// pipePane sends everything a pane writes to a command as well as to the
// screen.
//
// It is the answer to output that will not fit in scrollback. A pane's history
// has a limit, and a build that prints more than that loses the beginning
// before anyone reads it; a pipe writes every byte to a file as it happens,
// and nothing depends on reading the pane in time.
//
// The command runs on the machine tmux runs on, as the user running tmux, and
// keeps running until the pipe is stopped or the pane ends. That is more than
// most tools here do, which is why it is annotated as changing tmux rather
// than reading it.
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
