package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Buffers bridge client text and panes. Tools are limited to the
// libtmux-mcp- namespace, not to buffers whose provenance can be proved.

// bufferSequence provides unique suffixes for generated buffer names.
var bufferSequence atomic.Int64

// bufferPrefix limits access to one namespace; it does not prove who created a
// colliding name.
const bufferPrefix = "libtmux-mcp-"

// loadBufferInput stages text in a tmux buffer.
type loadBufferInput struct {
	// Text is what to store.
	Text string `json:"text" jsonschema:"the text to store in the buffer"`
	// Name names the buffer so it can be pasted or read back later. Empty
	// makes one up and returns it.
	Name string `json:"name,omitempty" jsonschema:"a name for the buffer; empty makes one up"`
}

// bufferRef identifies a buffer in the server's namespace.
type bufferRef struct {
	// Name is the buffer's tmux name, which paste_buffer and show_buffer take.
	Name string `json:"name"`
	// Bytes is how much it holds.
	Bytes int `json:"bytes"`
}

// loadBuffer stages text without delivering it to a pane.
func (t *tools) loadBuffer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input loadBufferInput,
) (*mcp.CallToolResult, bufferRef, error) {
	if input.Text == "" {
		return nil, bufferRef{}, errors.New("text is required")
	}
	name, err := bufferName(input.Name)
	if err != nil {
		return nil, bufferRef{}, err
	}
	if err := t.tmux(ctx).SetBuffer(ctx, tmux.SetBufferRequest{
		Data: input.Text,
		Name: &name,
	}); err != nil {
		return nil, bufferRef{}, err
	}
	return nil, bufferRef{Name: name, Bytes: len(input.Text)}, nil
}

// showBufferInput reads a buffer back.
type showBufferInput struct {
	// Name is the buffer to read, as load_buffer returned it.
	Name string `json:"name" jsonschema:"the buffer to read, as load_buffer returned it"`
	// MaxLines caps the reply, keeping the last lines.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines to return at most, keeping the last ones"`
	// MaxBytes caps the reply's size, keeping the last lines.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

// showBufferOutput carries a buffer's contents.
type showBufferOutput struct {
	// Name is the buffer that was read.
	Name string `json:"name"`
	// Lines are its contents.
	Lines []string `json:"lines,omitempty"`
	// truncation reports what the bounds dropped.
	truncation
}

// showBuffer reads a buffer in the libtmux-mcp- namespace.
func (t *tools) showBuffer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input showBufferInput,
) (*mcp.CallToolResult, showBufferOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, showBufferOutput{}, err
	}
	name, err := ownBufferName(input.Name)
	if err != nil {
		return nil, showBufferOutput{}, err
	}
	process, err := t.runtime.process(ctx)
	if err != nil {
		return nil, showBufferOutput{}, err
	}
	contents, err := process.ShowBuffer(ctx, &name)
	if err != nil {
		// tmux reports only that show-buffer failed; include the normalized
		// namespace name so the missing target is visible.
		return nil, showBufferOutput{}, fmt.Errorf(
			"no buffer named %q in the libtmux-mcp- namespace: "+
				"use the name load_buffer returned: %w", name, err)
	}
	kept, report := limits.apply(strings.Split(contents, "\n"))
	return textResult(kept), showBufferOutput{
		Name: name, Lines: kept, truncation: report,
	}, nil
}

// pasteBufferInput delivers a buffer into a pane.
type pasteBufferInput struct {
	// Name is the buffer to paste, as load_buffer returned it.
	Name string `json:"name" jsonschema:"the buffer to paste, as load_buffer returned it"`
	// PaneID is the tmux pane id. Empty pastes into the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to paste into; empty uses the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to paste into when paneId is empty"`
	// Delete removes the buffer once it has been pasted.
	Delete bool `json:"delete,omitempty" jsonschema:"remove the buffer once it has been pasted"`
}

// pasteBufferOutput reports the paste.
type pasteBufferOutput struct {
	// Name is the buffer that was pasted.
	Name string `json:"name"`
	// PaneID is the pane it went into.
	PaneID string `json:"paneId"`
}

