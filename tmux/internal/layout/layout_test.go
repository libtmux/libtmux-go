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
	// Cells counts leaves in a checksummed tree: a preset name and a malformed
	// tree both report none, while geometry tmux rejects stays countable.
	var fixtures []struct {
		ID, Layout string
		Cells      int
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			cells, valid := Cells(fixture.Layout)
			if valid != (fixture.Cells != 0) || valid && cells != fixture.Cells {
				t.Fatalf("Cells(%q) = %d, %t; want %d leaves", fixture.Layout, cells, valid, fixture.Cells)
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
