package tmux_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestFilterJSONRoundTripIsStrictStableWireForm(t *testing.T) {
	t.Parallel()

	filter := tmux.PaneFilter{
		CommandIn:  []string{"nvim", "vim"},
		Active:     tmux.Ptr(false),
		TitleRegex: "notes",
		AnyOf: []tmux.PaneFilter{
			{Command: tmux.Ptr("nvim")},
		},
		Not: &tmux.PaneFilter{Command: tmux.Ptr("bash")},
		Window: &tmux.WindowFilter{
			NameIn: []string{"editor"},
		},
	}

	data, err := json.Marshal(filter)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"commandIn":["nvim","vim"],"active":false,"titleRegex":"notes","anyOf":[{"command":"nvim"}],"not":{"command":"bash"},"window":{"nameIn":["editor"]}}`
	if string(data) != want {
		t.Fatalf("Marshal() = %s, want %s", data, want)
	}

	var roundTrip tmux.PaneFilter
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, filter) {
		t.Fatalf("round trip = %#v, want %#v", roundTrip, filter)
	}
}

func TestPtrReturnsDistinctShallowCopies(t *testing.T) {
	t.Parallel()

	falseValue := tmux.Ptr(false)
	zeroValue := tmux.Ptr(0)
	emptyValue := tmux.Ptr("")
	paneID := tmux.Ptr(tmux.PaneID("%7"))
	if *falseValue || *zeroValue != 0 || *emptyValue != "" || *paneID != "%7" {
		t.Fatalf("Ptr() values = (%v, %v, %q, %q)", *falseValue, *zeroValue, *emptyValue, *paneID)
	}
	if first, second := tmux.Ptr(1), tmux.Ptr(1); first == second {
		t.Fatal("Ptr() reused an address")
	}
	zeroSize := []*struct{}{tmux.Ptr(struct{}{}), tmux.Ptr(struct{}{})}
	if zeroSize[0] == zeroSize[1] {
		t.Fatal("Ptr() reused a zero-size address")
	}

	values := []int{1}
	shallowCopy := tmux.Ptr(values)
	(*shallowCopy)[0] = 2
	if values[0] != 2 {
		t.Fatalf("Ptr() deep-copied referenced data: %v", values)
	}
}

func TestExactFilterConstructorsSetOneCriterionWithoutValidation(t *testing.T) {
	t.Parallel()

	command := tmux.PaneCommandIs("nvim")
	if !reflect.DeepEqual(command, tmux.PaneFilter{Command: tmux.Ptr("nvim")}) {
		t.Fatalf("PaneCommandIs() = %#v", command)
	}
	index := tmux.WindowIndexIs(0)
	if !reflect.DeepEqual(index, tmux.WindowFilter{Index: tmux.Ptr(0)}) {
		t.Fatalf("WindowIndexIs() = %#v", index)
	}
	invalid := tmux.PaneIDIs("")
	if invalid.ID == nil || *invalid.ID != "" {
		t.Fatalf("PaneIDIs() = %#v", invalid)
	}
	if err := invalid.Validate(); !errors.Is(err, tmux.ErrInvalidFilter) {
		t.Fatalf("PaneIDIs(\"\").Validate() error = %v, want ErrInvalidFilter", err)
	}
}

func TestFilterSchemaVersionIsPublishedOutsideTheWireObject(t *testing.T) {
	t.Parallel()

	if tmux.FilterSchemaVersion != 1 {
		t.Fatalf("FilterSchemaVersion = %d, want 1", tmux.FilterSchemaVersion)
	}
	data, err := json.Marshal(tmux.PaneFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{}` {
		t.Fatalf("empty filter JSON = %s, want no embedded schema version", data)
	}
}

func TestFilterJSONRejectsUnknownDuplicateAndTrailingData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		data         string
		wantSentinel bool
	}{
		{name: "unknown top level", data: `{"future":true}`, wantSentinel: true},
		{name: "duplicate top level", data: `{"command":"nvim","command":"bash"}`, wantSentinel: true},
		{name: "unknown nested", data: `{"window":{"future":true}}`, wantSentinel: true},
		{name: "duplicate nested", data: `{"window":{"name":"one","name":"two"}}`, wantSentinel: true},
		{name: "trailing data", data: `{} {}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var filter tmux.PaneFilter
			err := json.Unmarshal([]byte(test.data), &filter)
			if err == nil {
				t.Fatal("Unmarshal() error = nil, want strict rejection")
			}
			if test.wantSentinel && !errors.Is(err, tmux.ErrInvalidFilter) {
				t.Fatalf("Unmarshal() error = %v, want ErrInvalidFilter", err)
			}
		})
	}
}

func TestFilterUnmarshalJSONDecodeErrorClearsAndLeavesPartialReceiver(t *testing.T) {
	t.Parallel()

	filter := tmux.PaneFilter{
		Command: tmux.Ptr("before"),
		Active:  tmux.Ptr(true),
	}
	err := filter.UnmarshalJSON([]byte(`{"title":"decoded","future":true}`))
	if !errors.Is(err, tmux.ErrInvalidFilter) {
		t.Fatalf("Unmarshal() error = %v, want ErrInvalidFilter", err)
	}
	if filter.Command != nil || filter.Active != nil {
		t.Fatalf("Unmarshal() retained receiver state: %#v", filter)
	}
	if filter.Title == nil || *filter.Title != "decoded" {
		t.Fatalf("Unmarshal() partial receiver = %#v, want decoded title", filter)
	}
}

func TestFilterUnmarshalJSONWrapsDecodeAndFramingFailures(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"wrong JSON type":     `[]`,
		"unterminated object": `{"title":"notes"`,
		"trailing value":      `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			var filter tmux.PaneFilter
			if err := filter.UnmarshalJSON([]byte(data)); !errors.Is(err, tmux.ErrInvalidFilter) {
				t.Fatalf("UnmarshalJSON() error = %v, want ErrInvalidFilter", err)
			}
		})
	}
}

