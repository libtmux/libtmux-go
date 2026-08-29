package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestInstanceCloseReleasesOwnedResources(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(AuditEnvironmentVariable, auditPath)
	instance := NewServer(tmux.NewServer(tmux.ServerOptions{
		SocketName: "lifecycle-unused",
	}))

	jobDirectory := filepath.Join(t.TempDir(), "job")
	if err := os.Mkdir(jobDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	instance.tools.jobs.keep(&job{id: "job", directory: jobDirectory})

	auditFile, ok := instance.audit.(*os.File)
	if !ok {
		t.Fatalf("audit owner = %T, want *os.File", instance.audit)
	}

	const uri = "tmux://panes/%9/content"
	instance.tools.watchers.subscribed[uri] = 1
	instance.tools.watchers.spelled[uri] = map[string]int{uri: 1}
	instance.tools.watchers.notify(t.Context(), uri)
	instance.tools.watchers.notify(t.Context(), uri)
	if !instance.tools.watchers.owes(uri) {
		t.Fatal("watcher has no deferred notification to release")
	}

	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := os.Stat(jobDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job directory survived Close(): %v", err)
	}
	if _, err := auditFile.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("audit write after Close() error = %v, want closed", err)
	}
	if instance.tools.watchers.owes(uri) {
		t.Fatal("deferred notification survived Close()")
	}
	time.Sleep(watchNotifyInterval + 50*time.Millisecond)
	if !instance.tools.watchers.at(uri).IsZero() {
		t.Fatal("a deferred notification ran after Close()")
	}
}
