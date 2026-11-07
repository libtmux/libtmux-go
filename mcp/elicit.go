package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Caller-pane writes and teardown require elicitation because they can disrupt
// the terminal carrying the conversation. Clients without elicitation are refused.

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

// callerQuestion is one yes-or-no question about the caller's pane, with the
// three answers a client gets when it ends.
type callerQuestion struct {
	identifier, consentKey, question, unaskable, declined string
	remembers                                             bool
}

// newWriteQuestion is the question before a write lands in the caller pane.
// With remembers false it is the question before something ends the pane.
func newWriteQuestion(
	pane tmux.Pane,
	action string,
	remembers bool,
	consentKey string,
) callerQuestion {
	identifier := pane.ID().String()
	// "there" belongs to a write and reads wrong on a kill, which reaches the
	// same terminal by ending it rather than by typing into it.
	reaches, guard := "typing into", callerWriteGuard
	if !remembers {
		reaches, guard = "ending", callerEndGuard
	}
	return callerQuestion{
		identifier: identifier,
		consentKey: consentKey,
		question: fmt.Sprintf("%s is the pane this MCP server is running in. %s it "+
			"reaches the terminal you are talking to it through. Allow it?",
			identifier, capitalise(action)),
		unaskable: fmt.Sprintf("%s is the pane this server is running in, so %s it "+
			"reaches the terminal you are talking to it through. This client "+
			"cannot be asked to allow it, so it is refused: name another pane, "+
			"make one with split_window or create_session, or list_panes to find "+
			"one where isCaller is false", identifier, reaches),
		declined:  fmt.Sprintf(guard, identifier, reaches),
		remembers: remembers,
	}
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

func (t *tools) confirmCallerInputPreflight(
	ctx context.Context,
	request *mcp.CallToolRequest,
	preflight paneInputPreflight,
	action string,
) error {
	question, err := t.callerInputQuestion(request, preflight, action)
	if err != nil || question == nil {
		return err
	}
	if err := t.askAboutTheCaller(ctx, request, *question); err != nil {
		return fmt.Errorf(
			"configured pane input membership %v: %w",
			preflight.ConfiguredIDs, err,
		)
	}
	return nil
}

// callerInputQuestion is the question a pane input owes the person. It is nil
// when the input does not reach the caller's pane, or the person already said
// yes to that pane for the rest of the session.
func (t *tools) callerInputQuestion(
	request *mcp.CallToolRequest,
	preflight paneInputPreflight,
	action string,
) (*callerQuestion, error) {
	if preflight.Caller.state == paneInputCallerDetached ||
		preflight.Caller.state == paneInputCallerForeign {
		return nil, nil
	}
	if preflight.Caller.state != paneInputCallerSelected {
		return nil, errors.New("configured pane input caller identity is malformed")
	}
	for _, pane := range preflight.Panes {
		if pane.ID().String() != preflight.Caller.paneID {
			continue
		}
		question := newWriteQuestion(pane, action, true, paneInputConsentKey(preflight.Caller))
		if t.allowed(request, question.consentKey) {
			return nil, nil
		}
		return &question, nil
	}
	return nil, nil
}

func paneInputConsentKey(caller paneInputCaller) string {
	return fmt.Sprintf(
		"pane-input:%s:%d:%d:%s:%s",
		caller.socket, caller.serverPID, caller.serverStartTime,
		caller.sessionID, caller.paneID,
	)
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
	question := newWriteQuestion(pane, action, remembers, pane.ID().String())
	if remembers && t.allowed(request, question.consentKey) {
		return nil
	}
	return t.askAboutTheCaller(ctx, request, question)
}

// confirmCallerLoss asks before ending something the caller's pane is inside.
//
// The write guard names one pane, and everything holding it reaches the same
// terminal one level up: a client refused kill_pane got the same outcome from
// kill_window or kill_session, and was told nothing -- the answer
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
	return t.askAboutTheCaller(ctx, request, callerQuestion{
		identifier: subject,
		consentKey: subject,
		question: fmt.Sprintf("%s holds the pane this MCP server is running in. Ending it "+
			"will close the terminal you are talking to it through. Allow it?",
			subject),
		unaskable: fmt.Sprintf("%s holds the pane this server is running in, so ending it "+
			"closes the terminal you are talking to it through. This client "+
			"cannot be asked to allow it, so it is refused: get_server_info "+
			"names the pane this server runs in, and list_panes marks it",
			subject),
		declined: fmt.Sprintf("the person declined: %s holds the pane this server is "+
			"running in, so ending it closes the terminal this conversation is "+
			"happening in", subject),
		// Ending something is never remembered. The harm is not the same as a
		// write's: a keystroke into the wrong terminal is disruptive, and
		// ending the terminal ends the conversation that would have said so.
		remembers: false,
	})
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

// elicitParams is the form the client renders for a question.
func (q callerQuestion) elicitParams() *mcp.ElicitParams {
	// A yes-or-no question, except where the yes can be kept: the client
	// renders this as a form either way, so the field costs nothing it had.
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if q.remembers {
		schema["properties"] = map[string]any{
			"remember": map[string]any{
				"type":        "boolean",
				"title":       "Allow this pane for the rest of the session",
				"description": "Stop asking before writing to this pane. Ending it still asks.",
			},
		}
	}
	return &mcp.ElicitParams{
		Mode:    "form",
		Message: q.question,
		// One field, so the person says how long the yes lasts rather than
		// this server deciding. Remembering every accept would widen a
		// consent nobody gave; remembering none is the question again on
		// every keystroke.
		RequestedSchema: schema,
	}
}

// inputRequestKey names a question to the client without telling it the
// socket, server pid, or start time that the consent key is built from.
func (q callerQuestion) inputRequestKey() string {
	sum := sha256.Sum256([]byte(q.consentKey + "\x00" + q.question))
	return "consent-" + hex.EncodeToString(sum[:12])
}

// askAboutTheCaller puts one yes-or-no question to the person and turns the
// three ways it can end into the three answers a client gets.
//
// From protocol 2026-07-28 the server may not send elicitation/create itself.
// The question returns as an inputRequiredError, which the tool wrapper turns
// into an input-required result, and the client's answer arrives on the
// retried call.
func (t *tools) askAboutTheCaller(
	ctx context.Context,
	request *mcp.CallToolRequest,
	question callerQuestion,
) error {
	if request == nil || request.Session == nil {
		return errors.New(question.unaskable)
	}
	var result *mcp.ElicitResult
	var err error
	if asksByInputRequest(request) {
		result, err = answerByInputRequest(request, question)
		if need, ok := errors.AsType[*inputRequiredError](err); ok {
			return need
		}
	} else {
		result, err = request.Session.Elicit(ctx, question.elicitParams())
	}
	if err != nil {
		// Caller-pane writes fail closed when the client cannot elicit consent.
		logToClient(ctx, request, "debug", map[string]any{
			"event": "caller-pane write refused, nobody to ask",
			"pane":  question.identifier,
			"why":   err.Error(),
		})
		return errors.New(question.unaskable)
	}
	if result.Action != "accept" {
		return errors.New(question.declined)
	}
	if remember, ok := result.Content["remember"].(bool); ok && remember && question.remembers {
		t.remember(request, question.consentKey)
	}
	return nil
}

// inputRequiredError carries the questions a call needs answered up to the
// tool wrapper, which sends them as the call's result.
type inputRequiredError struct {
	requests map[string]*mcp.ElicitParams
}

func (e *inputRequiredError) Error() string {
	return "input required: a question about the caller's pane is waiting for " +
		"the person, and this call did not ask it up front"
}

// inputRequests is the map an input-required result carries.
func (e *inputRequiredError) inputRequests() mcp.InputRequestMap {
	requests := make(mcp.InputRequestMap, len(e.requests))
	for key, params := range e.requests {
		requests[key] = params
	}
	return requests
}

// firstInputRequestProtocol is the first protocol in which a server asks
// through the result of the call instead of a request of its own.
var firstInputRequestProtocol = time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)

