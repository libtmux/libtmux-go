package tmux

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// ErrControlClosed identifies an operation attempted after a control client
// began closing or lost its protocol stream.
var ErrControlClosed = errors.New("tmux: control client is closed")

// ErrOutcomeUnknown identifies a command whose write began but whose reply
// boundary was not observed. The operation may have changed tmux.
var ErrOutcomeUnknown = errors.New("tmux: command outcome is unknown")

// ErrControlReplyCount identifies a call to [ControlClient.Cmd] for a command
// that produced zero or multiple reply frames.
var ErrControlReplyCount = errors.New("tmux: control command did not return exactly one reply")

// ControlClient is one attached tmux control-mode process. Create one with
// [Server.OpenControl]. Concurrent Cmd, Wait, and close calls are supported;
// exactly one caller may execute NextNotification at a time.
type ControlClient struct {
	server     Server
	session    Session
	clientName ClientName

	command       *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	stderr        *controlLockedBuffer
	notifications *controlNotificationQueue
	frames        chan controlFrame
	requests      chan *controlRequest
	stopRequests  chan struct{}
	requestDone   chan struct{}
	closing       chan struct{}
	readDone      chan struct{}
	done          chan struct{}
	closeDone     chan struct{}

	stateMu          sync.Mutex
	readErr          error
	waitErr          error
	currentSessionID SessionID

	closeRequested atomic.Bool
	closeOnce      sync.Once
	closeErr       error
	replyFence     controlReplyFence

	// dispatching excludes flags-0 frames after the attach reply.
	dispatching atomic.Bool
}

// ownReply uses tmux's CMDQ_STATE_CONTROL guard flag. Treating a key-binding
// command as our reply would shift every later reply on the connection.
func (f controlFrame) ownReply() bool {
	return f.flags != 0
}

type controlLockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *controlLockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func (b *controlLockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

// ClientName returns the tmux-assigned identity captured during registration.
func (c *ControlClient) ClientName() ClientName { return c.clientName }

// Server returns the server handle used to start the control client.
func (c *ControlClient) Server() Server { return c.server }

// Session returns the materialized session selected during startup.
func (c *ControlClient) Session() Session { return c.session }
