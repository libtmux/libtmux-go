package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestPatternLimitsBoundCompilationAndSearchWork(t *testing.T) {
	if _, err := compileMatcher(strings.Repeat("x", 4_097), true, true); err == nil {
		t.Fatal("search accepted a pattern larger than 4,096 UTF-8 bytes")
	}
	tooMany := make([]string, 33)
	for index := range tooMany {
		tooMany[index] = "x"
	}
	if err := validatePatternInputs(tooMany, nil); err == nil {
		t.Fatal("wait accepted more than 32 combined patterns")
	}
	if err := validatePatternInputs(
		[]string{strings.Repeat("x", 9_000)},
		[]string{strings.Repeat("y", 9_000)},
	); err == nil {
		t.Fatal("wait accepted more than 16,384 combined pattern bytes")
	}

	budget := searchWorkBudget{bytes: 999_999}
	if budget.consumeLine("x") {
		t.Fatal("search matched past its fixed 1,000,000-byte work ceiling")
	}
	if budget.bytes != 999_999 {
		t.Fatalf("rejected line changed work count to %d", budget.bytes)
	}
	for range searchPaneInspectionLimit {
		if !budget.startPane() {
			t.Fatal("search stopped before its fixed pane ceiling")
		}
	}
	if budget.startPane() {
		t.Fatal("search accepted a pane past its fixed pane ceiling")
	}
	budget = searchWorkBudget{lines: searchLineInspectionLimit}
	if budget.consumeLine("bounded") {
		t.Fatal("search accepted a line past its fixed line ceiling")
	}
	if searchWorkTimeout <= 0 || searchWorkTimeout > 5*time.Second {
		t.Fatalf("search work timeout = %s, want a positive ceiling no longer than five seconds", searchWorkTimeout)
	}

	definitions, err := toolManifest()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]toolDefinition, len(definitions))
	for _, definition := range definitions {
		byName[definition.name] = definition
	}
	pattern := byName["search_panes"].inputSchema.Properties["pattern"]
	if pattern.MaxLength == nil || *pattern.MaxLength != patternBytesLimit {
		t.Fatalf("search pattern maxLength = %v, want %d", pattern.MaxLength, patternBytesLimit)
	}
	for _, field := range []string{"patterns", "stop"} {
		property := byName["wait_for_text"].inputSchema.Properties[field]
		if property.MaxItems == nil || *property.MaxItems != patternCountLimit ||
			property.Items == nil || property.Items.MaxLength == nil ||
			*property.Items.MaxLength != patternBytesLimit {
			t.Fatalf("wait %s schema does not publish its count and byte bounds: %#v", field, property)
		}
	}
}
