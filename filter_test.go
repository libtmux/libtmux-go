package tmux

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tmux-python/libtmux/golang/tmuxq"
)

type filterGraph struct {
	sessions []Session
	windows  []Window
	panes    []Pane
	client   Client
}

// libtmux:parity libtmux.server.Server.attached_sessions
// libtmux:parity libtmux._internal.query_list.QueryList.filter
// libtmux:parity libtmux._internal.query_list.QueryList.filter#parameter-branch:matcher:e534581b46ea
// libtmux:parity libtmux._internal.query_list.QueryList.filter#parameter-branch:matcher:f2118d1b65fa
// libtmux:parity libtmux._internal.query_list.keygetter
// libtmux:parity libtmux._internal.query_list.keygetter#parameter-branch:obj:18e64e25404d
// libtmux:parity libtmux._internal.query_list.keygetter#parameter-branch:obj:87ee45124d16
func TestGeneratedPredicateIntegratesWithTmuxq(t *testing.T) {
	t.Parallel()

	panes := []Pane{
		{paneID: "%1", paneIndex: 0},
		{paneID: "%2", paneIndex: 1},
		{paneID: "%3", paneIndex: 2},
	}
	predicate, err := (PaneFilter{
		IDIn:    []PaneID{"%1", "%3"},
		IndexGT: Ptr(0),
	}).Predicate()
	if err != nil {
		t.Fatal(err)
	}

	got := tmuxq.Where(panes, predicate)
	if want := []Pane{{paneID: "%3", paneIndex: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Where() = %#v, want %#v", got, want)
	}
}

// libtmux:parity libtmux.server.Server.attached_sessions
// libtmux:parity libtmux._internal.query_list.QueryList.items
// libtmux:parity libtmux._internal.query_list.QueryList.pk_key
func TestGeneratedFiltersCoverCuratedModelFields(t *testing.T) {
	t.Parallel()

	graph := newFilterGraph()
	assertSessionFilter(t, SessionFilter{ID: Ptr(SessionID("$1"))}, graph.sessions[0], true)
	assertSessionFilter(t, SessionFilter{Name: Ptr("work")}, graph.sessions[0], true)
	assertSessionFilter(t, SessionFilter{Attached: Ptr(true)}, graph.sessions[0], true)

	assertWindowFilter(t, WindowFilter{SessionID: Ptr(SessionID("$1"))}, graph.windows[0], true)
	assertWindowFilter(t, WindowFilter{ID: Ptr(WindowID("@1"))}, graph.windows[0], true)
	assertWindowFilter(t, WindowFilter{Index: Ptr(1)}, graph.windows[0], true)
	assertWindowFilter(t, WindowFilter{Name: Ptr("alpha")}, graph.windows[0], true)
	assertWindowFilter(t, WindowFilter{Active: Ptr(true)}, graph.windows[0], true)

	assertPaneFilter(t, PaneFilter{SessionID: Ptr(SessionID("$1"))}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{WindowID: Ptr(WindowID("@1"))}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{ID: Ptr(PaneID("%1"))}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{WindowIndex: Ptr(1)}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{Index: Ptr(0)}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{Command: Ptr("nvim")}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{Title: Ptr("notes.md")}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{Active: Ptr(true)}, graph.panes[0], true)

	assertClientFilter(t, ClientFilter{Name: Ptr(ClientName("/dev/pts/7"))}, graph.client, true)
	assertClientFilter(t, ClientFilter{ReadOnly: Ptr(true)}, graph.client, true)
}

func TestGeneratedScalarOperatorsComposeWithAnd(t *testing.T) {
	t.Parallel()

	graph := newFilterGraph()
	paneFilter := PaneFilter{
		CommandIn:       []string{"nvim", "vim"},
		CommandContains: Ptr("vi"),
		CommandRegex:    `^n?vim$`,
		TitleContains:   Ptr("notes"),
		TitleRegex:      `\.md$`,
		Active:          Ptr(true),
	}
	assertPaneFilter(t, paneFilter, graph.panes[0], true)
	assertPaneFilter(t, paneFilter, graph.panes[1], false)

	windowFilter := WindowFilter{
		IndexIn:  []int{1, 2},
		IndexGT:  Ptr(0),
		IndexGTE: Ptr(1),
		IndexLT:  Ptr(2),
		IndexLTE: Ptr(1),
		NameIn:   []string{"alpha", "gamma"},
	}
	assertWindowFilter(t, windowFilter, graph.windows[0], true)
	assertWindowFilter(t, windowFilter, graph.windows[1], false)

	assertSessionFilter(t, SessionFilter{IDIn: []SessionID{"$1"}}, graph.sessions[0], true)
	assertWindowFilter(t, WindowFilter{IDIn: []WindowID{"@1"}}, graph.windows[0], true)
	assertPaneFilter(t, PaneFilter{IDIn: []PaneID{"%1"}}, graph.panes[0], true)
	assertClientFilter(t, ClientFilter{NameRegex: `^/dev/pts/`}, graph.client, true)
}

// libtmux:parity libtmux._internal.query_list.QueryList.filter
// libtmux:parity libtmux._internal.query_list.QueryList.filter#parameter-branch:matcher:e534581b46ea
// libtmux:parity libtmux._internal.query_list.QueryList.filter#parameter-branch:matcher:f2118d1b65fa
// libtmux:parity libtmux._internal.query_list.keygetter
// libtmux:parity libtmux._internal.query_list.keygetter#parameter-branch:obj:18e64e25404d
// libtmux:parity libtmux._internal.query_list.keygetter#parameter-branch:obj:87ee45124d16
func TestGeneratedFilterCompositionAndDifferentialReference(t *testing.T) {
	t.Parallel()

	graph := newFilterGraph()
	filter := PaneFilter{
		CommandIn: []string{"nvim", "bash"},
		AnyOf: []PaneFilter{
			{TitleContains: Ptr("notes")},
			{Title: Ptr("README")},
		},
		Not:    &PaneFilter{Title: Ptr("scratch")},
		Window: &WindowFilter{NameContains: Ptr("a")},
	}
	predicate, err := filter.Predicate()
	if err != nil {
		t.Fatalf("Predicate() error = %v", err)
	}

	for index := range graph.panes {
		pane := graph.panes[index]
		command, commandOK := pane.CurrentCommand()
		title, titleOK := pane.Title()
		window, windowOK := pane.Window()
		windowName, nameOK := window.Name()
		want := commandOK && slices.Contains([]string{"nvim", "bash"}, command) &&
			titleOK && (strings.Contains(title, "notes") || title == "README") &&
			title != "scratch" && windowOK && nameOK && strings.Contains(windowName, "a")
		if got := predicate(&pane); got != want {
			t.Errorf("pane %s predicate = %v, direct reference = %v", pane.paneID, got, want)
		}
	}
}

func TestGeneratedRelationsCoverToOneAndToMany(t *testing.T) {
	t.Parallel()

	graph := newFilterGraph()
	work := graph.sessions[0]
	empty := graph.sessions[1]

	relationCases := []struct {
		name   string
		filter SessionFilter
		value  Session
		want   bool
	}{
		{
			name:   "windows some",
			filter: SessionFilter{Windows: &WindowRel{Some: &WindowFilter{Name: Ptr("beta")}}},
			value:  work,
			want:   true,
		},
		{
			name:   "windows every",
			filter: SessionFilter{Windows: &WindowRel{Every: &WindowFilter{NameRegex: `^(alpha|beta)$`}}},
			value:  work,
			want:   true,
		},
		{
			name:   "windows none",
			filter: SessionFilter{Windows: &WindowRel{None: &WindowFilter{Name: Ptr("gamma")}}},
			value:  work,
			want:   true,
		},
		{
			name:   "empty every is vacuously true",
			filter: SessionFilter{Windows: &WindowRel{Every: &WindowFilter{NameRegex: `.+`}}},
			value:  empty,
			want:   true,
		},
		{
			name:   "empty some is false",
			filter: SessionFilter{Windows: &WindowRel{Some: &WindowFilter{NameRegex: `.+`}}},
			value:  empty,
			want:   false,
		},
		{
			name:   "empty none is true",
			filter: SessionFilter{Windows: &WindowRel{None: &WindowFilter{NameRegex: `.+`}}},
			value:  empty,
			want:   true,
		},
		{
			name:   "session panes",
			filter: SessionFilter{Panes: &PaneRel{Some: &PaneFilter{Command: Ptr("bash")}}},
			value:  work,
			want:   true,
		},
	}
	for _, test := range relationCases {
		t.Run(test.name, func(t *testing.T) {
			assertSessionFilter(t, test.filter, test.value, test.want)
		})
	}

	assertWindowFilter(t, WindowFilter{Session: &SessionFilter{Name: Ptr("work")}}, graph.windows[0], true)
	assertWindowFilter(t, WindowFilter{Panes: &PaneRel{Some: &PaneFilter{ID: Ptr(PaneID("%1"))}}}, graph.windows[0], true)
	assertPaneFilter(t, PaneFilter{Session: &SessionFilter{ID: Ptr(SessionID("$1"))}}, graph.panes[0], true)
	assertPaneFilter(t, PaneFilter{Window: &WindowFilter{Name: Ptr("alpha")}}, graph.panes[0], true)
	assertClientFilter(t, ClientFilter{Session: &SessionFilter{Name: Ptr("work")}}, graph.client, true)
	assertClientFilter(t, ClientFilter{Window: &WindowFilter{Name: Ptr("beta")}}, graph.client, true)
	assertClientFilter(t, ClientFilter{Pane: &PaneFilter{ID: Ptr(PaneID("%2"))}}, graph.client, true)
}

func TestGeneratedValidationRejectsInvalidFiltersBeforeCompilation(t *testing.T) {
	t.Parallel()
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1

	selfCycle := PaneFilter{}
	selfCycle.Not = &selfCycle

	crossCycle := SessionFilter{}
	window := WindowFilter{Session: &crossCycle}
	crossCycle.Windows = &WindowRel{Some: &window}

	deep := PaneFilter{}
	cursor := &deep
	for range 100 {
		cursor.Not = &PaneFilter{}
		cursor = cursor.Not
	}

	cycleBeforeRegex := PaneFilter{TitleRegex: "["}
	cycleBeforeRegex.Not = &cycleBeforeRegex

	tests := []struct {
		name   string
		filter interface {
			Validate() error
		}
		contains string
	}{
		{name: "invalid regex", filter: PaneFilter{TitleRegex: "["}, contains: "regex"},
		{name: "empty in", filter: PaneFilter{CommandIn: []string{}}, contains: "must not be empty"},
		{name: "empty anyOf", filter: PaneFilter{AnyOf: []PaneFilter{}}, contains: "must not be empty"},
		{name: "empty relation", filter: SessionFilter{Windows: &WindowRel{}}, contains: "some, every, or none"},
		{name: "self cycle", filter: selfCycle, contains: "cycle"},
		{name: "cross relation cycle", filter: crossCycle, contains: "cycle"},
		{name: "depth", filter: deep, contains: "depth"},
		{name: "cycle precedes regex", filter: cycleBeforeRegex, contains: "cycle"},
		{
			name: "exact outside membership",
			filter: PaneFilter{
				Command:   Ptr("nvim"),
				CommandIn: []string{"bash"},
			},
			contains: "command",
		},
		{
			name: "exact violates contains",
			filter: PaneFilter{
				Command:         Ptr("nvim"),
				CommandContains: Ptr("bash"),
			},
			contains: "command",
		},
		{
			name: "exact violates regex",
			filter: PaneFilter{
				Command:      Ptr("nvim"),
				CommandRegex: `^bash$`,
			},
			contains: "command",
		},
		{
			name: "membership excluded by string criteria",
			filter: PaneFilter{
				CommandIn:       []string{"nvim", "vim"},
				CommandContains: Ptr("bash"),
			},
			contains: "command",
		},
		{
			name: "contradictory integer bounds",
			filter: PaneFilter{
				IndexGT:  Ptr(5),
				IndexLTE: Ptr(5),
			},
			contains: "index",
		},
		{
			name: "adjacent exclusive integer bounds",
			filter: PaneFilter{
				IndexGT: Ptr(5),
				IndexLT: Ptr(6),
			},
			contains: "index",
		},
		{
			name: "exact violates integer bounds",
			filter: PaneFilter{
				Index:    Ptr(5),
				IndexGTE: Ptr(6),
			},
			contains: "index",
		},
		{
			name: "membership excluded by integer bounds",
			filter: PaneFilter{
				IndexIn: []int{1, 2},
				IndexGT: Ptr(2),
			},
			contains: "index",
		},
		{
			name:     "negative exact index",
			filter:   PaneFilter{Index: Ptr(-1)},
			contains: "index",
		},
		{
			name:     "strict bound above maximum integer",
			filter:   PaneFilter{IndexGT: &maxInt},
			contains: "index",
		},
		{
			name:     "strict bound below minimum integer",
			filter:   PaneFilter{IndexLT: &minInt},
			contains: "index",
		},
		{
			name:     "negative membership index",
			filter:   PaneFilter{IndexIn: []int{0, -1}},
			contains: "index",
		},
		{
			name:     "malformed stable identifier",
			filter:   PaneFilter{ID: Ptr(PaneID("pane-1"))},
			contains: "id",
		},
		{
			name: "client identity violates contains",
			filter: ClientFilter{
				Name:         Ptr(ClientName("/dev/pts/7")),
				NameContains: Ptr("private"),
			},
			contains: "name",
		},
		{
			name:     "negated match-all filter",
			filter:   PaneFilter{Not: &PaneFilter{}},
			contains: "negate every",
		},
		{
			name: "positive criterion negated identically",
			filter: PaneFilter{
				Command: Ptr("nvim"),
				Not:     &PaneFilter{Command: Ptr("nvim")},
			},
			contains: "negate every",
		},
		{
			name: "relation requires and forbids identical match",
			filter: SessionFilter{Windows: &WindowRel{
				Some: &WindowFilter{Name: Ptr("editor")},
				None: &WindowFilter{Name: Ptr("editor")},
			}},
			contains: "requires and forbids",
		},
		{
			name: "relation requires value but forbids all values",
			filter: SessionFilter{Windows: &WindowRel{
				Some: &WindowFilter{Name: Ptr("editor")},
				None: &WindowFilter{},
			}},
			contains: "requires and forbids",
		},
		{
			name: "nonempty relation requires every forbidden match",
			filter: SessionFilter{Windows: &WindowRel{
				Some:  &WindowFilter{Name: Ptr("editor")},
				Every: &WindowFilter{Active: Ptr(true)},
				None:  &WindowFilter{Active: Ptr(true)},
			}},
			contains: "cannot be nonempty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.filter.Validate()
			if !errors.Is(err, ErrInvalidFilter) || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Validate() error = %v, want ErrInvalidFilter containing %q", err, test.contains)
			}
		})
	}

	if _, err := (PaneFilter{CommandIn: []string{}}).Predicate(); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("Predicate() error = %v, want ErrInvalidFilter", err)
	}

	valid := PaneFilter{
		Index:           Ptr(5),
		IndexIn:         []int{4, 5, 6},
		IndexGT:         Ptr(4),
		IndexLTE:        Ptr(5),
		Command:         Ptr("nvim"),
		CommandIn:       []string{"nvim", "bash"},
		CommandContains: Ptr("vim"),
		CommandRegex:    `^nvim$`,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate(valid intersection) error = %v", err)
	}
	validRelations := SessionFilter{Windows: &WindowRel{
		Some: &WindowFilter{Name: Ptr("editor")},
		None: &WindowFilter{Name: Ptr("scratch")},
	}}
	if err := validRelations.Validate(); err != nil {
		t.Fatalf("Validate(valid relation intersection) error = %v", err)
	}
}

