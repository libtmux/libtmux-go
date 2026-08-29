package mcp

import "github.com/modelcontextprotocol/go-sdk/jsonrpc"

// responseLedger tracks read calls until their replies settle at the transport
// Write boundary or become impossible after connection termination.
// Instance.mutex guards it.
type responseLedger map[*sessionScope]map[jsonrpc.ID]struct{}

func (l responseLedger) read(scope *sessionScope, message jsonrpc.Message) {
	request, ok := message.(*jsonrpc.Request)
	if !ok || !request.IsCall() {
		return
	}
	requests := l[scope]
	if requests == nil {
		requests = map[jsonrpc.ID]struct{}{}
		l[scope] = requests
	}
	requests[request.ID] = struct{}{}
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

func (i *Instance) requestRead(scope *sessionScope, message jsonrpc.Message) bool {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.closing {
		return false
	}
	i.responses.read(scope, message)
	return true
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
