package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/libtmux/libtmux-go/workspace/internal/cli"
)

func TestMain(m *testing.M) { os.Exit(tmuxtest.Main(m)) }
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	code := cli.Run(ctx, args, strings.NewReader(""), &out, &diagnostic)
	return code, out.String(), diagnostic.String()
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFreezeReloadAppendAndEnvironment(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	one := filepath.Join(dir, "one")
	two := filepath.Join(dir, "two")
	if err := os.Mkdir(one, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(two, 0o700); err != nil {
		t.Fatal(err)
	}
	path := write(t, dir, "workspace.yaml", `session_name: native
start_directory: .
environment: {SESSION_VALUE: session}
windows:
- window_name: development
  window_index: 3
  layout: even-horizontal
  environment: {WINDOW_VALUE: window}
  panes:
  - start_directory: one
    shell_command: 'printf "%s:%s:%s" "$SESSION_VALUE" "$WINDOW_VALUE" "$PANE_VALUE" > result'
  - start_directory: two
    environment: {PANE_VALUE: pane}
    shell_command: 'printf "%s:%s:%s" "$SESSION_VALUE" "$WINDOW_VALUE" "$PANE_VALUE" > result'
- window_name: second
  focus: true
  panes: [blank]
`)
	args := []string{"load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json"}
	code, out, diagnostic := run(t, args...)
	if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
		t.Fatalf("load %d %s %s", code, out, diagnostic)
	}
	deadline := time.Now().Add(4 * time.Second)
	for !time.Now().After(deadline) {
		if a, e := os.ReadFile(filepath.Join(one, "result")); e == nil && string(a) == "session:window:" {
			if b, e := os.ReadFile(filepath.Join(two, "result")); e == nil && string(b) == "session::pane" {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	for path, want := range map[string]string{filepath.Join(one, "result"): "session:window:", filepath.Join(two, "result"): "session::pane"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("launch environment/cwd %q: %q %v", want, got, err)
		}
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Windows()) != 2 || len(snapshot.Panes()) != 3 {
		t.Fatalf("topology %d/%d", len(snapshot.Windows()), len(snapshot.Panes()))
	}
	code, out, diagnostic = run(t, args...)
	if code != 0 || !strings.Contains(out, `"reused":true`) {
		t.Fatalf("reuse %d %s %s", code, out, diagnostic)
	}
	code, out, diagnostic = run(t, "freeze", "native", "-S", server.SocketPath(), "--json")
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("freeze %d %s %s", code, out, diagnostic)
	}
	var frozen map[string]any
	if err := json.Unmarshal([]byte(out), &frozen); err != nil {
		t.Fatal(err)
	}
	frozen["session_name"] = "reloaded"
	encoded, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	frozenPath := write(t, dir, "frozen.json", string(encoded))
	code, out, diagnostic = run(t, "load", frozenPath, "-S", server.SocketPath(), "-d", "--json")
	if code != 0 {
		t.Fatalf("reload %d %s %s", code, out, diagnostic)
	}
	pane := snapshot.Panes()[0]
	t.Setenv("TMUX_PANE", pane.ID().String())
	t.Setenv("TMUX", server.SocketPath()+",1,0")
	appendPath := write(t, dir, "append.yaml", "session_name: ignored\nwindows:\n- window_name: appended\n  panes: [blank]\n")
	code, out, diagnostic = run(t, "load", appendPath, "-S", server.SocketPath(), "--append", "--json")
	if code != 0 {
		t.Fatalf("append %d %s %s", code, out, diagnostic)
	}
	snapshot, err = server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions()) != 2 || len(snapshot.Windows()) != 5 {
		t.Fatalf("append damaged topology %d/%d", len(snapshot.Sessions()), len(snapshot.Windows()))
	}
}

func TestBeforeScriptFailureRemovesOnlyOwnedSession(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "unrelated"}})
	dir := t.TempDir()
	path := write(t, dir, "fail.yaml", "session_name: failure\nbefore_script: /bin/false\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-d", "--ndjson")
	if code != 1 || !json.Valid([]byte(diagnostic)) {
		t.Fatalf("failure %d %s %s", code, out, diagnostic)
	}
	terminal := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event["event"] == "failed" || event["event"] == "completed" {
			terminal++
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal count %d", terminal)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions()) != 1 {
		t.Fatalf("unexpected sessions %+v", snapshot.Sessions())
	}
	name, _ := snapshot.Sessions()[0].Name()
	if name != "unrelated" {
		t.Fatalf("lost unrelated session %s", name)
	}
}

func TestReadinessWaitsForPrompt(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{Config: []byte("set -g default-shell /bin/sh\nset -g default-command \"sleep 0.25; printf ready; exec /bin/sh\"\n")})
	dir := t.TempDir()
	path := write(t, dir, "ready.yaml", "session_name: ready\nworkspace_builder_options: {pane_readiness: always}\nwindows:\n- panes: [blank]\n")
	start := time.Now()
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json")
	if code != 0 || diagnostic != "" {
		t.Fatalf("readiness %d %s %s", code, out, diagnostic)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Fatal("load did not wait for the delayed prompt")
	}
}
