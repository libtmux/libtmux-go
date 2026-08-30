package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var errOpaqueGateTransportReached = errors.New("opaque gate transport reached")

type opaqueGateTransport struct {
	calls atomic.Int32
}

func (t *opaqueGateTransport) Connect(context.Context) (sdk.Connection, error) {
	t.calls.Add(1)
	return nil, errOpaqueGateTransportReached
}

func TestSDKIOTransportRejectsAmbiguousJSONRPCBatches(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`
	const call = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	writer := nopClose{io.Discard}
	transport, err := normalizeResponseTransport(&sdk.IOTransport{
		Reader: io.NopCloser(strings.NewReader(batch + "\n" + call + "\n")),
		Writer: writer,
	})
	if err != nil {
		t.Fatal(err)
	}
	framed, ok := transport.(*sdk.IOTransport)
	if !ok {
		t.Fatalf("normalizeResponseTransport() = %T, want *mcp.IOTransport", transport)
	}
	passed, err := io.ReadAll(framed.Reader)
	if !errors.Is(err, errJSONRPCBatchUnsupported) {
		t.Fatalf("Read() error = %v, want errJSONRPCBatchUnsupported", err)
	}
	if len(passed) != 0 {
		t.Fatalf("framed input = %q, want no message after the batch", passed)
	}
}

func TestLoggingTransportRejectsAmbiguousJSONRPCBatches(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`
	transport, err := normalizeResponseTransport(&sdk.LoggingTransport{
		Transport: &sdk.IOTransport{
			Reader: io.NopCloser(strings.NewReader(batch + "\n")),
			Writer: nopClose{io.Discard},
		},
		Writer: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	logging, ok := transport.(*sdk.LoggingTransport)
	if !ok {
		t.Fatalf("normalizeResponseTransport() = %T, want *mcp.LoggingTransport", transport)
	}
	framed, ok := logging.Transport.(*sdk.IOTransport)
	if !ok {
		t.Fatalf("logging transport delegate = %T, want *mcp.IOTransport", logging.Transport)
	}
	if _, err := io.ReadAll(framed.Reader); !errors.Is(err, errJSONRPCBatchUnsupported) {
		t.Fatalf("Read() error = %v, want errJSONRPCBatchUnsupported", err)
	}
}

func TestHandshakeOrderedTransportCannotBypassSingleMessageFraming(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`
	transport, err := normalizeResponseTransport(HandshakeOrdered(&sdk.IOTransport{
		Reader: io.NopCloser(strings.NewReader(batch + "\n")),
		Writer: nopClose{io.Discard},
	}))
	if err != nil {
		t.Fatal(err)
	}
	handshake, ok := transport.(handshakeOrderedTransport)
	if !ok {
		t.Fatalf("normalizeResponseTransport() = %T, want handshakeOrderedTransport", transport)
	}
	framed, ok := handshake.inner.(*sdk.IOTransport)
	if !ok {
		t.Fatalf("handshake delegate = %T, want *mcp.IOTransport", handshake.inner)
	}
	if _, err := io.ReadAll(framed.Reader); !errors.Is(err, errJSONRPCBatchUnsupported) {
		t.Fatalf("Read() error = %v, want errJSONRPCBatchUnsupported", err)
	}
}

func TestAssumeResponseCommitCannotBypassKnownFraming(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`
	ioTransport := func() sdk.Transport {
		return &sdk.IOTransport{
			Reader: io.NopCloser(strings.NewReader(batch + "\n")),
			Writer: nopClose{io.Discard},
		}
	}
	tests := map[string]func() sdk.Transport{
		"marked IO": func() sdk.Transport { return AssumeResponseCommit(ioTransport()) },
		"marked logging IO": func() sdk.Transport {
			return AssumeResponseCommit(&sdk.LoggingTransport{Transport: ioTransport(), Writer: io.Discard})
		},
		"logging marked IO": func() sdk.Transport {
			return &sdk.LoggingTransport{Transport: AssumeResponseCommit(ioTransport()), Writer: io.Discard}
		},
		"marked handshake IO": func() sdk.Transport { return AssumeResponseCommit(HandshakeOrdered(ioTransport())) },
		"handshake marked IO": func() sdk.Transport { return HandshakeOrdered(AssumeResponseCommit(ioTransport())) },
		"double-marked IO":    func() sdk.Transport { return AssumeResponseCommit(AssumeResponseCommit(ioTransport())) },
	}
	for name, transport := range tests {
		t.Run(name, func(t *testing.T) {
			normalized, err := normalizeResponseTransport(transport())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadAll(knownTransportReader(t, normalized)); !errors.Is(
				err, errJSONRPCBatchUnsupported,
			) {
				t.Fatalf("Read() error = %v, want errJSONRPCBatchUnsupported", err)
			}
		})
	}

	stdioTests := map[string]func() sdk.Transport{
		"marked stdio": func() sdk.Transport { return AssumeResponseCommit(&sdk.StdioTransport{}) },
		"marked logging stdio": func() sdk.Transport {
			return AssumeResponseCommit(&sdk.LoggingTransport{Transport: &sdk.StdioTransport{}, Writer: io.Discard})
		},
		"logging marked stdio": func() sdk.Transport {
			return &sdk.LoggingTransport{Transport: AssumeResponseCommit(&sdk.StdioTransport{}), Writer: io.Discard}
		},
		"marked handshake stdio": func() sdk.Transport { return AssumeResponseCommit(HandshakeOrdered(&sdk.StdioTransport{})) },
		"handshake marked stdio": func() sdk.Transport { return HandshakeOrdered(AssumeResponseCommit(&sdk.StdioTransport{})) },
		"double-marked stdio":    func() sdk.Transport { return AssumeResponseCommit(AssumeResponseCommit(&sdk.StdioTransport{})) },
	}
	for name, transport := range stdioTests {
		t.Run(name, func(t *testing.T) {
			normalized, err := normalizeResponseTransport(transport())
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := knownTransportReader(t, normalized).(*jsonLineReader); !ok {
				t.Fatalf("normalized stdio reader is not batch-rejecting: %T", normalized)
			}
		})
	}
}

func knownTransportReader(t *testing.T, transport sdk.Transport) io.Reader {
	t.Helper()
	switch transport := transport.(type) {
	case *sdk.IOTransport:
		return transport.Reader
	case *sdk.LoggingTransport:
		return knownTransportReader(t, transport.Transport)
	case handshakeOrderedTransport:
		return knownTransportReader(t, transport.inner)
	case assumedResponseCommitTransport:
		return knownTransportReader(t, transport.inner)
	default:
		t.Fatalf("normalized transport contains unknown %T", transport)
		return nil
	}
}

func TestInstanceConnectRejectsATwoCallIOBatch(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`
	target := mustInternalTmuxServer(t, executableFixtureOptions(t, fixtureVersion36, tmux.ServerOptions{
		SocketName: "batch-connect-unused",
	}))
	instance := mustInternalMCPServer(t, target)
	var replies bytes.Buffer
	session, err := instance.Connect(t.Context(), &sdk.IOTransport{
		Reader: io.NopCloser(strings.NewReader(batch + "\n")),
		Writer: nopClose{&replies},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- session.Wait() }()
	select {
	case err := <-waited:
		if !errors.Is(err, errJSONRPCBatchUnsupported) {
			t.Fatalf("Wait() error = %v, want errJSONRPCBatchUnsupported", err)
		}
	case <-time.After(time.Second):
		t.Fatal("two-call batch did not terminate the session")
	}
	if replies.Len() != 0 {
		t.Fatalf("batch produced replies before rejection: %q", replies.String())
	}
}

func TestResponseTransportRejectsMalformedKnownTransports(t *testing.T) {
	var nilLogging *sdk.LoggingTransport
	var nilIO *sdk.IOTransport
	var nilReader *io.PipeReader
	var nilWriter *io.PipeWriter
	var nilLogWriter *bytes.Buffer
	tests := map[string]sdk.Transport{
		"typed-nil logging": nilLogging,
		"typed-nil IO":      nilIO,
		"nil logging transport": &sdk.LoggingTransport{
			Writer: io.Discard,
		},
		"nil logging writer": &sdk.LoggingTransport{
			Transport: &sdk.IOTransport{
				Reader: io.NopCloser(strings.NewReader("")),
				Writer: nopClose{io.Discard},
			},
		},
		"typed-nil logging writer": &sdk.LoggingTransport{
			Transport: &sdk.IOTransport{
				Reader: io.NopCloser(strings.NewReader("")),
				Writer: nopClose{io.Discard},
			},
			Writer: nilLogWriter,
		},
		"nil IO reader": &sdk.IOTransport{
			Writer: nopClose{io.Discard},
		},
		"typed-nil IO reader": &sdk.IOTransport{
			Reader: nilReader,
			Writer: nopClose{io.Discard},
		},
		"nil IO writer": &sdk.IOTransport{
			Reader: io.NopCloser(strings.NewReader("")),
		},
		"typed-nil IO writer": &sdk.IOTransport{
			Reader: io.NopCloser(strings.NewReader("")),
			Writer: nilWriter,
		},
	}
	for name, transport := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeResponseTransport(transport); !errors.Is(
				err, ErrResponseCommitUnknown,
			) {
				t.Fatalf("normalizeResponseTransport() error = %v, want ErrResponseCommitUnknown", err)
			}
		})
	}
}

func TestAssumeResponseCommitRejectsNilAndAllowsDoubleWrap(t *testing.T) {
	if _, err := normalizeResponseTransport(AssumeResponseCommit(nil)); !errors.Is(
		err, ErrResponseCommitUnknown,
	) {
		t.Fatalf("nil marker error = %v, want ErrResponseCommitUnknown", err)
	}
	doubleNil, err := normalizeResponseTransport(
		AssumeResponseCommit(AssumeResponseCommit(nil)),
	)
	if err == nil {
		_, err = doubleNil.Connect(t.Context())
	}
	if !errors.Is(err, ErrResponseCommitUnknown) {
		t.Fatalf("double nil marker error = %v, want ErrResponseCommitUnknown", err)
	}

	inner := &opaqueGateTransport{}
	transport, err := normalizeResponseTransport(
		AssumeResponseCommit(AssumeResponseCommit(inner)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Connect(t.Context()); !errors.Is(err, errOpaqueGateTransportReached) {
		t.Fatalf("double marker Connect() error = %v, want delegate error", err)
	}
	if calls := inner.calls.Load(); calls != 1 {
		t.Fatalf("double marker delegate calls = %d, want 1", calls)
	}
}

func TestAssumeResponseCommitRejectsMalformedDelegates(t *testing.T) {
	var typedNil *opaqueGateTransport
	tests := map[string]sdk.Transport{
		"typed-nil opaque": typedNil,
		"incomplete IO": &sdk.IOTransport{
			Reader: io.NopCloser(strings.NewReader("")),
		},
		"incomplete logging": &sdk.LoggingTransport{
			Transport: &opaqueGateTransport{},
		},
		"incomplete IO below logging and marker": &sdk.LoggingTransport{
			Transport: AssumeResponseCommit(&sdk.IOTransport{
				Reader: io.NopCloser(strings.NewReader("")),
			}),
			Writer: io.Discard,
		},
	}
	for name, delegate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeResponseTransport(
				AssumeResponseCommit(delegate),
			); !errors.Is(err, ErrResponseCommitUnknown) {
				t.Fatalf("normalizeResponseTransport() error = %v, want ErrResponseCommitUnknown", err)
			}
		})
	}
}

func TestAssumeResponseCommitTypedNilConnectDoesNotPanic(t *testing.T) {
	var typedNil *opaqueGateTransport
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Connect() panic = %v, want ErrResponseCommitUnknown", recovered)
		}
	}()
	if _, err := AssumeResponseCommit(typedNil).Connect(t.Context()); !errors.Is(
		err,
		ErrResponseCommitUnknown,
	) {
		t.Fatalf("Connect() error = %v, want ErrResponseCommitUnknown", err)
	}
}

func TestAssumeResponseCommitKeepsValidOpaqueLoggingDelegate(t *testing.T) {
	wrappers := map[string]func(sdk.Transport) sdk.Transport{
		"marker outside logging": func(inner sdk.Transport) sdk.Transport {
			return AssumeResponseCommit(&sdk.LoggingTransport{
				Transport: inner, Writer: io.Discard,
			})
		},
		"marker inside logging": func(inner sdk.Transport) sdk.Transport {
			return &sdk.LoggingTransport{
				Transport: AssumeResponseCommit(inner), Writer: io.Discard,
			}
		},
	}
	for name, wrap := range wrappers {
		t.Run(name, func(t *testing.T) {
			inner := &opaqueGateTransport{}
			transport, err := normalizeResponseTransport(wrap(inner))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := transport.Connect(t.Context()); !errors.Is(
				err, errOpaqueGateTransportReached,
			) {
				t.Fatalf("Connect() error = %v, want opaque delegate error", err)
			}
			if calls := inner.calls.Load(); calls != 1 {
				t.Fatalf("opaque delegate calls = %d, want 1", calls)
			}
		})
	}
}
