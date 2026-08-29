package mcp

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrInstanceClosed identifies work refused after instance shutdown starts.
var ErrInstanceClosed = errors.New("libtmux MCP instance is closed")

const terminalResponseDrainTimeout = 5 * time.Second

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
	drainTimer  *time.Timer
	drainWait   time.Duration
	sessions    map[*sdk.ServerSession]*ServerSession
	connecting  sync.WaitGroup
	closeOnce   sync.Once
	closeDone   chan struct{}
	closeErr    error
}

func newInstance() *Instance {
	ctx, cancel := context.WithCancel(context.Background())
	return &Instance{
		sessions:  map[*sdk.ServerSession]*ServerSession{},
		responses: responseLedger{},
		closeDone: make(chan struct{}),
		drainWait: terminalResponseDrainTimeout,
		ctx:       ctx,
		cancel:    cancel,
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
