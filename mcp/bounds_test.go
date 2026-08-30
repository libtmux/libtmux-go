package mcp

import "testing"

func TestBoundsReportsBytesDiscardedAtUTF8Boundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		line    string
		limit   int
		want    string
		dropped int
	}{
		{name: "two-byte rune", line: "é", limit: 1, dropped: 2},
		{name: "three-byte rune", line: "€", limit: 2, dropped: 3},
		{name: "four-byte rune", line: "🙂", limit: 3, dropped: 4},
		{name: "rune before retained ASCII", line: "éx", limit: 2, want: "x", dropped: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			kept, report := (bounds{lines: 1, bytes: test.limit}).apply([]string{test.line})
			if len(kept) != 1 || kept[0] != test.want {
				t.Fatalf("kept = %q, want [%q]", kept, test.want)
			}
			if report.TruncatedBytes != test.dropped {
				t.Fatalf("TruncatedBytes = %d, want %d", report.TruncatedBytes, test.dropped)
			}
		})
	}
}