// libtmux:parity libtmux._internal.query_list.parse_lookup
// libtmux:parity libtmux._internal.query_list.parse_lookup#parameter-branch:lookup,path:5623e853fec0
// libtmux:parity libtmux._internal.query_list.parse_lookup#parameter-branch:lookup,path:a9600799e8b8
func TestLegacyLookupsLowerIntoGeneratedCriteria(t *testing.T) {
	t.Parallel()

	session, err := ParseSessionLookup("name__iexact", "WORK")
	if err != nil {
		t.Fatal(err)
	}
	if session.NameRegex != `(?i)^WORK$` {
		t.Fatalf("ParseSessionLookup() regex = %q", session.NameRegex)
	}

	window, err := ParseWindowLookup("index__in", "1", "3")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(window.IndexIn, []int{1, 3}) {
		t.Fatalf("ParseWindowLookup() index membership = %#v", window.IndexIn)
	}

	pane, err := ParsePaneLookup("window__name__istartswith", "a+b")
	if err != nil {
		t.Fatal(err)
	}
	if pane.Window == nil || pane.Window.NameRegex != `(?i)^a\+b` {
		t.Fatalf("ParsePaneLookup() relation = %#v", pane.Window)
	}

	session, err = ParseSessionLookup("windows__name__contains", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if session.Windows == nil || session.Windows.Some == nil ||
		session.Windows.Some.NameContains == nil ||
		*session.Windows.Some.NameContains != "alpha" {
		t.Fatalf("ParseSessionLookup() to-many relation = %#v", session.Windows)
	}
	session, err = ParseSessionLookup("windows__name__nin", "scratch")
	if err != nil {
		t.Fatal(err)
	}
	if session.Windows == nil || session.Windows.None == nil ||
		!slices.Equal(session.Windows.None.NameIn, []string{"scratch"}) {
		t.Fatalf("ParseSessionLookup() negative to-many relation = %#v", session.Windows)
	}
	pane, err = ParsePaneLookup("window__name__nin", "scratch")
	if err != nil {
		t.Fatal(err)
	}
	if pane.Window == nil || pane.Window.Not == nil ||
		!slices.Equal(pane.Window.Not.NameIn, []string{"scratch"}) {
		t.Fatalf("ParsePaneLookup() negative to-one relation = %#v", pane.Window)
	}

	pane, err = ParsePaneLookup("command__nin", "bash", "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if pane.Not == nil || !slices.Equal(pane.Not.CommandIn, []string{"bash", "zsh"}) {
		t.Fatalf("ParsePaneLookup() negation = %#v", pane.Not)
	}

	client, err := ParseClientLookup("readOnly", "true")
	if err != nil {
		t.Fatal(err)
	}
	if client.ReadOnly == nil || !*client.ReadOnly {
		t.Fatalf("ParseClientLookup() bool = %#v", client.ReadOnly)
	}

	predicateFilter, err := ParsePaneLookup("command__icontains", "VIM")
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := predicateFilter.Predicate()
	if err != nil {
		t.Fatal(err)
	}
	graph := newFilterGraph()
	if !predicate(&graph.panes[0]) || predicate(&graph.panes[1]) {
		t.Fatal("case-insensitive lookup did not lower to equivalent criteria")
	}
}

// libtmux:parity libtmux._internal.query_list.lookup_contains
// libtmux:parity libtmux._internal.query_list.lookup_contains#parameter-branch:data,rhs:37dfa954f91c
// libtmux:parity libtmux._internal.query_list.lookup_endswith
// libtmux:parity libtmux._internal.query_list.lookup_endswith#parameter-branch:data,rhs:6e73fdda4e85
// libtmux:parity libtmux._internal.query_list.lookup_exact
// libtmux:parity libtmux._internal.query_list.lookup_icontains
// libtmux:parity libtmux._internal.query_list.lookup_icontains#parameter-branch:data,rhs:37dfa954f91c
// libtmux:parity libtmux._internal.query_list.lookup_icontains#parameter-branch:data:3c3be71141f9
// libtmux:parity libtmux._internal.query_list.lookup_icontains#parameter-branch:data:58e0683d3d5e
// libtmux:parity libtmux._internal.query_list.lookup_iendswith
// libtmux:parity libtmux._internal.query_list.lookup_iendswith#parameter-branch:data,rhs:6e73fdda4e85
// libtmux:parity libtmux._internal.query_list.lookup_iexact
// libtmux:parity libtmux._internal.query_list.lookup_iexact#parameter-branch:data,rhs:6e73fdda4e85
// libtmux:parity libtmux._internal.query_list.lookup_in
// libtmux:parity libtmux._internal.query_list.lookup_in#parameter-branch:data,rhs:3b71c1caafc3
// libtmux:parity libtmux._internal.query_list.lookup_in#parameter-branch:data,rhs:3b71c1caafc3:2
// libtmux:parity libtmux._internal.query_list.lookup_in#parameter-branch:data,rhs:4237c192c7b9
// libtmux:parity libtmux._internal.query_list.lookup_in#parameter-branch:rhs:8e63455d8bb7
// libtmux:parity libtmux._internal.query_list.lookup_iregex
// libtmux:parity libtmux._internal.query_list.lookup_iregex#parameter-branch:data,rhs:19f42a7a9f0f
// libtmux:parity libtmux._internal.query_list.lookup_istartswith
// libtmux:parity libtmux._internal.query_list.lookup_istartswith#parameter-branch:data,rhs:6e73fdda4e85
// libtmux:parity libtmux._internal.query_list.lookup_nin
// libtmux:parity libtmux._internal.query_list.lookup_nin#parameter-branch:data,rhs:3b71c1caafc3
// libtmux:parity libtmux._internal.query_list.lookup_nin#parameter-branch:data,rhs:3b71c1caafc3:2
// libtmux:parity libtmux._internal.query_list.lookup_nin#parameter-branch:data,rhs:4237c192c7b9
// libtmux:parity libtmux._internal.query_list.lookup_nin#parameter-branch:rhs:8e63455d8bb7
// libtmux:parity libtmux._internal.query_list.lookup_regex
// libtmux:parity libtmux._internal.query_list.lookup_regex#parameter-branch:data,rhs:19f42a7a9f0f
// libtmux:parity libtmux._internal.query_list.lookup_startswith
// libtmux:parity libtmux._internal.query_list.lookup_startswith#parameter-branch:data,rhs:6e73fdda4e85
func TestLegacyStringLookupOperatorsPreserveTheirPredicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lookup string
		values []string
	}{
		{lookup: "command", values: []string{"nvim"}},
		{lookup: "command__eq", values: []string{"nvim"}},
		{lookup: "command__exact", values: []string{"nvim"}},
		{lookup: "command__iexact", values: []string{"NVIM"}},
		{lookup: "command__contains", values: []string{"vim"}},
		{lookup: "command__icontains", values: []string{"VIM"}},
		{lookup: "command__startswith", values: []string{"nv"}},
		{lookup: "command__istartswith", values: []string{"NV"}},
		{lookup: "command__endswith", values: []string{"vim"}},
		{lookup: "command__iendswith", values: []string{"VIM"}},
		{lookup: "command__in", values: []string{"nvim", "bash"}},
		{lookup: "command__nin", values: []string{"bash", "zsh"}},
		{lookup: "command__regex", values: []string{`^n.*m$`}},
		{lookup: "command__iregex", values: []string{`^NVIM$`}},
	}
	graph := newFilterGraph()
	for _, test := range tests {
		t.Run(test.lookup, func(t *testing.T) {
			filter, err := ParsePaneLookup(test.lookup, test.values...)
			if err != nil {
				t.Fatal(err)
			}
			predicate, err := filter.Predicate()
			if err != nil {
				t.Fatal(err)
			}
			if !predicate(&graph.panes[0]) {
				t.Fatalf("%s did not match nvim", test.lookup)
			}
		})
	}
}

