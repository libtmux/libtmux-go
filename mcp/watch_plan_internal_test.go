package mcp

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestWatchPlanProfilesMetadataAndPaneOwners(t *testing.T) {
	t.Parallel()

	topology := watchTopology{
		sessions: []watchTopologySession{
			{id: "$1"},
			{id: "$2"},
			{id: "$3"},
		},
		panes: []watchTopologyPane{
			{id: "%7", sessionID: "$1"},
			{id: "%7", sessionID: "$2"}, // one linked pane, two owners
			{id: "%8", sessionID: "$3"},
			{id: "%9", sessionID: "$9"}, // owner missing from sessions
		},
	}
	tests := []struct {
		name      string
		selection watchSelection
		want      []watchProjectionSummary
	}{
		{
			name:      "metadata only",
			selection: watchSelection{metadata: true},
			want: []watchProjectionSummary{{
				sessions: []tmux.SessionID{"$1", "$2", "$3"},
			}},
		},
		{
			name:      "resolved linked pane",
			selection: selectedPanes("%7"),
			want: []watchProjectionSummary{{
				paneOutput: true,
				sessions:   []tmux.SessionID{"$1", "$2"},
			}},
		},
		{
			name: "mixed metadata and resolved pane",
			selection: watchSelection{
				metadata: true,
				panes:    map[string]struct{}{"%8": {}},
			},
			want: []watchProjectionSummary{
				{paneOutput: true, sessions: []tmux.SessionID{"$3"}},
				{sessions: []tmux.SessionID{"$1", "$2"}},
			},
		},
		{
			name:      "unresolved pane keeps structural coverage",
			selection: selectedPanes("%404"),
			want: []watchProjectionSummary{{
				sessions: []tmux.SessionID{"$1", "$2", "$3"},
			}},
		},
		{
			name:      "resolved and unresolved panes split output from structure",
			selection: selectedPanes("%7", "%404"),
			want: []watchProjectionSummary{
				{paneOutput: true, sessions: []tmux.SessionID{"$1", "$2"}},
				{sessions: []tmux.SessionID{"$3"}},
			},
		},
		{
			name:      "pane owner missing from sessions keeps structural coverage",
			selection: selectedPanes("%9"),
			want: []watchProjectionSummary{{
				sessions: []tmux.SessionID{"$1", "$2", "$3"},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := summarizeWatchPlan(projectWatchPlan(test.selection, topology))
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("watch plan = %#v, want %#v", got, test.want)
			}
			if len(got) > 2 {
				t.Fatalf("watch plan has %d projections, want at most 2", len(got))
			}
		})
	}
}

func TestWatchTopologyReadsOnlyNeededProjections(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		includePanes bool
		want         []string
	}{
		{name: "metadata", want: []string{"sessions"}},
		{name: "pane content", includePanes: true, want: []string{"panes", "sessions"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := &orderedWatchTopologySource{}
			if _, err := readWatchTopology(
				context.Background(),
				source,
				test.includePanes,
			); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(source.calls, test.want) {
				t.Fatalf("topology calls = %v, want %v", source.calls, test.want)
			}
		})
	}
}

func TestWatchPlanIgnoresSessionListingOrder(t *testing.T) {
	t.Parallel()

	selection := watchSelection{metadata: true}
	first := projectWatchPlan(selection, watchTopology{sessions: []watchTopologySession{
		{id: "$2"},
		{id: "$1"},
	}})
	second := projectWatchPlan(selection, watchTopology{sessions: []watchTopologySession{
		{id: "$1"},
		{id: "$2"},
	}})
	if !first.equal(second) {
		t.Fatalf("session listing permutation changed watch plan: %#v != %#v", first, second)
	}
}

type orderedWatchTopologySource struct{ calls []string }

func (s *orderedWatchTopologySource) SearchPanes(
	context.Context,
	*tmux.TmuxFilter,
) ([]tmux.Pane, error) {
	s.calls = append(s.calls, "panes")
	return nil, nil
}

func (s *orderedWatchTopologySource) SearchSessions(
	context.Context,
	*tmux.TmuxFilter,
) ([]tmux.Session, error) {
	s.calls = append(s.calls, "sessions")
	return nil, nil
}

type watchProjectionSummary struct {
	paneOutput bool
	sessions   []tmux.SessionID
}

func summarizeWatchPlan(plan watchPlan) []watchProjectionSummary {
	summary := make([]watchProjectionSummary, 0, len(plan.projections))
	for _, projection := range plan.projections {
		ids := make([]tmux.SessionID, 0, len(projection.sessions))
		for _, session := range projection.sessions {
			ids = append(ids, session.id)
		}
		summary = append(summary, watchProjectionSummary{
			paneOutput: projection.options.IncludePaneOutput,
			sessions:   ids,
		})
	}
	return summary
}

func selectedPanes(ids ...string) watchSelection {
	selection := watchSelection{panes: make(map[string]struct{}, len(ids))}
	for _, id := range ids {
		selection.panes[id] = struct{}{}
	}
	return selection
}
