package mcp

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// ErrInstanceClosed identifies work refused after instance shutdown starts.
	ErrInstanceClosed = errors.New("libtmux MCP instance is closed")
	// ErrRequestCapacity identifies a client session closed after it exceeded
	// the server's finite unsettled-call capacity.
	ErrRequestCapacity = errors.New("libtmux MCP request capacity exceeded")
)

const (
	terminalResponseDrainTimeout = 5 * time.Second
	defaultMaxSessionCalls       = 32
	defaultMaxInstanceCalls      = 128
)

// Instance owns the SDK server, all connected client sessions, and every
// resource allocated on their behalf. The SDK server is composed so Connect
// and Run cannot bypass lifecycle tracking.
type Instance struct {
	server  *sdk.Server
	tools   *tools
	runtime *tmuxRuntime
	audit   io.Closer
	ctx     context.Context
	cancel  context.CancelFunc

	mutex       sync.Mutex
	closing     bool
	terminalErr error
	responses   responseLedger
	// Calls are admitted before the SDK's unbounded handler queue. These fields
	// are private so tests can exercise small limits without widening the API.
	maxSessionCalls  int
	maxInstanceCalls int
	drainTimer       *time.Timer
	drainWait        time.Duration
	sessions         map[*sdk.ServerSession]*ServerSession
	connecting       sync.WaitGroup
	closeOnce        sync.Once
	closeDone        chan struct{}
	closeErr         error
}

func newInstance() *Instance {
	ctx, cancel := context.WithCancel(context.Background())
	return &Instance{
		sessions:         map[*sdk.ServerSession]*ServerSession{},
		responses:        responseLedger{},
		closeDone:        make(chan struct{}),
		drainWait:        terminalResponseDrainTimeout,
		maxSessionCalls:  defaultMaxSessionCalls,
		maxInstanceCalls: defaultMaxInstanceCalls,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// ServerSession owns one SDK session and the resources scoped to its client.
type ServerSession struct {
	instance   *Instance
	sdk        *sdk.ServerSession
	connection *sessionReadyConnection
	scope      *sessionScope

	finishOnce sync.Once
	done       chan struct{}
	waitErr    error
}
