package mcp

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// notificationToolListChanged is emitted by the SDK during handshake.
const notificationToolListChanged = "notifications/tools/list_changed"

// HandshakeOrdered holds the SDK's early tool-list notification until
// notifications/initialized; strict legacy clients reject the earlier ordering.
func HandshakeOrdered(inner mcp.Transport) mcp.Transport {
	return handshakeOrderedTransport{inner: inner}
}

// handshakeOrderedTransport delays SDK tool-list notifications until
// notifications/initialized rather than dropping them.
type handshakeOrderedTransport struct {
	inner mcp.Transport
}

func (t handshakeOrderedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &handshakeOrderedConnection{inner: connection}, nil
}

type handshakeOrderedConnection struct {
	inner       mcp.Connection
	mutex       sync.Mutex
	initialized bool
	held        jsonrpc.Message
}

func (c *handshakeOrderedConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.inner.Read(ctx)
	if err != nil {
		return nil, err
	}
	if request, ok := message.(*jsonrpc.Request); ok &&
		request.Method == "notifications/initialized" {
		c.mutex.Lock()
		c.initialized = true
		held := c.held
		c.held = nil
		if held != nil {
			if writeErr := c.inner.Write(ctx, held); writeErr != nil {
				c.mutex.Unlock()
				return nil, writeErr
			}
		}
		c.mutex.Unlock()
	}
	return message, nil
}

func (c *handshakeOrderedConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if !c.initialized {
		if request, ok := message.(*jsonrpc.Request); ok &&
			request.Method == notificationToolListChanged {
			c.held = message
			return nil
		}
	}
	return c.inner.Write(ctx, message)
}

func (c *handshakeOrderedConnection) Close() error { return c.inner.Close() }

func (c *handshakeOrderedConnection) SessionID() string { return c.inner.SessionID() }
