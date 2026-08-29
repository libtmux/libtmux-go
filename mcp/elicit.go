package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Caller-pane writes and teardown require elicitation because they can disrupt
// the terminal carrying the conversation. Clients without elicitation are refused.
// Entering copy mode is guarded; splitting and exit_copy_mode are not.

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

// resolvePaneToDeliver is the target-resolution seam for input tools, applying
// caller-pane and input-state guards. Non-input mutations use
// resolvePaneToWrite so dead panes remain addressable.
//
// tool is explicit because batched requests name the batch; refusals must
// identify the nested tool.
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
	caller, err := t.callerIdentityFor(ctx)
	if err != nil {
		return err
	}
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
func (t *tools) callerPaneOnThisServer(ctx context.Context) (tmux.Pane, bool, error) {
	caller, err := t.callerIdentityFor(ctx)
	if err != nil {
		return tmux.Pane{}, false, err
	}
	if !caller.inside {
		return tmux.Pane{}, false, nil
	}
	socket := t.socketPath(ctx)
	if socket == "" || resolvePath(socket) != caller.socket {
		return tmux.Pane{}, false, nil
	}
	pane, err := t.tmux(ctx).Pane(ctx, tmux.PaneID(caller.paneID))
	if err != nil {
		return tmux.Pane{}, false, err
	}
	return pane, true, nil
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
		// Caller-pane writes fail closed when the client cannot elicit consent.
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

// allowed reports a pane this session already said yes to for the rest of it.
func (t *tools) allowed(request *mcp.CallToolRequest, pane string) bool {
	if request == nil || request.Session == nil {
		return false
	}
	scope, err := t.instance.scope(request.Session)
	if err != nil {
		return false
	}
	scope.mutex.Lock()
	defer scope.mutex.Unlock()
	return !scope.closed && scope.consent[pane]
}

// remember keeps one session's yes about one pane.
func (t *tools) remember(request *mcp.CallToolRequest, pane string) {
	if request == nil || request.Session == nil {
		return
	}
	scope, err := t.instance.scope(request.Session)
	if err != nil {
		return
	}
	scope.mutex.Lock()
	defer scope.mutex.Unlock()
	if !scope.closed {
		scope.consent[pane] = true
	}
}
