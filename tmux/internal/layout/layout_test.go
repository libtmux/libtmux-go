package layout

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"strings"
	"testing"
)

func TestCellsCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/layout-preflight.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct{ ID, Layout string }
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	// Geometry failures and too few leaves remain syntactically valid.
	leaves := map[string]int{
		"leaf-with-id": 1, "leaf-without-id": 1, "two-with-ids": 2,
		"two-without-ids": 2, "nested": 3, "short-root-geometry": 2,
		"too-few-cells": 1, "bad-inner-size": 2, "nested-trim-one": 3,
		"nested-trim-two": 3, "nested-invalid-width": 3,
		"nested-short-parent": 3, "bad-inner-size-trimmed": 2,
	}
	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			cells, valid := Cells(fixture.Layout)
			want := leaves[fixture.ID]
			if valid != (want != 0) || valid && cells != want {
				t.Fatalf("Cells(%q) = %d, %t; want %d leaves", fixture.Layout, cells, valid, want)
			}
		})
	}
}

func serialized(body string) string {
	var checksum uint16
	for _, b := range []byte(body) {
		checksum = bits.RotateLeft16(checksum, -1) + uint16(b)
	}
	return fmt.Sprintf("%04x,%s", checksum, body)
}

func TestCellsBounds(t *testing.T) {
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"uint32 maximum", "4294967295x4294967295,4294967295,4294967295,4294967295", true},
		{"width overflow", "4294967296x1,0,0", false},
		{"height overflow", "1x4294967296,0,0", false},
		{"offset overflow", "1x1,4294967296,0", false},
		{"pane ID overflow", "1x1,0,0,4294967296", false},
		{"negative offset", "1x1,-1,0", false},
		{"missing number", "1x1,,0", false},
		{"empty child", "1x1,0,0[]", false},
		{"empty sibling", "1x1,0,0{1x1,0,0,}", false},
		{"trailing delimiter", "1x1,0,0,0,", false},
		{"depth floor", strings.Repeat("1x1,0,0{", 256) + "1x1,0,0" + strings.Repeat("}", 256), true},
		{"depth ceiling", strings.Repeat("1x1,0,0{", 257) + "1x1,0,0" + strings.Repeat("}", 257), false},
		{"long input", strings.Repeat("0", 8192) + "1x1,0,0", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, valid := Cells(serialized(test.body)); valid != test.valid {
				t.Fatalf("valid=%t, want %t", valid, test.valid)
			}
		})
	}
	if cells, ok := Cells("B25D,80x24,0,0,0"); !ok || cells != 1 {
		t.Fatalf("uppercase checksum: %d, %t", cells, ok)
	}
}

func FuzzCells(f *testing.F) {
	for _, seed := range []string{"", "32d2,80x24,0,0{}", "b25d,80x24,0,0,0", "203f,80x24,0,0{39x24,0,0,40x24,40,0}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		cells, valid := Cells(value)
		if valid && (cells < 1 || cells > len(value)) {
			t.Fatalf("invalid leaf count %d for %q", cells, value)
		}
		if valid {
			corrupt := []byte(value)
			corrupt[0] = '0'
			if value[0] == '0' {
				corrupt[0] = '1'
			}
			if _, accepted := Cells(string(corrupt)); accepted {
				t.Fatal("changed checksum accepted")
			}
		}
	})
}
