package mcp

import (
	"context"
	"fmt"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The one pane where being wrong is not recoverable.
//
// Every pane summary carries isCaller, and until now that was the whole of the
// protection: the server said which pane it was running in and trusted whoever
// read it not to type into it. That is a note in a reply, and a model with
// forty tools and a task does not always read the note. Typing into the caller
// pane feeds keystrokes to the terminal the conversation is happening in --
// interrupting the client, or answering a prompt nobody saw -- and no later
// call undoes it.
//
// So a write to that pane asks first. MCP has a way to ask: elicitation puts
// the question to the person, in their own client, and answers accept, decline,
// or cancel.
//
// What counts as a write is what reaches the person's keyboard or their shell:
// keys, pasted text, a command, a restart, a clear, an ending, and entering
// copy mode, which takes their keystrokes away from their shell. Splitting the
// pane is not one -- an agent that finds its own pane and makes room beside it
// is the ordinary opening move -- and neither is exit_copy_mode, which is the
// way out of the one mode that is.
//
// A client that cannot be asked is not blocked. Elicitation is negotiated at
// initialize, so a client that did not declare it gets the behaviour it had
// before, which is the write going through with isCaller reported beside it.
// This is a guard rail rather than a boundary, and the package documentation
// says plainly that the tools are not a sandbox: a caller with send_keys can
// run anything the user can. Refusing every write on every client that cannot
// answer would break them all to enforce something that was never enforceable.

// callerWriteGuard is what a caller is told when the person says no.
const callerWriteGuard = "the person declined: %s is the pane this server " +
	"is running in, so %s there types into the terminal this conversation is " +
	"happening in"

// resolvePaneToWrite resolves the pane a write is aimed at and, when that pane
// is this server's own, asks the person before letting it through.
//
// Write tools call this instead of resolvePane, so a tool added later is
// guarded by the way it finds its target rather than by remembering to ask.
func (t *tools) resolvePaneToWrite(
	ctx context.Context,
	request *mcp.CallToolRequest,
	id, sessionName, action string,
) (tmux.Pane, error) {
	pane, err := t.resolvePane(ctx, id, sessionName)
	if err != nil {
		return tmux.Pane{}, err
	}
	if err := t.confirmCallerWrite(ctx, request, pane, action); err != nil {
		return tmux.Pane{}, err
	}
	return pane, nil
}

// resolvePaneToDeliver resolves the pane a keystroke is aimed at and refuses
// one that cannot read it.
//
// Tools that type call this instead of resolvePaneToWrite, for the same reason
// write tools call that instead of resolvePane: a tool added later is guarded
// by the way it finds its target rather than by remembering to ask. Delivering
// a tool is what it means to be one of these, so the set cannot be listed
// wrongly or fall out of date.
//
// The tool names itself rather than being read off the request, because a
// batched call carries the batch's name and a refusal has to say which of its
// calls stopped it.
//
// Tools that change a pane without typing into it keep resolvePaneToWrite. A
// dead pane is a reasonable target for clearing, for respawning, and for
// entering a mode: a person attached to that session scrolls a corpse by hand,
// which no capture does for them.
func (t *tools) resolvePaneToDeliver(
	ctx context.Context,
	request *mcp.CallToolRequest,
	id, sessionName, action, tool string,
) (tmux.Pane, error) {
	pane, err := t.resolvePaneToWrite(ctx, request, id, sessionName, action)
	if err != nil {
		return tmux.Pane{}, err
	}
	if err := refuseAPaneThatCannotRead(pane, tool); err != nil {
		return tmux.Pane{}, err
	}
	return pane, nil
}

// confirmCallerWrite asks the person before a write lands in the caller pane.
func (t *tools) confirmCallerWrite(
	ctx context.Context,
	request *mcp.CallToolRequest,
	pane tmux.Pane,
	action string,
) error {
	// A batch carries the request it was called with down to each of its
	// calls, so a batched write is asked about like any other. Without a
	// session there is nobody to put the question to.
	if request == nil || request.Session == nil {
		return nil
	}
	caller := t.callerIdentityFor(ctx)
	isCaller := caller.isCaller(pane, t.socketPath(ctx))
	if isCaller == nil || !*isCaller {
		return nil
	}

	identifier := pane.ID().String()
	result, err := request.Session.Elicit(ctx, &mcp.ElicitParams{
		Message: fmt.Sprintf(
			"%s is the pane this MCP server is running in. %s there will reach "+
				"the terminal you are talking to it through. Allow it?",
			identifier, action),
		// No fields to fill in: the answer is the action, and asking for a
		// value as well would make a yes-or-no question into a form.
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	})
	if err != nil {
		// A client that cannot be asked is refused rather than waved through.
		// Letting it through made the guard advisory exactly where it matters
		// most: the client least able to warn its person is the one that types
		// into their terminal unannounced, which is what happened -- a session
		// identifying its own server ran a command against the caller pane and
		// put the text in its user's prompt box. Writing to the terminal the
		// conversation is happening in is almost always a mistake, and naming
		// another pane is the whole of the way out.
		logToClient(ctx, request, "debug", map[string]any{
			"event": "caller-pane write refused, nobody to ask",
			"pane":  identifier,
			"why":   err.Error(),
		})
		return fmt.Errorf(
			"%s is the pane this server is running in, so %s there types into "+
				"the terminal you are talking to it through. This client cannot "+
				"be asked to allow it, so it is refused: name another pane, make "+
				"one with split_window or create_session, or list_panes to find "+
				"one where isCaller is false",
			identifier, action)
	}
	if result.Action != "accept" {
		return fmt.Errorf(callerWriteGuard, identifier, action)
	}
	return nil
}
