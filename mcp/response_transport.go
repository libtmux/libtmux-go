package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrResponseCommitUnknown identifies a transport whose successful Write may
// leave one response buffered behind another response in a JSON-RPC batch.
var ErrResponseCommitUnknown = errors.New(
	"libtmux MCP: transport does not guarantee independent response writes",
)

// AssumeResponseCommit marks transport as committing each response before its
// Connection.Write returns nil. It does not change transport behavior. Use it
// only for a transport that rejects JSON-RPC batches or writes every response
// independently; direct IO and stdio transports are handled automatically.
func AssumeResponseCommit(transport mcp.Transport) mcp.Transport {
	return assumedResponseCommitTransport{inner: transport}
}

type assumedResponseCommitTransport struct {
	inner mcp.Transport
}

func (t assumedResponseCommitTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	if err := validateAssumedResponseTransport(t.inner); err != nil {
		return nil, err
	}
	return t.inner.Connect(ctx)
}

func validateAssumedResponseTransport(transport mcp.Transport) error {
	if nilInterface(transport) {
		return fmt.Errorf("%w: nil %T", ErrResponseCommitUnknown, transport)
	}
	switch transport := transport.(type) {
	case handshakeOrderedTransport:
		return validateAssumedResponseTransport(transport.inner)
	case *mcp.LoggingTransport:
		if nilInterface(transport.Writer) {
			return fmt.Errorf("%w: incomplete *mcp.LoggingTransport", ErrResponseCommitUnknown)
		}
		return validateAssumedResponseTransport(transport.Transport)
	case *mcp.IOTransport:
		if nilInterface(transport.Reader) || nilInterface(transport.Writer) {
			return fmt.Errorf("%w: incomplete *mcp.IOTransport", ErrResponseCommitUnknown)
		}
		return nil
	case assumedResponseCommitTransport:
		return validateAssumedResponseTransport(transport.inner)
	default:
		return nil
	}
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	dynamic := reflect.ValueOf(value)
	switch dynamic.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return dynamic.IsNil()
	default:
		return false
	}
}

// normalizeResponseTransport keeps known IO transports on one-message framing
// and refuses opaque transports without an explicit response-commit contract.
func normalizeResponseTransport(transport mcp.Transport) (mcp.Transport, error) {
	return normalizeResponseTransportTrustingOpaque(transport, false)
}

func normalizeResponseTransportTrustingOpaque(
	transport mcp.Transport,
	trustedOpaque bool,
) (mcp.Transport, error) {
	if nilInterface(transport) {
		return nil, fmt.Errorf("%w: nil %T", ErrResponseCommitUnknown, transport)
	}
	switch transport := transport.(type) {
	case handshakeOrderedTransport:
		inner, err := normalizeResponseTransportTrustingOpaque(
			transport.inner,
			trustedOpaque,
		)
		return handshakeOrderedTransport{inner: inner}, err
	case *mcp.LoggingTransport:
		if nilInterface(transport.Transport) || nilInterface(transport.Writer) {
			return nil, fmt.Errorf("%w: incomplete *mcp.LoggingTransport", ErrResponseCommitUnknown)
		}
		inner, err := normalizeResponseTransportTrustingOpaque(
			transport.Transport,
			trustedOpaque,
		)
		if err != nil {
			return nil, err
		}
		return &mcp.LoggingTransport{
			Transport: inner,
			Writer:    transport.Writer,
		}, nil
	case *mcp.IOTransport:
		if nilInterface(transport.Reader) || nilInterface(transport.Writer) {
			return nil, fmt.Errorf("%w: incomplete *mcp.IOTransport", ErrResponseCommitUnknown)
		}
		if _, wrapped := transport.Reader.(*jsonLineReader); wrapped {
			return transport, nil
		}
		return &mcp.IOTransport{
			Reader: wholeJSONLines(transport.Reader, io.Discard),
			Writer: transport.Writer,
		}, nil
	case *mcp.StdioTransport:
		return stdio(), nil
	case assumedResponseCommitTransport:
		return normalizeResponseTransportTrustingOpaque(transport.inner, true)
	default:
		if trustedOpaque {
			return assumedResponseCommitTransport{inner: transport}, nil
		}
		return nil, fmt.Errorf("%w: %T", ErrResponseCommitUnknown, transport)
	}
}
