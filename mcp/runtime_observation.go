package mcp

import (
	"context"

	"github.com/libtmux/libtmux-go/tmux"
)

// openObservation registers a dedicated pane stream before Instance shutdown
// can begin waiting for owned resources. wait_for_text and capture_since each
// open one of these per call, over and above the one long-lived command
// connection - every one of them is this process attaching a control client
// of its own, and a listing must leave all of them out (GO2-5/D2), not only
// the command connection.
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
	if r.ownObservationClients == nil {
		r.ownObservationClients = make(map[tmux.ClientName]tmux.SessionID)
	}
	r.ownObservationClients[observation.ClientName()] = observation.SessionID()
	r.mutex.Unlock()
	return observation, nil
}

// releaseObservation starts shutdown without the request's expired context and
// keeps Instance shutdown waiting until the control client has actually exited.
func (r *tmuxRuntime) releaseObservation(observation *tmux.PaneObservation) {
	if observation == nil {
		return
	}
	name := observation.ClientName()
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	_ = observation.CloseContext(stopped)
	go func() {
		err := observation.Close()
		r.observe(err)
		r.mutex.Lock()
		delete(r.ownObservationClients, name)
		r.mutex.Unlock()
		r.observations.Done()
	}()
}

// ownObservationSnapshot copies the client names and per-session attachment
// counts of every observation this runtime currently has open.
func (r *tmuxRuntime) ownObservationSnapshot() (names map[tmux.ClientName]struct{}, perSession map[tmux.SessionID]int) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	names = make(map[tmux.ClientName]struct{}, len(r.ownObservationClients))
	perSession = make(map[tmux.SessionID]int, len(r.ownObservationClients))
	for name, sessionID := range r.ownObservationClients {
		names[name] = struct{}{}
		perSession[sessionID]++
	}
	return names, perSession
}
