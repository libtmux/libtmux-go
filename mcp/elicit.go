package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	"is running in, so %s it reaches the terminal this conversation is " +
	"happening in"

// callerEndGuard is the same for something that ends the pane rather than
// writing to it, where "types into" describes the wrong harm.
const callerEndGuard = "the person declined: %s is the pane this server is " +
	"running in, so %s it closes the terminal this conversation is happening in"

// capitalise starts a sentence with the action it is about.
func capitalise(action string) string {
	if action == "" {
		return action
	}
	return strings.ToUpper(action[:1]) + action[1:]
}

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
	if err := t.confirmCallerWrite(ctx, request, pane, action, true); err != nil {
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
	remembers bool,
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
	if remembers && t.allowed(request, identifier) {
		return nil
	}
	// "there" belongs to a write and reads wrong on a kill, which reaches the
	// same terminal by ending it rather than by typing into it.
	reaches, guard := "typing into", callerWriteGuard
	if !remembers {
		reaches, guard = "ending", callerEndGuard
	}
	return t.askAboutTheCaller(ctx, request, identifier,
		fmt.Sprintf("%s is the pane this MCP server is running in. %s it "+
			"reaches the terminal you are talking to it through. Allow it?",
			identifier, capitalise(action)),
		fmt.Sprintf("%s is the pane this server is running in, so %s it "+
			"reaches the terminal you are talking to it through. This client "+
			"cannot be asked to allow it, so it is refused: name another pane, "+
			"make one with split_window or create_session, or list_panes to find "+
			"one where isCaller is false", identifier, reaches),
		fmt.Sprintf(guard, identifier, reaches), remembers)
}

// confirmCallerLoss asks before ending something the caller's pane is inside.
//
// The write guard names one pane, and everything holding it reaches the same
// terminal one level up: a client refused kill_pane got the same outcome from
// kill_window, kill_session, or kill_server, and was told nothing -- the answer
// never arrived, because the pane carrying the reply had gone. holds is worked
// out by the caller, which already has the container in hand.
func (t *tools) confirmCallerLoss(
	ctx context.Context,
	request *mcp.CallToolRequest,
	holds bool,
	subject string,
) error {
	if !holds || request == nil || request.Session == nil {
		return nil
	}
	return t.askAboutTheCaller(ctx, request, subject,
		fmt.Sprintf("%s holds the pane this MCP server is running in. Ending it "+
			"will close the terminal you are talking to it through. Allow it?",
			subject),
		fmt.Sprintf("%s holds the pane this server is running in, so ending it "+
			"closes the terminal you are talking to it through. This client "+
			"cannot be asked to allow it, so it is refused: get_server_info "+
			"names the pane this server runs in, and list_panes marks it",
			subject),
		fmt.Sprintf("the person declined: %s holds the pane this server is "+
			"running in, so ending it closes the terminal this conversation is "+
			"happening in", subject),
		// Ending something is never remembered. The harm is not the same as a
		// write's: a keystroke into the wrong terminal is disruptive, and
		// ending the terminal ends the conversation that would have said so.
		false)
}

// callerPaneOnThisServer is the caller's own pane, when this process runs in
// one of the panes of the server it drives.
func (t *tools) callerPaneOnThisServer(ctx context.Context) (tmux.Pane, bool) {
	caller := t.callerIdentityFor(ctx)
	if !caller.inside {
		return tmux.Pane{}, false
	}
	socket := t.socketPath(ctx)
	if socket == "" || resolvePath(socket) != caller.socket {
		return tmux.Pane{}, false
	}
	pane, err := t.tmux().Pane(ctx, tmux.PaneID(caller.paneID))
	if err != nil {
		return tmux.Pane{}, false
	}
	return pane, true
}

// askAboutTheCaller puts one yes-or-no question to the person and turns the
// three ways it can end into the three answers a client gets.
func (t *tools) askAboutTheCaller(
	ctx context.Context,
	request *mcp.CallToolRequest,
	identifier, question, unaskable, declined string,
	remembers bool,
) error {
	// A yes-or-no question, except where the yes can be kept: the client
	// renders this as a form either way, so the field costs nothing it had.
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if remembers {
		schema["properties"] = map[string]any{
			"remember": map[string]any{
				"type":        "boolean",
				"title":       "Allow this pane for the rest of the session",
				"description": "Stop asking before writing to this pane. Ending it still asks.",
			},
		}
	}
	result, err := request.Session.Elicit(ctx, &mcp.ElicitParams{
		Message: question,
		// One field, so the person says how long the yes lasts rather than
		// this server deciding. Remembering every accept would widen a
		// consent nobody gave; remembering none is the question again on
		// every keystroke.
		RequestedSchema: schema,
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
		return errors.New(unaskable)
	}
	if result.Action != "accept" {
		return errors.New(declined)
	}
	if remember, ok := result.Content["remember"].(bool); ok && remember && remembers {
		t.remember(request, identifier)
	}
	return nil
}

// rememberedSessions bounds how many clients' answers are kept at once.
//
// Nothing tells this server that a session has gone -- the SDK offers no
// closed hook -- so the map would otherwise grow for the life of a hosted
// server. A stdio server has one session and never reaches this; exceeding it
// costs one client being asked again, which is the safe direction.
const rememberedSessions = 32

// allowed reports a pane this session already said yes to for the rest of it.
func (t *tools) allowed(request *mcp.CallToolRequest, pane string) bool {
	t.consentMutex.Lock()
	defer t.consentMutex.Unlock()
	return t.consented[request.Session][pane]
}

// remember keeps one session's yes about one pane.
func (t *tools) remember(request *mcp.CallToolRequest, pane string) {
	t.consentMutex.Lock()
	defer t.consentMutex.Unlock()
	if t.consented == nil {
		t.consented = map[*mcp.ServerSession]map[string]bool{}
	}
	if _, known := t.consented[request.Session]; !known &&
		len(t.consented) >= rememberedSessions {
		// Forgetting everything rather than choosing whose answer to drop:
		// the choice would need an ordering nothing here keeps, and the cost
		// of being wrong is one question asked again.
		clear(t.consented)
	}
	if t.consented[request.Session] == nil {
		t.consented[request.Session] = map[string]bool{}
	}
	t.consented[request.Session][pane] = true
}
