package mcp

import (
	"context"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// waitProgressInterval is how often a long wait tells the client it is
	// still waiting. A client that asked for progress on a two-minute wait
	// should not go two minutes without hearing anything.
	waitProgressInterval = 2 * time.Second
	progressShutdownWait = time.Second
)

// Progress reports elapsed wait time, not command completion. It is sent only
// when the request carries a progress token.

type progressReporter struct {
	cancel       context.CancelFunc
	done         chan struct{}
	once         sync.Once
	shutdownWait time.Duration
}

// With a progress token, newProgressReporter owns a coordinator until stop or
// timeout; callers must defer stop.
func newProgressReporter(
	ctx context.Context,
	request *mcp.CallToolRequest,
	timeout time.Duration,
	message string,
) *progressReporter {
	return startProgressReporter(
		ctx,
		request,
		timeout,
		message,
		waitProgressInterval,
		nil,
		progressShutdownWait,
	)
}

func startProgressReporter(
	ctx context.Context,
	request *mcp.CallToolRequest,
	timeout time.Duration,
	message string,
	interval time.Duration,
	ticks <-chan time.Time,
	shutdownWait time.Duration,
) *progressReporter {
	progressCtx, cancel := context.WithTimeout(ctx, timeout)
	reporter := &progressReporter{
		cancel:       cancel,
		done:         make(chan struct{}),
		shutdownWait: shutdownWait,
	}
	if request == nil || request.Session == nil || request.Params == nil {
		close(reporter.done)
		return reporter
	}
	token := request.Params.GetProgressToken()
	if token == nil {
		close(reporter.done)
		return reporter
	}

	session := request.Session
	started := time.Now()
	go reporter.run(
		progressCtx,
		session,
		token,
		started,
		timeout,
		message,
		interval,
		ticks,
	)
	return reporter
}

func (r *progressReporter) run(
	ctx context.Context,
	session *mcp.ServerSession,
	token any,
	started time.Time,
	timeout time.Duration,
	message string,
	interval time.Duration,
	ticks <-chan time.Time,
) {
	defer close(r.done)
	var ticker *time.Ticker
	if ticks == nil {
		ticker = time.NewTicker(interval)
		ticks = ticker.C
		defer ticker.Stop()
	}

	var delivery <-chan struct{}
	var pending *mcp.ProgressNotificationParams
	for {
		select {
		case <-ctx.Done():
			r.waitForDelivery(delivery)
			return
		case now, ok := <-ticks:
			if !ok {
				r.waitForDelivery(delivery)
				return
			}
			if ctx.Err() != nil {
				continue
			}
			elapsed := now.Sub(started)
			if elapsed >= timeout {
				r.cancel()
				continue
			}
			next := &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Progress:      elapsed.Seconds(),
				Total:         timeout.Seconds(),
				Message:       message,
			}
			if delivery == nil {
				delivery = deliverProgress(ctx, session, next)
			} else {
				pending = next
			}
		case <-delivery:
			delivery = nil
			if ctx.Err() == nil && pending != nil {
				delivery = deliverProgress(ctx, session, pending)
				pending = nil
			}
		}
	}
}

func deliverProgress(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.ProgressNotificationParams,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The session transport classifies delivery failure.
		_ = session.NotifyProgress(ctx, params)
	}()
	return done
}

func (r *progressReporter) waitForDelivery(done <-chan struct{}) {
	if done == nil {
		return
	}
	timer := time.NewTimer(r.shutdownWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		// The session write budget retires the SDK send independently.
	}
}

func (r *progressReporter) stop() {
	r.once.Do(r.cancel)
	<-r.done
}