// libtmux:parity libtmux._internal.query_list.OpNotFound
// libtmux:parity libtmux._internal.query_list.OpNotFound.__init__
func TestLegacyLookupsRejectUnsupportedDynamicCriteria(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		lookup string
		values []string
	}{
		{name: "empty", lookup: "", values: []string{"value"}},
		{name: "empty segment", lookup: "window____name", values: []string{"value"}},
		{name: "unknown field", lookup: "future", values: []string{"value"}},
		{name: "unknown operator", lookup: "command__callable", values: []string{"value"}},
		{name: "relation without field", lookup: "window", values: []string{"value"}},
		{name: "missing scalar", lookup: "command"},
		{name: "extra scalar", lookup: "command", values: []string{"one", "two"}},
		{name: "empty membership", lookup: "command__in"},
		{name: "bad integer", lookup: "index", values: []string{"private-value"}},
		{name: "bad boolean", lookup: "active", values: []string{"private-value"}},
		{name: "operator incompatible with integer", lookup: "index__contains", values: []string{"1"}},
		{name: "operator incompatible with boolean", lookup: "active__regex", values: []string{"true"}},
		{name: "malformed stable ID", lookup: "id", values: []string{"pane-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParsePaneLookup(test.lookup, test.values...)
			if !errors.Is(err, ErrInvalidFilter) {
				t.Fatalf("ParsePaneLookup() error = %v, want ErrInvalidFilter", err)
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatalf("ParsePaneLookup() error retained a supplied value: %v", err)
			}
		})
	}
}

