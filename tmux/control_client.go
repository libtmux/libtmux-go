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
// began closing or lost its protocol stream. A notification read after either
// end matches it, so one errors.Is check covers a stream the caller closed and
// one tmux ended; [ErrControlStreamLost] tells the second apart.
var ErrControlClosed = errors.New("tmux: control client is closed")

// ErrControlStreamLost identifies a [ControlClient.NextNotification] stream
// that ended before the caller asked to close it, distinct from the clean
// end an explicit Close or CloseContext produces. Such an error also matches
// [ErrControlClosed]. It is most often the
// underlying tmux client process exiting because the server it was attached
// to did.
var ErrControlStreamLost = errors.New("tmux: control stream lost")

// ErrOutcomeUnknown identifies a command whose write began but whose reply
// boundary was not observed. The operation may have changed tmux.
var ErrOutcomeUnknown = errors.New("tmux: command outcome is unknown")

// ErrControlReplyCount identifies a call to [ControlClient.Cmd] for a command
// that produced zero or multiple reply frames.
var ErrControlReplyCount = errors.New("tmux: control command did not return exactly one reply")

// ControlClient is one attached tmux control-mode process. Create one with
// [Server.OpenControl]. Concurrent Cmd, Wait, close, and NextNotification calls
// are supported; concurrent notification reads are serialized.
//
// It is the low-level handle: it carries commands and notifications on one
// client and can [ControlClient.Reconnect], but records do not run through it.
// [Session.OpenControl] returns a [Connection] instead, whose lanes the
// sessions, windows and panes obtained from it use for their own commands.
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
	// lastExitReason is the tail of the most recent %exit notification read
	// off the wire, tracked as notifications arrive rather than as a caller
	// consumes them. Empty means either none arrived or tmux sent a bare
	// %exit with no reason.
	lastExitReason string

	closeRequested atomic.Bool
	closeOnce      sync.Once
	closeErr       error
	replyFence     controlReplyFence

	// parser reads the stream. It lives here so a request can arm it to
	// capture a command's output; see [controlCapture].
	parser controlStreamParser

	// dispatching excludes flags-0 frames after the attach reply.
	dispatching atomic.Bool

	// outputFrom, once set, names the one pane whose output this client
	// keeps; output from every other pane is dropped as it arrives. A pane
	// observation sets it on the client it opened for itself. tmux sends a
	// control client the output of every pane in its session, and a busy
	// sibling would otherwise fill the observation's queue and end it.
	outputFrom atomic.Pointer[string]
}

// outputFromAnotherPane reports whether record is pane output that this
// client was told to drop.
func (c *ControlClient) outputFromAnotherPane(record []byte) bool {
	kept := c.outputFrom.Load()
	if kept == nil {
		return false
	}
	var rest []byte
	switch {
	case bytes.HasPrefix(record, []byte("%output ")):
		rest = record[len("%output "):]
	case bytes.HasPrefix(record, []byte("%extended-output ")):
		rest = record[len("%extended-output "):]
	default:
		return false
	}
	pane, _, _ := bytes.Cut(rest, []byte(" "))
	return string(pane) != *kept
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
