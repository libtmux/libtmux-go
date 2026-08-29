package mcp

import (
	"cmp"
	"context"
	"slices"

	"github.com/libtmux/libtmux-go/tmux"
)

type watchSelection struct {
	panes    map[string]struct{}
	metadata bool
}

type watchTopologySource interface {
	SearchPanes(context.Context, *tmux.TmuxFilter) ([]tmux.Pane, error)
	SearchSessions(context.Context, *tmux.TmuxFilter) ([]tmux.Session, error)
}

type watchTopology struct {
	sessions []watchTopologySession
	panes    []watchTopologyPane
}

type watchTopologySession struct {
	id      tmux.SessionID
	session tmux.Session
}

type watchTopologyPane struct {
	id        string
	sessionID tmux.SessionID
}

type watchPlan struct {
	projections []watchProjection
}

type watchProjection struct {
	options  tmux.NotificationOptions
	sessions []watchTopologySession
}

func readWatchTopology(
	ctx context.Context,
	source watchTopologySource,
	includePanes bool,
) (watchTopology, error) {
	var panes []tmux.Pane
	if includePanes {
		var err error
		panes, err = source.SearchPanes(ctx, nil)
		if err != nil {
			return watchTopology{}, err
		}
	}
	sessions, err := source.SearchSessions(ctx, nil)
	if err != nil {
		return watchTopology{}, err
	}
	topology := watchTopology{
		panes:    make([]watchTopologyPane, 0, len(panes)),
		sessions: make([]watchTopologySession, 0, len(sessions)),
	}
	for _, pane := range panes {
		topology.panes = append(topology.panes, watchTopologyPane{
			id:        pane.ID().String(),
			sessionID: pane.SessionID(),
		})
	}
	for _, session := range sessions {
		topology.sessions = append(topology.sessions, watchTopologySession{
			id:      session.ID(),
			session: session,
		})
	}
	return topology, nil
}

func projectWatchPlan(selection watchSelection, topology watchTopology) watchPlan {
	if !selection.metadata && len(selection.panes) == 0 {
		return watchPlan{}
	}
	sessions := append([]watchTopologySession(nil), topology.sessions...)
	slices.SortFunc(sessions, func(left, right watchTopologySession) int {
		return cmp.Compare(left.id.String(), right.id.String())
	})
	if len(selection.panes) == 0 {
		return watchPlan{projections: []watchProjection{{sessions: sessions}}}
	}

	knownSessions := make(map[tmux.SessionID]struct{}, len(sessions))
	for _, session := range sessions {
		knownSessions[session.id] = struct{}{}
	}
	owners := make(map[tmux.SessionID]struct{})
	resolved := make(map[string]struct{}, len(selection.panes))
	incomplete := false
	for _, pane := range topology.panes {
		if _, wanted := selection.panes[pane.id]; !wanted {
			continue
		}
		resolved[pane.id] = struct{}{}
		if _, known := knownSessions[pane.sessionID]; known {
			owners[pane.sessionID] = struct{}{}
		} else {
			incomplete = true
		}
	}
	if len(resolved) != len(selection.panes) {
		incomplete = true
	}

	full := make([]watchTopologySession, 0, len(owners))
	structural := make([]watchTopologySession, 0, len(sessions)-len(owners))
	for _, session := range sessions {
		if _, ownsPane := owners[session.id]; ownsPane {
			full = append(full, session)
		} else if selection.metadata || incomplete {
			structural = append(structural, session)
		}
	}
	plan := watchPlan{}
	if len(full) > 0 {
		plan.projections = append(plan.projections, watchProjection{
			options:  tmux.NotificationOptions{IncludePaneOutput: true},
			sessions: full,
		})
	}
	if len(structural) > 0 {
		plan.projections = append(plan.projections, watchProjection{sessions: structural})
	}
	return plan
}

func (plan watchPlan) empty() bool {
	for _, projection := range plan.projections {
		if len(projection.sessions) > 0 {
			return false
		}
	}
	return true
}

func (plan watchPlan) equal(other watchPlan) bool {
	if len(plan.projections) != len(other.projections) {
		return false
	}
	for index, projection := range plan.projections {
		peer := other.projections[index]
		if projection.options != peer.options || len(projection.sessions) != len(peer.sessions) {
			return false
		}
		for sessionIndex, session := range projection.sessions {
			if session.id != peer.sessions[sessionIndex].id {
				return false
			}
		}
	}
	return true
}