func TestCompiledPredicateOwnsFilterStateAndTreatsUnavailableFieldsAsMissing(t *testing.T) {
	t.Parallel()

	command := "nvim"
	filter := PaneFilter{Command: &command}
	predicate, err := filter.Predicate()
	if err != nil {
		t.Fatal(err)
	}
	command = "bash"
	filter.Command = Ptr("other")

	graph := newFilterGraph()
	pane := graph.panes[0]
	if !predicate(&pane) {
		t.Fatal("compiled predicate changed after its source filter was mutated")
	}
	if commandValue, _ := pane.CurrentCommand(); commandValue != "nvim" {
		t.Fatalf("predicate mutated pane command to %q", commandValue)
	}

	missing := Pane{paneID: "%9"}
	emptyCommand, err := (PaneFilter{Command: Ptr("")}).Predicate()
	if err != nil {
		t.Fatal(err)
	}
	if emptyCommand(&missing) {
		t.Fatal("unavailable command matched an explicit empty command")
	}
}

func assertSessionFilter(t *testing.T, filter SessionFilter, value Session, want bool) {
	t.Helper()
	predicate, err := filter.Predicate()
	if err != nil {
		t.Fatalf("Predicate() error = %v", err)
	}
	if got := predicate(&value); got != want {
		t.Fatalf("predicate(%s) = %v, want %v", value.sessionID, got, want)
	}
}