func TestFilterUnmarshalJSONValidatesDecodedCriteria(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		decode func() error
	}{
		{
			name: "session",
			decode: func() error {
				var filter tmux.SessionFilter
				return json.Unmarshal([]byte(`{"nameIn":[]}`), &filter)
			},
		},
		{
			name: "window",
			decode: func() error {
				var filter tmux.WindowFilter
				return json.Unmarshal([]byte(`{"indexIn":[]}`), &filter)
			},
		},
		{
			name: "pane",
			decode: func() error {
				var filter tmux.PaneFilter
				return json.Unmarshal([]byte(`{"commandRegex":"["}`), &filter)
			},
		},
		{
			name: "client",
			decode: func() error {
				var filter tmux.ClientFilter
				return json.Unmarshal([]byte(`{"nameIn":[]}`), &filter)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.decode(); !errors.Is(err, tmux.ErrInvalidFilter) {
				t.Fatalf("Unmarshal() error = %v, want ErrInvalidFilter", err)
			}
		})
	}
}

func TestRelationUnmarshalJSONValidatesDecodedCriteria(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		decode func() error
	}{
		{
			name: "empty window relation",
			decode: func() error {
				var relation tmux.WindowRel
				return json.Unmarshal([]byte(`{}`), &relation)
			},
		},
		{
			name: "invalid window filter",
			decode: func() error {
				var relation tmux.WindowRel
				return json.Unmarshal([]byte(`{"some":{"indexIn":[]}}`), &relation)
			},
		},
		{
			name: "empty pane relation",
			decode: func() error {
				var relation tmux.PaneRel
				return json.Unmarshal([]byte(`{}`), &relation)
			},
		},
		{
			name: "invalid pane filter",
			decode: func() error {
				var relation tmux.PaneRel
				return json.Unmarshal([]byte(`{"some":{"idIn":[]}}`), &relation)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.decode(); !errors.Is(err, tmux.ErrInvalidFilter) {
				t.Fatalf("Unmarshal() error = %v, want ErrInvalidFilter", err)
			}
		})
	}
}

func TestFilterJSONPreservesTriStateAndImpossibleEmptyLists(t *testing.T) {
	t.Parallel()

	var unset tmux.PaneFilter
	if err := json.Unmarshal([]byte(`{"active":null,"not":null,"window":null}`), &unset); err != nil {
		t.Fatal(err)
	}
	if unset.Active != nil || unset.Not != nil || unset.Window != nil {
		t.Fatalf("null pointers decoded as set: %#v", unset)
	}

	var explicitFalse tmux.PaneFilter
	if err := json.Unmarshal([]byte(`{"active":false}`), &explicitFalse); err != nil {
		t.Fatal(err)
	}
	if explicitFalse.Active == nil || *explicitFalse.Active {
		t.Fatalf("explicit false lost during decode: %#v", explicitFalse)
	}

	var empty tmux.PaneFilter
	if err := json.Unmarshal([]byte(`{"commandIn":[],"anyOf":[]}`), &empty); !errors.Is(err, tmux.ErrInvalidFilter) {
		t.Fatalf("Unmarshal() error = %v, want ErrInvalidFilter", err)
	}
	if empty.CommandIn == nil || empty.AnyOf == nil {
		t.Fatalf("explicit empty slices decoded as nil: %#v", empty)
	}
	for name, filter := range map[string]tmux.PaneFilter{
		"empty membership":  {CommandIn: []string{}},
		"empty composition": {AnyOf: []tmux.PaneFilter{}},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(filter)
			if !errors.Is(err, tmux.ErrInvalidFilter) {
				t.Fatalf("Marshal() = %s, %v, want ErrInvalidFilter", data, err)
			}
		})
	}
	if data, err := json.Marshal(tmux.WindowRel{}); !errors.Is(err, tmux.ErrInvalidFilter) {
		t.Fatalf("Marshal(empty relation) = %s, %v, want ErrInvalidFilter", data, err)
	}
}

func TestLegacyLookupParserReturnsConcreteGeneratedFilters(t *testing.T) {
	t.Parallel()

	session, sessionErr := tmux.ParseSessionLookup("name__contains", "work")
	window, windowErr := tmux.ParseWindowLookup("name__contains", "editor")
	pane, paneErr := tmux.ParsePaneLookup("command__contains", "vim")
	client, clientErr := tmux.ParseClientLookup("name__contains", "pts")
	for name, err := range map[string]error{
		"session": sessionErr,
		"window":  windowErr,
		"pane":    paneErr,
		"client":  clientErr,
	} {
		if err != nil {
			t.Fatalf("Parse%sLookup() error = %v", name, err)
		}
	}
	if session.NameContains == nil || window.NameContains == nil ||
		pane.CommandContains == nil || client.NameContains == nil {
		t.Fatal("lookup parser did not return concrete generated criteria")
	}
}
