package mcp

import (
	"context"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Progress reports elapsed wait time, not command completion. It is sent only
// when the request carries a progress token.

type progressReporter struct {
	stopped chan struct{}
}

// With a progress token, newProgressReporter owns a goroutine until stop or
// timeout; callers must defer stop.
func newProgressReporter(
	request *mcp.CallToolRequest,
	timeout time.Duration,
	message string,
) *progressReporter {
	reporter := &progressReporter{stopped: make(chan struct{})}
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
				// Progress loss must not fail the underlying wait.
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

func (r *progressReporter) stop() {
	select {
	case <-r.stopped:
	default:
		close(r.stopped)
	}
}
