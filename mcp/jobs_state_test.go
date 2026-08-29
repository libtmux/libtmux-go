package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConcurrentJobCollectorsUseSettledResult(t *testing.T) {
	directory := t.TempDir()
	started := time.Unix(100, 0)
	owned := newJobs()
	if err := owned.keep(&job{
		id: "job", directory: directory, started: started,
	}); err != nil {
		t.Fatalf("keep() error = %v", err)
	}
	leader, elected, done, found := owned.beginCollection("job")
	if !found || !elected || leader.finished || done == nil {
		t.Fatalf("first beginCollection() = (%#v, %t, %p, %t)", leader, elected, done, found)
	}
	follower, elected, sameDone, found := owned.beginCollection("job")
	if !found || elected || follower.finished || sameDone != done {
		t.Fatalf("second beginCollection() = (%#v, %t, %p, %t)", follower, elected, sameDone, found)
	}

	ended := started.Add(5 * time.Second)
	settled, ok := owned.settle(
		"job", 7, []string{"canonical"}, truncation{TruncatedLines: 2},
		"capture unavailable", true, ended,
	)
	if !ok || !settled.finished {
		t.Fatalf("settle() = (%#v, %t), want a finished job", settled, ok)
	}
	select {
	case <-done:
	default:
		t.Fatal("settle() did not wake collection followers")
	}
	owned.endCollection("job", done)
	canonical, ok := owned.find("job")
	if !ok || !canonical.finished || canonical.exitStatus != 7 ||
		canonical.ended != ended || len(canonical.output) != 1 ||
		canonical.output[0] != "canonical" || canonical.directory != "" ||
		canonical.outputUnavailable != "capture unavailable" || !canonical.linesMissed {
		t.Errorf("find() = %#v, want the canonical settled result", canonical)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settled directory still exists: %v", err)
	}
}

func TestClosingJobsWakesCollectionFollowers(t *testing.T) {
	owned := newJobs()
	if err := owned.keep(&job{id: "job", directory: t.TempDir()}); err != nil {
		t.Fatalf("keep() error = %v", err)
	}
	_, elected, done, found := owned.beginCollection("job")
	if !found || !elected {
		t.Fatal("beginCollection() did not elect a collector")
	}
	owned.close()
	select {
	case <-done:
	default:
		t.Fatal("close() did not wake collection followers")
	}
}

func TestJobEvictionPreservesAnActiveCollector(t *testing.T) {
	base := t.TempDir()
	owned := newJobs()
	directories := make([]string, jobsRetained+1)
	for index := range jobsRetained {
		directory := filepath.Join(base, fmt.Sprintf("job-%02d", index))
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		directories[index] = directory
		if err := owned.keep(&job{
			id: fmt.Sprintf("job-%02d", index), directory: directory,
		}); err != nil {
			t.Fatalf("keep job %d: %v", index, err)
		}
	}
	_, elected, done, found := owned.beginCollection("job-00")
	if !found || !elected {
		t.Fatal("oldest job did not elect a collector")
	}

	directories[jobsRetained] = filepath.Join(base, "new")
	if err := os.Mkdir(directories[jobsRetained], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := owned.keep(&job{
		id: "new", directory: directories[jobsRetained],
	}); err != nil {
		t.Fatalf("keep new job: %v", err)
	}
	if _, retained := owned.find("job-00"); !retained {
		t.Fatal("eviction removed the active collector")
	}
	if _, retained := owned.find("job-01"); retained {
		t.Fatal("eviction retained the oldest idle job")
	}
	if _, err := os.Stat(directories[0]); err != nil {
		t.Fatalf("collector directory was removed: %v", err)
	}
	if _, err := os.Stat(directories[1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evicted job directory still exists: %v", err)
	}
	owned.endCollection("job-00", done)
}

func TestJobCapacityRefusesToEvictCollectors(t *testing.T) {
	base := t.TempDir()
	owned := newJobs()
	for index := range jobsRetained {
		id := fmt.Sprintf("job-%02d", index)
		directory := filepath.Join(base, id)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := owned.keep(&job{id: id, directory: directory}); err != nil {
			t.Fatalf("keep job %d: %v", index, err)
		}
		if _, elected, _, found := owned.beginCollection(id); !found || !elected {
			t.Fatalf("job %d did not elect a collector", index)
		}
	}
	refused := filepath.Join(base, "refused")
	if err := os.Mkdir(refused, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := owned.keep(&job{id: "refused", directory: refused}); !errors.Is(err, errJobCapacity) {
		t.Fatalf("keep beyond collector capacity = %v, want %v", err, errJobCapacity)
	}
	if _, retained := owned.find("refused"); retained {
		t.Fatal("a refused job was retained")
	}
	if _, err := os.Stat(refused); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused job directory still exists: %v", err)
	}
	for index := range jobsRetained {
		if _, retained := owned.find(fmt.Sprintf("job-%02d", index)); !retained {
			t.Fatalf("collector %d was evicted", index)
		}
	}
	owned.close()
}