// pasteBuffer delivers a staged buffer into a pane.
func (t *tools) pasteBuffer(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input pasteBufferInput,
) (*mcp.CallToolResult, pasteBufferOutput, error) {
	name, err := ownBufferName(input.Name)
	if err != nil {
		return nil, pasteBufferOutput{}, err
	}
	pane, err := t.resolvePaneToDeliver(
		ctx, request, input.PaneID, input.SessionName, "pasting a buffer", "paste_buffer")
	if err != nil {
		return nil, pasteBufferOutput{}, err
	}
	if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
		BufferName:  &name,
		DeleteAfter: input.Delete,
		Bracket:     true,
	}); err != nil {
		return nil, pasteBufferOutput{}, err
	}
	return nil, pasteBufferOutput{Name: name, PaneID: pane.ID().String()}, nil
}

// deleteBufferInput removes a buffer.
type deleteBufferInput struct {
	// Name is the buffer to remove, as load_buffer returned it.
	Name string `json:"name" jsonschema:"the buffer to remove, as load_buffer returned it"`
}

// deleteBufferOutput reports the removal.
type deleteBufferOutput struct {
	// Deleted is the buffer that was removed.
	Deleted string `json:"deleted"`
}

// deleteBuffer removes a buffer in the libtmux-mcp- namespace.
func (t *tools) deleteBuffer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input deleteBufferInput,
) (*mcp.CallToolResult, deleteBufferOutput, error) {
	name, err := ownBufferName(input.Name)
	if err != nil {
		return nil, deleteBufferOutput{}, err
	}
	if err := t.tmux(ctx).DeleteBuffer(ctx, &name); err != nil {
		return nil, deleteBufferOutput{}, err
	}
	return nil, deleteBufferOutput{Deleted: name}, nil
}

// bufferName allocates or validates a namespaced buffer name. User names gain
// bufferPrefix so unprefixed foreign buffers remain unreachable.
func bufferName(requested string) (string, error) {
	name := strings.TrimSpace(requested)
	if name == "" {
		return bufferPrefix + strconv.FormatInt(bufferSequence.Add(1), 10), nil
	}
	if err := usableBufferName(requested, name); err != nil {
		return "", err
	}
	if strings.HasPrefix(name, bufferPrefix) {
		return name, nil
	}
	return bufferPrefix + name, nil
}

// usableBufferName rejects backslashes because tmux 3.7+ stores a
// display-escaped spelling that lookup does not accept.
func usableBufferName(requested, name string) error {
	if strings.Contains(name, `\`) {
		return fmt.Errorf(
			"buffer name %q must not contain a backslash: tmux 3.7 stores it "+
				"doubled and looks it up undoubled, so the name would not "+
				"reach the buffer", requested)
	}
	return nil
}

// ownBufferName normalizes a name into the libtmux-mcp- namespace.
func ownBufferName(requested string) (string, error) {
	name := strings.TrimSpace(requested)
	if name == "" {
		return "", errors.New("name is required")
	}
	if !strings.HasPrefix(name, bufferPrefix) {
		// Accept the short form a client passed to load_buffer, which is how
		// it thinks of the buffer, and refuse anything else.
		name = bufferPrefix + name
	}
	if err := usableBufferName(requested, name); err != nil {
		return "", err
	}
	return name, nil
}

// addBufferTools advertises the tools for tmux's paste buffers.
func addBufferTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityTmuxSettings, &mcp.Tool{
		Name:        "load_buffer",
		Annotations: mutating("Stage Text in a tmux Buffer"),
		Description: "Put text into a named tmux buffer, ready to paste into a " +
			"pane once or several times. paste_text is the shorter road when the " +
			"text goes into one pane once.",
	}, t.loadBuffer)
	register(server, t, CapabilityPaneControl, &mcp.Tool{
		Name:        "paste_buffer",
		Annotations: mutating("Paste a tmux Buffer"),
		Description: "Deliver a staged buffer into a pane as text, with no tmux " +
			"key names read. The name load_buffer returned reaches the buffer, " +
			"and so does the short one passed to it.",
	}, t.pasteBuffer)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "show_buffer",
		Annotations: readOnly("Read a tmux Buffer"),
		Description: "Read a buffer in the libtmux-mcp- namespace. Names outside " +
			"it are prefixed before lookup, so other tmux buffers remain unreachable.",
	}, t.showBuffer)
	register(server, t, CapabilityTmuxSettings, &mcp.Tool{
		Name:        "delete_buffer",
		Annotations: settling("Delete a tmux Buffer"),
		Description: "Remove a buffer in the libtmux-mcp- namespace once nothing else " +
			"will paste it. A buffer left behind stays on the tmux server for " +
			"anyone attached to paste by hand, and tmux keeps a bounded stack " +
			"of them, so the oldest is dropped to make room for a new one.",
	}, t.deleteBuffer)
}
