package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Connect starts and tracks one client session. It requires tmux 3.6 or newer
// and rejects an opaque transport unless [AssumeResponseCommit] marks its
// response-write contract. Incoming messages remain gated until the SDK
// session has an installed scope.
func (i *Instance) Connect(
	ctx context.Context,
	transport sdk.Transport,
	options *sdk.ServerSessionOptions,
) (*ServerSession, error) {
	if i == nil {
		return nil, ErrInstanceClosed
	}
	i.mutex.Lock()
	if i.closing {
		i.mutex.Unlock()
		return nil, ErrInstanceClosed
	}
	i.connecting.Add(1)
	i.mutex.Unlock()
	defer i.connecting.Done()

	connectCtx, cancelConnect := context.WithCancel(ctx)
	stopConnect := context.AfterFunc(i.ctx, cancelConnect)
	defer cancelConnect()
	defer stopConnect()
	if i.ctx.Err() != nil {
		cancelConnect()
	}
	versionCtx, cancelVersion := context.WithTimeout(connectCtx, runtimeSetupTimeout)
	err := i.requireTmuxVersion(versionCtx)
	cancelVersion()
	if err != nil {
		return nil, err
	}
	transport, err = normalizeResponseTransport(transport)
	if err != nil {
		return nil, err
	}

	ready := make(chan struct{})
	scope := newSessionScope(i.ctx)
	var responseConnection *sessionReadyConnection
	managedTransport := sessionReadyTransport{
		inner:     transport,
		ready:     ready,
		onRequest: func(message jsonrpc.Message) bool { return i.requestRead(scope, message) },
		onSettled: func(message jsonrpc.Message) { i.responseSettled(scope, message) },
		onConnect: func(connection *sessionReadyConnection) { responseConnection = connection },
		onTerminal: func(err error) {
			scope.terminate(err)
			i.connectionTerminated(scope)
		},
	}
	connected, err := i.server.Connect(
		connectCtx,
		HandshakeOrdered(managedTransport),
		options,
	)
	if err != nil {
		if responseConnection != nil {
			_ = responseConnection.Close()
		}
		close(ready)
		scope.close(i.tools.watchers)
		return nil, err
	}
	session := &ServerSession{
		instance:   i,
		sdk:        connected,
		connection: responseConnection,
		scope:      scope,
		done:       make(chan struct{}),
	}

	i.mutex.Lock()
	if i.closing {
		i.mutex.Unlock()
		close(ready)
		scope.stop()
		_ = connected.Close()
		scope.close(i.tools.watchers)
		return nil, ErrInstanceClosed
	}
	i.sessions[connected] = session
	i.mutex.Unlock()
	close(ready)
	go session.await()
	return session, nil
}

// Run serves one tracked session until the client, context, or instance ends.
// Its transport requirements are the same as [Instance.Connect].
func (i *Instance) Run(ctx context.Context, transport sdk.Transport) error {
	session, err := i.Connect(ctx, transport, nil)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		_ = session.Close()
		return ctx.Err()
	case <-session.done:
		return session.waitErr
	}
}
