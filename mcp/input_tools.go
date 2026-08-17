package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Three ways to put something into a pane, because they are not the same
// thing and picking the wrong one is how an agent corrupts a file.
//
// send_keys types a command and presses Enter, and tmux reads its own key
// names on the way, so "C-c" is an interrupt rather than three characters.
// send_keys_batch is the same lookup without the Enter, for driving a program
// that reads keys rather than lines. paste_text delivers text as text, with no
// key names read at all, which is the only safe way to put arbitrary content
// into a pane: a line of it beginning with "Escape" would otherwise be a key.

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
	// Sent is how many keys were delivered.
	Sent int `json:"sent"`
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
		return nil, sendKeysBatchOutput{}, fmt.Errorf("keys is required")
	}
	pane, err := t.resolvePaneToWrite(ctx, request, input.PaneID, input.SessionName, "sending keys")
	if err != nil {
		return nil, sendKeysBatchOutput{}, err
	}
	for index, key := range input.Keys {
		if key == "" {
			return nil, sendKeysBatchOutput{PaneID: pane.ID().String(), Sent: index},
				fmt.Errorf("key %d is empty", index)
		}
		name := key
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
			Command:   &name,
			SkipEnter: true,
			Literal:   input.Literal,
		}); err != nil {
			// The keys already sent were delivered and cannot be taken back,
			// so the count says how far it got rather than implying none were.
			return nil, sendKeysBatchOutput{PaneID: pane.ID().String(), Sent: index},
				fmt.Errorf("sending key %q: %w", key, err)
		}
	}
	return nil, sendKeysBatchOutput{PaneID: pane.ID().String(), Sent: len(input.Keys)}, nil
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

// pasteText puts text into a pane without tmux reading any of it as keys.
//
// send_keys looks up tmux key names, so text containing "Escape", "C-c", or a
// leading dash is not delivered as itself. Anything a client did not write by
// hand — a file, a message, a generated command — goes through here, because
// the client cannot know in advance whether it contains a word tmux claims.
//
// The text is staged in a tmux buffer and pasted from it, which is how tmux
// delivers bytes rather than keys. The buffer is named for this call and
// removed afterwards, so it does not join the buffers a person pastes from by
// hand.
func (t *tools) pasteText(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input pasteTextInput,
) (*mcp.CallToolResult, pasteTextOutput, error) {
	if input.Text == "" {
		return nil, pasteTextOutput{}, fmt.Errorf("text is required")
	}
	pane, err := t.resolvePaneToWrite(ctx, request, input.PaneID, input.SessionName, "pasting text")
	if err != nil {
		return nil, pasteTextOutput{}, err
	}
	output := pasteTextOutput{PaneID: pane.ID().String()}

	server := t.target
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
		_ = server.DeleteBuffer(ctx, &name)
		return nil, output, err
	}
	if input.Enter {
		enter := "Enter"
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
			Command:   &enter,
			SkipEnter: true,
		}); err != nil {
			// The text arrived; only the Enter did not. Reporting the paste as
			// a failure would invite a client to send it again.
			return toolFailure(fmt.Errorf("text pasted but Enter was not sent: %w", err)),
				pasteTextOutput{PaneID: pane.ID().String(), Bytes: len(input.Text)}, nil
		}
	}
	output.Bytes = len(input.Text)
	return nil, output, nil
}

// exitCopyModeInput returns a pane to passing keys to the program in it.
//
// Entering and leaving take separate types even though leaving needs nothing
// extra, because sharing one put scrollUp in the schema of a tool that cannot
// act on it, and a client reading the schema had no way to know that.
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

// enterCopyMode puts a pane into copy mode.
//
// A pane in copy mode reads keys as tmux's own rather than passing them to the
// program in it, which is what a client needs in order to scroll and select.
// It is also a trap worth naming: a client that leaves a pane in copy mode has
// left a person's keystrokes going somewhere they do not expect, and every
// send_keys after it goes to tmux rather than to the shell. get_pane_info
// reports inMode, and exit_copy_mode is the way back.
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

