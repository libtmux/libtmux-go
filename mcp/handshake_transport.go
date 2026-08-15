package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// notificationToolListChanged is the notification tmux tooling has no reason
// to send during a handshake, and which the SDK sends anyway.
const notificationToolListChanged = "notifications/tools/list_changed"

// HandshakeOrdered wraps a transport so the server holds its notifications
// until the client reports itself initialized, which is the order MCP asks for.
func HandshakeOrdered(inner mcp.Transport) mcp.Transport {
	return handshakeOrderedTransport{inner: inner}
}

// handshakeOrderedTransport holds back server notifications until the client
// has finished initializing.
//
// MCP says a server should not send notifications before it receives
// notifications/initialized. The SDK announces the tool list as changed once a
// session exists, which lands between the initialize response and that
// notification, and a client that enforces the rule never finishes connecting:
// the MCP Inspector times out its handshake against an otherwise working
// server. Suppressing this one message is the difference between connecting
// and not.
//
// Held rather than dropped: a client that asked for list-changed notifications
// should still get one, just not before it is ready for it.
type handshakeOrderedTransport struct {
	inner mcp.Transport
}

// Connect wraps the inner transport's connection.
func (t handshakeOrderedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &handshakeOrderedConnection{inner: connection}, nil
}

type handshakeOrderedConnection struct {
	inner       mcp.Connection
	initialized bool
	held        []jsonrpc.Message
}

// Read notes when the client says it is initialized, and releases anything
// held back until then.
func (c *handshakeOrderedConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.inner.Read(ctx)
	if err != nil {
		return nil, err
	}
	if request, ok := message.(*jsonrpc.Request); ok &&
		request.Method == "notifications/initialized" {
		c.initialized = true
		held := c.held
		c.held = nil
		for _, delayed := range held {
			if writeErr := c.inner.Write(ctx, delayed); writeErr != nil {
				return nil, writeErr
			}
		}
	}
	return message, nil
}

// Write holds a tool-list notification sent before the client is initialized,
// and passes everything else through untouched.
func (c *handshakeOrderedConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if !c.initialized {
		if request, ok := message.(*jsonrpc.Request); ok &&
			request.Method == notificationToolListChanged {
			c.held = append(c.held, message)
			return nil
		}
	}
	return c.inner.Write(ctx, message)
}

// Close closes the wrapped connection.
func (c *handshakeOrderedConnection) Close() error { return c.inner.Close() }

// SessionID reports the wrapped connection's session, when it has one.
func (c *handshakeOrderedConnection) SessionID() string { return c.inner.SessionID() }