// asksByInputRequest reports whether this call's protocol forbids the server
// sending elicitation/create. A version that is not a date is an older one.
func asksByInputRequest(request *mcp.CallToolRequest) bool {
	if request == nil || request.Session == nil {
		return false
	}
	version, err := time.Parse(time.DateOnly, request.ProtocolVersion())
	return err == nil && !version.Before(firstInputRequestProtocol)
}

// canElicitForm reports whether the client declared form elicitation on this
// call. An empty elicitation capability means form, as it does in the SDK.
func canElicitForm(request *mcp.CallToolRequest) bool {
	capabilities := request.ClientCapabilities()
	if capabilities == nil || capabilities.Elicitation == nil {
		return false
	}
	return capabilities.Elicitation.Form != nil || capabilities.Elicitation.URL == nil
}

// answerByInputRequest returns the client's answer when the retried call
// carries one. Without one, including one of the wrong kind, it asks again.
func answerByInputRequest(
	request *mcp.CallToolRequest,
	question callerQuestion,
) (*mcp.ElicitResult, error) {
	if !canElicitForm(request) {
		return nil, errors.New("client does not support elicitation")
	}
	if answer, ok := inputAnswer(request, question); ok {
		return answer, nil
	}
	return nil, &inputRequiredError{requests: map[string]*mcp.ElicitParams{
		question.inputRequestKey(): question.elicitParams(),
	}}
}

// inputAnswer is the client's answer to a question on this call, if any.
func inputAnswer(request *mcp.CallToolRequest, question callerQuestion) (*mcp.ElicitResult, bool) {
	if request.Params == nil {
		return nil, false
	}
	answer, ok := request.Params.InputResponses[question.inputRequestKey()].(*mcp.ElicitResult)
	return answer, ok && answer != nil
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
