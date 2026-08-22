package mcp

import (
	"context"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A long wait tells the client it is still waiting.
//
// run_command and wait_for_text can hold a request open for minutes, and a
// client with nothing coming back cannot tell a slow command from a hung
// server. MCP's answer is a progress notification, which a client asks for by
// putting a token on its call; without one, nothing is sent and this costs
// nothing.
//
// The progress is time against the deadline rather than work done, because
// nothing here knows how far along a command is. That is still the number a
// client wants: it says how much of the wait is left before the answer becomes
// a timeout.

// progressReporter sends progress until it is stopped.
type progressReporter struct {
	stopped chan struct{}
}

// newProgressReporter starts reporting on a request, if the client asked.
//
// The reporter owns a goroutine that ends when stop is called, which every
// caller does with a defer. A caller that forgot would leak it for the length
// of the timeout and no longer, since the ticker loop gives up there too.
func newProgressReporter(
	request *mcp.CallToolRequest,
	timeout time.Duration,
	message string,
) *progressReporter {
	reporter := &progressReporter{stopped: make(chan struct{})}
	// A client that did not send a token did not ask to hear about this, and
	// a batched call reports under the token its batch was given.
	if request == nil || request.Session == nil || request.Params == nil {
		return reporter
	}
	token := request.Params.GetProgressToken()
	if token == nil {
		return reporter
	}

	session := request.Session
	started := time.Now()
	go func() {
		ticker := time.NewTicker(waitProgressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-reporter.stopped:
				return
			case now := <-ticker.C:
				elapsed := now.Sub(started)
				if elapsed >= timeout {
					return
				}
				// A failure here means the client is gone, which the call
				// itself will discover; losing a progress line is not worth
				// ending the wait it describes.
				_ = session.NotifyProgress(context.Background(), &mcp.ProgressNotificationParams{
					ProgressToken: token,
					Progress:      elapsed.Seconds(),
					Total:         timeout.Seconds(),
					Message:       message,
				})
			}
		}
	}()
	return reporter
}

// stop ends the reporting.
func (r *progressReporter) stop() {
	select {
	case <-r.stopped:
	default:
		close(r.stopped)
	}
}