func assertWindowFilter(t *testing.T, filter WindowFilter, value Window, want bool) {
	t.Helper()
	predicate, err := filter.Predicate()
	if err != nil {
		t.Fatalf("Predicate() error = %v", err)
	}
	if got := predicate(&value); got != want {
		t.Fatalf("predicate(%s) = %v, want %v", value.windowID, got, want)
	}
}

func assertPaneFilter(t *testing.T, filter PaneFilter, value Pane, want bool) {
	t.Helper()
	predicate, err := filter.Predicate()
	if err != nil {
		t.Fatalf("Predicate() error = %v", err)
	}
	if got := predicate(&value); got != want {
		t.Fatalf("predicate(%s) = %v, want %v", value.paneID, got, want)
	}
}

func assertClientFilter(t *testing.T, filter ClientFilter, value Client, want bool) {
	t.Helper()
	predicate, err := filter.Predicate()
	if err != nil {
		t.Fatalf("Predicate() error = %v", err)
	}
	if got := predicate(&value); got != want {
		t.Fatalf("predicate(%s) = %v, want %v", value.clientName, got, want)
	}
}

func newFilterGraph() filterGraph {
	state := &snapshotState{
		sessionsByID:     map[SessionID][]int{"$1": {0}, "$2": {1}},
		windowsBySession: map[SessionID][]int{"$1": {0, 1}},
		windowsByWinlink: map[winlinkKey][]int{},
		panesBySession:   map[SessionID][]int{"$1": {0, 1}},
		panesByWinlink:   map[winlinkKey][]int{},
		panesByView:      map[paneViewKey][]int{},
	}
	state.sessions = []Session{
		{
			sessionID: "$1",
			formats: formatValues{values: map[string]string{
				"session_name":     "work",
				"session_attached": "2",
			}},
			snapshot: state,
		},
		{
			sessionID: "$2",
			formats: formatValues{values: map[string]string{
				"session_name":     "empty",
				"session_attached": "0",
			}},
			snapshot: state,
		},
	}
	state.windows = []Window{
		{
			sessionID: "$1", windowID: "@1", windowIndex: 1, snapshot: state,
			formats: formatValues{values: map[string]string{"window_name": "alpha", "window_active": "1"}},
		},
		{
			sessionID: "$1", windowID: "@2", windowIndex: 2, snapshot: state,
			formats: formatValues{values: map[string]string{"window_name": "beta", "window_active": "0"}},
		},
	}
	for index, window := range state.windows {
		key := winlinkKey{sessionID: window.sessionID, windowID: window.windowID, index: window.windowIndex}
		state.windowsByWinlink[key] = []int{index}
	}
	state.panes = []Pane{
		{
			sessionID: "$1", windowID: "@1", windowIndex: 1, paneID: "%1", paneIndex: 0, snapshot: state,
			formats: formatValues{values: map[string]string{
				"pane_current_command": "nvim", "pane_title": "notes.md", "pane_active": "1",
			}},
		},
		{
			sessionID: "$1", windowID: "@2", windowIndex: 2, paneID: "%2", paneIndex: 1, snapshot: state,
			formats: formatValues{values: map[string]string{
				"pane_current_command": "bash", "pane_title": "README", "pane_active": "0",
			}},
		},
	}
	for index, pane := range state.panes {
		winlink := winlinkKey{sessionID: pane.sessionID, windowID: pane.windowID, index: pane.windowIndex}
		state.panesByWinlink[winlink] = append(state.panesByWinlink[winlink], index)
		state.panesByView[paneViewKey{winlinkKey: winlink, paneID: pane.paneID}] = []int{index}
	}
	client := Client{
		clientName: "/dev/pts/7",
		formats:    formatValues{values: map[string]string{"client_readonly": "1"}},
		snapshot:   state,
		attachment: clientAttachment{
			sessionID: "$1", windowID: "@2", windowIndex: 2, paneID: "%2",
			hasSession: true, hasWindow: true, hasPane: true,
		},
	}
	state.clients = []Client{client}
	return filterGraph{
		sessions: state.sessions,
		windows:  state.windows,
		panes:    state.panes,
		client:   client,
	}
}
