package tmux

import (
	"context"
	"strings"
	"testing"
)

// BenchmarkPaneObservationWaitForBatches measures WaitFor on a pane whose every
// write reaches match alone, the worst case for work done per write. total is
// the number of bytes written; the retained text caps at paneTextLimit.
func BenchmarkPaneObservationWaitForBatches(b *testing.B) {
	for _, tc := range []struct {
		name  string
		total int
		chunk int
	}{
		{"64KiB_by_80", 64 << 10, 80},
		{"1MiB_by_80", 1 << 20, 80},
		{"4MiB_by_1KiB", 4 << 20, 1024},
	} {
		b.Run(tc.name, func(b *testing.B) {
			record := []byte("%output %1 " + strings.Repeat("x", tc.chunk))
			writes := tc.total / tc.chunk
			for b.Loop() {
				queue := newControlNotificationQueue(defaultControlNotificationLimit)
				observation := newTestPaneObservation(queue)
				var sequence uint64
				push := func() {
					sequence++
					if err := queue.append(sequence, record); err != nil {
						b.Fatal(err)
					}
				}
				push()
				seen := 0
				_, err := observation.WaitFor(context.Background(), func(string) bool {
					seen++
					if seen >= writes {
						return true
					}
					push()
					return false
				})
				if err != nil {
					b.Fatal(err)
				}
				_ = queue.Close()
			}
		})
	}
}
