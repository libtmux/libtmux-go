package mcp

import (
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// responseLedger tracks read calls until their replies settle at the transport
// Write boundary or become impossible after connection termination.
// Instance.mutex guards it.
type responseLedger map[*sessionScope]map[jsonrpc.ID]struct{}

var errDuplicateRequestID = errors.New("duplicate unsettled MCP request ID")

func (l responseLedger) admit(
	scope *sessionScope,
	message jsonrpc.Message,
	maxSessionCalls int,
	maxInstanceCalls int,
) error {
	request, ok := message.(*jsonrpc.Request)
	if !ok || !request.IsCall() {
		return nil
	}
	requests := l[scope]
	if _, duplicate := requests[request.ID]; duplicate {
		// The SDK forgets an ID immediately before it writes the response. Refuse
		// reuse until the wrapped write commits so that gap cannot create an
		// accepted call with no ledger entry.
		return fmt.Errorf("%w: %v", errDuplicateRequestID, request.ID)
	}
	if len(requests) >= maxSessionCalls {
		return fmt.Errorf(
			"%w: session has %d unsettled calls (limit %d)",
			ErrRequestCapacity, len(requests), maxSessionCalls,
		)
	}
	if total := l.count(); total >= maxInstanceCalls {
		return fmt.Errorf(
			"%w: server has %d unsettled calls (limit %d)",
			ErrRequestCapacity, total, maxInstanceCalls,
		)
	}
	if requests == nil {
		requests = map[jsonrpc.ID]struct{}{}
		l[scope] = requests
	}
	requests[request.ID] = struct{}{}
	return nil
}

func (l responseLedger) settled(scope *sessionScope, message jsonrpc.Message) {
	response, ok := message.(*jsonrpc.Response)
	if !ok {
		return
	}
	requests := l[scope]
	delete(requests, response.ID)
	if len(requests) == 0 {
		delete(l, scope)
	}
}

func (l responseLedger) terminate(scope *sessionScope) { delete(l, scope) }

func (l responseLedger) count() int {
	total := 0
	for _, requests := range l {
		total += len(requests)
	}
	return total
}

func (i *Instance) requestRead(scope *sessionScope, message jsonrpc.Message) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.closing {
		return ErrInstanceClosed
	}
	return i.responses.admit(
		scope, message, i.maxSessionCalls, i.maxInstanceCalls,
	)
}

func (i *Instance) responseSettled(scope *sessionScope, message jsonrpc.Message) {
	i.mutex.Lock()
	i.responses.settled(scope, message)
	finished := i.terminalErr != nil && i.responses.count() == 0
	i.mutex.Unlock()
	if finished {
		i.startClose()
	}
}

func (i *Instance) connectionTerminated(scope *sessionScope) {
	i.mutex.Lock()
	i.responses.terminate(scope)
	finished := i.terminalErr != nil && i.responses.count() == 0
	i.mutex.Unlock()
	if finished {
		i.startClose()
	}
}
