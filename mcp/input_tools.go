package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Input tools distinguish a command plus Enter, a sequence of tmux key names,
// and literal text that tmux must not interpret as keys.

// sendKeysBatchInput sends several keys to a pane in order.
type sendKeysBatchInput struct {
	// PaneID is the tmux pane id. Empty sends to the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to send to; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to send to when paneId is empty"`
	// Keys are tmux key names sent in order, such as ["C-c", "q", "Escape"].
	// Nothing is appended, so a client driving a program that reads lines adds
	// "Enter" itself.
	Keys []string `json:"keys" jsonschema:"tmux key names to send in order, such as [\"C-c\", \"q\", \"Enter\"]"`
	// Literal sends the keys as characters rather than looking their names up,
	// so "Escape" is those six letters.
	Literal bool `json:"literal,omitempty" jsonschema:"send the keys as characters rather than as tmux key names"`
}

// sendKeysBatchOutput reports what was sent.
type sendKeysBatchOutput struct {
	// PaneID is the pane that received the keys.
	PaneID string `json:"paneId"`
	// Sent is how many keys tmux accepted when the call succeeds.
	Sent int `json:"sent"`
	// ResolvedPaneIDs lists every pane that received input after tmux applied
	// synchronize-panes.
	ResolvedPaneIDs []string `json:"resolvedPaneIds"`
}

// sendKeysBatch sends a sequence of keys without pressing Enter.
//
// A program that reads keys rather than lines — an editor, a pager, a menu —
// is driven by key names in order, and send_keys cannot express that: it
// appends Enter, so every key would be its own line. This is what lets a
// client answer a prompt, quit a pager, or leave an editor.
func (t *tools) sendKeysBatch(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input sendKeysBatchInput,
) (*mcp.CallToolResult, sendKeysBatchOutput, error) {
	if len(input.Keys) == 0 {
		return nil, sendKeysBatchOutput{}, errors.New("keys is required")
	}
	pane, err := t.resolvePaneToDeliver(ctx, request, input.PaneID, input.SessionName, "sending keys", "send_keys_batch")
	if err != nil {
		return nil, sendKeysBatchOutput{}, err
	}
	for index, key := range input.Keys {
		if key == "" {
			return nil, sendKeysBatchOutput{
					PaneID: pane.ID().String(), Sent: index,
					ResolvedPaneIDs: []string{pane.ID().String()},
				},
				fmt.Errorf("key %d is empty", index)
		}
	}
	resolved, err := t.resolvedPaneInputTargets(ctx, pane)
	if err != nil {
		return nil, sendKeysBatchOutput{
			PaneID: pane.ID().String(), ResolvedPaneIDs: []string{pane.ID().String()},
		}, fmt.Errorf("resolve synchronized pane targets: %w", err)
	}
	if err := pane.SendKeySequence(ctx, tmux.SendKeySequenceRequest{
		Keys: input.Keys, Literal: input.Literal,
	}); err != nil {
		return nil, sendKeysBatchOutput{PaneID: pane.ID().String(), ResolvedPaneIDs: resolved},
			fmt.Errorf("sending keys: %w", err)
	}
	return nil, sendKeysBatchOutput{
		PaneID: pane.ID().String(), Sent: len(input.Keys), ResolvedPaneIDs: resolved,
	}, nil
}

func (t *tools) resolvedPaneInputTargets(ctx context.Context, pane tmux.Pane) ([]string, error) {
	options, err := pane.Options(ctx)
	if err != nil {
		return nil, err
	}
	synchronized, present := options.SynchronizePanes().Get()
	if !present || !synchronized {
		return []string{pane.ID().String()}, nil
	}
	window, err := t.tmux(ctx).Window(ctx, pane.WindowID())
	if err != nil {
		return nil, err
	}
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(panes))
	for _, resolved := range panes {
		targets = append(targets, resolved.ID().String())
	}
	slices.Sort(targets)
	return targets, nil
}

// pasteSequence names each staged buffer apart from the last, so two pastes at
// once do not overwrite one another's text before either is delivered.
var pasteSequence atomic.Int64

// pasteTextInput delivers text into a pane as text.
type pasteTextInput struct {
	// PaneID is the tmux pane id. Empty pastes into the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to paste into; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to paste into when paneId is empty"`
	// Text is delivered exactly, with no key names read.
	Text string `json:"text" jsonschema:"the text to deliver, taken literally"`
	// Bracket wraps the paste in the codes that tell a program the text was
	// pasted rather than typed, which stops an editor auto-indenting it into
	// a staircase. It is on unless a caller turns it off, and programs that
	// did not ask for bracketed paste never see the codes.
	Bracket *bool `json:"bracket,omitempty" jsonschema:"mark the text as pasted so an editor does not auto-indent it; on by default"`
	// Enter presses Enter after the text, which is what turns a pasted command
	// into a command that runs.
	Enter bool `json:"enter,omitempty" jsonschema:"press Enter after the text"`
}

// pasteTextOutput reports what was pasted.
type pasteTextOutput struct {
	// PaneID is the pane that received the text.
	PaneID string `json:"paneId"`
	// Bytes is how many bytes were delivered.
	Bytes int `json:"bytes"`
}

