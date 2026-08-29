package mcp

import (
	"context"

	"github.com/libtmux/libtmux-go/tmux"
)

// openObservation registers a dedicated pane stream before Instance shutdown
// can begin waiting for owned resources.
func (r *tmuxRuntime) openObservation(
	ctx context.Context,
	pane tmux.Pane,
) (*tmux.PaneObservation, error) {
	observation, err := pane.OpenObservation(ctx)
	if err != nil {
		return nil, err
	}
	r.mutex.Lock()
	if r.state == runtimeTerminal || r.state == runtimeClosed {
		err = r.stateErrorLocked()
		r.mutex.Unlock()
		_ = observation.Close()
		return nil, err
	}
	r.observations.Add(1)
	r.mutex.Unlock()
	return observation, nil
}

// releaseObservation starts shutdown without the request's expired context and
// keeps Instance shutdown waiting until the control client has actually exited.
func (r *tmuxRuntime) releaseObservation(observation *tmux.PaneObservation) {
	if observation == nil {
		return
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	_ = observation.CloseContext(stopped)
	go func() {
		err := observation.Close()
		r.observe(err)
		r.observations.Done()
	}()
}
