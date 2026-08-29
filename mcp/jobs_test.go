package mcp

import (
	"sync"
	"testing"
	"time"
)

func TestJobsDoNotExposeStateWhileSettling(t *testing.T) {
	jobs := newJobs()
	jobs.keep(&job{id: "job", output: []string{"before"}})
	entry, ok := jobs.find("job")
	if !ok {
		t.Fatal("find() did not return the kept job")
	}

	started := make(chan struct{})
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = entry.finished, entry.output
			}
		}
	})
	<-started
	jobs.settle("job", 0, []string{"after"}, truncation{}, time.Now())
	close(stop)
	readers.Wait()
}