// pasteText stages text in a per-call buffer so tmux cannot interpret key
// names, then removes the buffer after delivery.
func (t *tools) pasteText(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input pasteTextInput,
) (*mcp.CallToolResult, pasteTextOutput, error) {
	if input.Text == "" {
		return nil, pasteTextOutput{}, errors.New("text is required")
	}
	pane, err := t.resolvePaneToDeliver(ctx, request, input.PaneID, input.SessionName, "pasting text", "paste_text")
	if err != nil {
		return nil, pasteTextOutput{}, err
	}
	output := pasteTextOutput{PaneID: pane.ID().String()}

	server := t.tmux(ctx)
	name := "libtmux-mcp-paste-" + strconv.FormatInt(pasteSequence.Add(1), 10)
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{
		Data: input.Text,
		Name: &name,
	}); err != nil {
		return nil, output, err
	}
	// Deleted with the paste rather than left behind: tmux keeps buffers until
	// something drops them, and a client pasting repeatedly would fill a
	// person's buffer list with text they never copied.
	bracket := true
	if input.Bracket != nil {
		bracket = *input.Bracket
	}
	if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
		BufferName:  &name,
		DeleteAfter: true,
		Bracket:     bracket,
	}); err != nil {
		cleanupErr := server.DeleteBuffer(ctx, &name)
		return nil, output, errors.Join(err, cleanupErr)
	}
	if input.Enter {
		enter := "Enter"
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
			Command:   &enter,
			SkipEnter: true,
		}); err != nil {
			t.runtime.observe(err)
			// The text arrived; only the Enter did not. Reporting the paste as
			// a failure would invite a client to send it again.
			return toolFailure(fmt.Errorf("text pasted but Enter was not sent: %w", err)),
				pasteTextOutput{PaneID: pane.ID().String(), Bytes: len(input.Text)}, nil
		}
	}
	output.Bytes = len(input.Text)
	return nil, output, nil
}

// exitCopyModeInput omits fields that only entering copy mode can use.
type exitCopyModeInput struct {
	// PaneID is the tmux pane id. Empty uses the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to use when paneId is empty"`
}

// enterCopyModeInput puts a pane into copy mode.
type enterCopyModeInput struct {
	// PaneID is the tmux pane id. Empty uses the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to use when paneId is empty"`
	// ScrollUp enters copy mode one page above the bottom, which is where a
	// client that wants to look at what scrolled past wants to start.
	ScrollUp bool `json:"scrollUp,omitempty" jsonschema:"enter one page above the bottom"`
}

// copyModeOutput reports the pane's mode.
type copyModeOutput struct {
	// PaneID is the pane whose mode changed.
	PaneID string `json:"paneId"`
	// InCopyMode reports the mode the pane is in now.
	InCopyMode bool `json:"inCopyMode"`
}

// enterCopyMode redirects subsequent keys to tmux rather than the pane's
// program. get_pane_info reports inMode; exit_copy_mode restores input.
func (t *tools) enterCopyMode(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input enterCopyModeInput,
) (*mcp.CallToolResult, copyModeOutput, error) {
	// Guarded like a write, because it is one from the person's side: copy
	// mode takes their keystrokes away from their shell. exit_copy_mode is
	// deliberately not guarded, being the way out of exactly that.
	pane, err := t.resolvePaneToWrite(
		ctx, request, input.PaneID, input.SessionName, "entering copy mode")
	if err != nil {
		return nil, copyModeOutput{}, err
	}
	if err := pane.CopyMode(ctx, tmux.CopyModeRequest{ScrollUp: input.ScrollUp}); err != nil {
		return nil, copyModeOutput{}, err
	}
	return nil, copyModeOutput{PaneID: pane.ID().String(), InCopyMode: true}, nil
}

// refuseAPaneThatCannotRead rejects dead or modal panes before input can be
// lost or interpreted as tmux bindings.
func refuseAPaneThatCannotRead(pane tmux.Pane, tool string) error {
	formats := pane.Formats()
	// Before the mode, because a pane can be dead and in a mode at once -- a
	// corpse is scrollable -- and having no process is the more fundamental of
	// the two: leaving the mode would not give the keys anywhere to go.
	if dead, _ := formats.PaneDead(); dead {
		return fmt.Errorf(
			"pane %s has no process: its program exited, so it reads no keys and "+
				"%s reaches nothing. respawn_pane restarts it, and capture_pane "+
				"with includeHistory still reads what it printed",
			pane.ID(), tool)
	}
	mode, ok := formats.PaneInMode()
	if !ok || mode == 0 {
		return nil
	}
	return fmt.Errorf(
		"pane %s is in a mode, so %s would be read as that mode's key bindings "+
			"rather than reaching the program. To read scrollback, capture_pane "+
			"with includeHistory and startLine reads it without leaving the mode "+
			"or sending anything; to reach the program, exit_copy_mode first",
		pane.ID(), tool)
}

// exitCopyMode returns a pane to passing keys to the program in it.
func (t *tools) exitCopyMode(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input exitCopyModeInput,
) (*mcp.CallToolResult, copyModeOutput, error) {
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, copyModeOutput{}, err
	}
	if err := pane.CopyMode(ctx, tmux.CopyModeRequest{Cancel: true}); err != nil {
		return nil, copyModeOutput{}, err
	}
	return nil, copyModeOutput{PaneID: pane.ID().String()}, nil
}

// addInputTools advertises the tools that put something into a pane.