// sendKeys types a command into a pane and presses Enter.
func (t *tools) sendKeys(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input sendKeysInput,
) (*mcp.CallToolResult, sendKeysOutput, error) {
	if strings.TrimSpace(input.Command) == "" {
		return nil, sendKeysOutput{}, fmt.Errorf("command is required")
	}
	pane, err := t.resolvePaneToWrite(ctx, request, input.PaneID, input.SessionName, "sending keys")
	if err != nil {
		return nil, sendKeysOutput{}, err
	}
	command := input.Command
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command:         &command,
		SuppressHistory: input.SuppressHistory,
	}); err != nil {
		return nil, sendKeysOutput{}, err
	}
	return nil, sendKeysOutput{PaneID: pane.ID().String(), Sent: command}, nil
}

// sendKeysInput selects the pane and the command to run in it.
type sendKeysInput struct {
	// PaneID is a stable tmux pane identifier such as %1. Empty types into the
	// active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"a tmux pane id such as %1; empty types into the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to type into when paneId is empty"`
	// SuppressHistory prefixes the command with a space, which a shell
	// configured to ignore such lines keeps out of its history. An agent
	// typing into a person's pane otherwise fills their history with commands
	// they did not run.
	SuppressHistory bool `json:"suppressHistory,omitempty" jsonschema:"keep the command out of the shell's history by prefixing a space"`
	// Command is typed into the pane, then Enter is sent. tmux reads its own
	// key names here, so "C-c" interrupts what the pane is running and
	// "Escape" is a key rather than those letters. That is also the way back
	// from a pane left holding a command by a run_command that timed out,
	// which would otherwise time out every later command sent to it.
	Command string `json:"command" jsonschema:"what to type, read as tmux key names: \"C-c\" interrupts the pane and \"Escape\" is a key rather than those letters; use paste_text for text to take literally"`
}

// sendKeysOutput reports what was sent.
type sendKeysOutput struct {
	// PaneID is the pane that received it, which a caller that left it empty
	// did not know.
	PaneID string `json:"paneId"`
	// Sent is the command that was typed into the pane.
	Sent string `json:"sent"`
}

// addInputTools advertises the tools that put something into a pane.
func addInputTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "send_keys",
		Annotations: mutating("Send Keys to a Pane"),
		Description: "Type into one pane and press Enter. tmux key names are " +
			"read, so \"C-c\" interrupts what the pane is running, which is how " +
			"to recover a pane left busy by a run_command that timed out. Use " +
			"paste_text for content you did not write by hand, and run_command " +
			"when you want the exit status.",
	}, t.sendKeys)
	register(server, t, &mcp.Tool{
		Name:        "send_keys_batch",
		Annotations: mutating("Send a Sequence of Keys"),
		Description: "Send several tmux key names to a pane in order, with no " +
			"Enter appended. This is how to drive a program that reads keys " +
			"rather than lines: quit a pager, answer a prompt, leave an editor.",
	}, t.sendKeysBatch)
	register(server, t, &mcp.Tool{
		Name:        "paste_text",
		Annotations: mutating("Paste Text into a Pane"),
		Description: "Deliver text into a pane exactly, with no tmux key names " +
			"read. Use this for anything you did not write by hand: a word like " +
			"\"Escape\" in the middle of it would otherwise be sent as that key.",
	}, t.pasteText)
	register(server, t, &mcp.Tool{
		Name:        "enter_copy_mode",
		Annotations: mutating("Enter Copy Mode"),
		Description: "Put a pane into tmux's copy mode, where keys scroll and " +
			"select rather than reaching the program in the pane. Leave it with " +
			"exit_copy_mode: keys sent to a pane still in copy mode do not reach " +
			"the shell.",
	}, t.enterCopyMode)
	register(server, t, &mcp.Tool{
		Name:        "exit_copy_mode",
		Annotations: mutating("Leave Copy Mode"),
		Description: "Return a pane from copy mode to passing keys to the " +
			"program running in it.",
	}, t.exitCopyMode)
}
