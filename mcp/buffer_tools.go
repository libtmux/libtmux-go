package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	tmux "github.com/tmux-python/libtmux/golang"
)

// tmux's paste buffers, which is how text gets from anywhere to a pane.
//
// A buffer is what a person's copy lands in and what a paste comes from, so
// this is the shared surface between what someone selected in their terminal
// and what a client can read. It is also how a client stages something too
// large or too awkward to type: load_buffer puts it there, paste_buffer
// delivers it.
//
// There is no tool that lists buffers. A person's buffers hold whatever they
// have copied, which is their clipboard history and none of a client's
// business; a client reads back the buffers it named itself, which it can do
// because it knows their names.

// bufferSequence names each buffer this server creates apart from the last.
var bufferSequence atomic.Int64

// bufferPrefix marks the buffers this server made, so show_buffer and
// delete_buffer can be limited to them without a client having to be trusted
// to stay out of a person's own.
const bufferPrefix = "libtmux-mcp-"

// loadBufferInput stages text in a tmux buffer.
type loadBufferInput struct {
	// Text is what to store.
	Text string `json:"text" jsonschema:"the text to store in the buffer"`
	// Name names the buffer so it can be pasted or read back later. Empty
	// makes one up and returns it.
	Name string `json:"name,omitempty" jsonschema:"a name for the buffer; empty makes one up"`
}

// bufferRef identifies a buffer this server owns.
type bufferRef struct {
	// Name is the buffer's tmux name, which paste_buffer and show_buffer take.
	Name string `json:"name"`
	// Bytes is how much it holds.
	Bytes int `json:"bytes"`
}

// loadBuffer puts text into a named tmux buffer.
//
// It is a staging step rather than a delivery: nothing reaches a pane until
// paste_buffer, which is what makes it useful for text a client wants to
// deliver more than once or into more than one pane.
func (t *tools) loadBuffer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input loadBufferInput,
) (*mcp.CallToolResult, bufferRef, error) {
	if input.Text == "" {
		return nil, bufferRef{}, fmt.Errorf("text is required")
	}
	name, err := bufferName(input.Name)
	if err != nil {
		return nil, bufferRef{}, err
	}
	if err := t.strict().SetBuffer(ctx, tmux.SetBufferRequest{
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

// showBuffer reads back a buffer this server created.
//
// Only this server's own buffers, which is the point: tmux's buffer list is
// where a person's copies land, and a tool that read any buffer by name would
// be a tool for reading what someone copied.
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
	contents, err := t.strict().ShowBuffer(ctx, &name)
	if err != nil {
		return nil, showBufferOutput{}, err
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
	_ *mcp.CallToolRequest,
	input pasteBufferInput,
) (*mcp.CallToolResult, pasteBufferOutput, error) {
	name, err := ownBufferName(input.Name)
	if err != nil {
		return nil, pasteBufferOutput{}, err
	}
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
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

// deleteBuffer removes a buffer this server created.
func (t *tools) deleteBuffer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input deleteBufferInput,
) (*mcp.CallToolResult, deleteBufferOutput, error) {
	name, err := ownBufferName(input.Name)
	if err != nil {
		return nil, deleteBufferOutput{}, err
	}
	if err := t.strict().DeleteBuffer(ctx, &name); err != nil {
		return nil, deleteBufferOutput{}, err
	}
	return nil, deleteBufferOutput{Deleted: name}, nil
}

// bufferName validates a name a client chose, or makes one up.
//
// tmux reads a buffer name as a plain argument, so whitespace would be read as
// further arguments and a leading dash as a flag. The prefix is added rather
// than required, so a client that named a buffer "notes" gets a buffer it can
// find again and a person's own buffers keep their names to themselves.
func bufferName(requested string) (string, error) {
	name := strings.TrimSpace(requested)
	if name == "" {
		return bufferPrefix + strconv.FormatInt(bufferSequence.Add(1), 10), nil
	}
	if strings.ContainsAny(name, " \t\n") {
		return "", fmt.Errorf("buffer name %q must not contain whitespace", requested)
	}
	if strings.HasPrefix(name, bufferPrefix) {
		return name, nil
	}
	return bufferPrefix + name, nil
}

// ownBufferName refuses a name this server did not create.
func ownBufferName(requested string) (string, error) {
	name := strings.TrimSpace(requested)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if !strings.HasPrefix(name, bufferPrefix) {
		// Accept the short form a client passed to load_buffer, which is how
		// it thinks of the buffer, and refuse anything else.
		name = bufferPrefix + name
	}
	if strings.ContainsAny(name, " \t\n") {
		return "", fmt.Errorf("buffer name %q must not contain whitespace", requested)
	}
	return name, nil
}

// addBufferTools advertises the tools for tmux's paste buffers.
func addBufferTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "load_buffer",
		Annotations: mutating("Stage Text in a tmux Buffer"),
		Description: "Put text into a named tmux buffer, ready to paste into a " +
			"pane once or several times. paste_text is the shorter road when the " +
			"text goes into one pane once.",
	}, t.loadBuffer)
	register(server, t, &mcp.Tool{
		Name:        "paste_buffer",
		Annotations: mutating("Paste a tmux Buffer"),
		Description: "Deliver a staged buffer into a pane as text, with no tmux " +
			"key names read.",
	}, t.pasteBuffer)
	register(server, t, &mcp.Tool{
		Name:        "show_buffer",
		Annotations: readOnly("Read a tmux Buffer"),
		Description: "Read back a buffer this server staged. Only those: tmux's " +
			"other buffers hold whatever a person copied.",
	}, t.showBuffer)
	register(server, t, &mcp.Tool{
		Name:        "delete_buffer",
		Annotations: mutating("Delete a tmux Buffer"),
		Description: "Remove a buffer this server staged.",
	}, t.deleteBuffer)
}
