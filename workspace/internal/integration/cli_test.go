package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestMalformedBeforeScriptLeavesSessionsUntouched(t *testing.T) {
	for _, appendMode := range []bool{false, true} {
		t.Run(strconv.FormatBool(appendMode), func(t *testing.T) {
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}})
			before, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			valid := write(t, dir, "valid.yaml", "session_name: first\nwindows:\n- panes: [blank]\n")
			invalid := write(t, dir, "invalid.json", `{"session_name":"invalid","before_script":"printf 'unterminated","windows":[{"panes":[null]}]}`)
			args := []string{"load", valid, invalid, "-S", server.SocketPath(), "--ndjson"}
			if appendMode {
				t.Setenv("TMUX", server.SocketPath()+",1,0")
				t.Setenv("TMUX_PANE", before.Panes()[0].ID().String())
				args = append(args, "--append")
			} else {
				args = append(args, "-d")
			}
			code, out, diagnostic := run(t, args...)
			if code != 1 || out != "" || !strings.Contains(diagnostic, "before_script") {
				t.Errorf("expected preflight failure: %d %s %s", code, out, diagnostic)
			}
			after, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Sessions()) != 1 || len(after.Windows()) != 1 || len(after.Panes()) != 1 || after.Sessions()[0].ID() != before.Sessions()[0].ID() || after.Windows()[0].ID() != before.Windows()[0].ID() || after.Panes()[0].ID() != before.Panes()[0].ID() {
				t.Errorf("preflight changed retained topology: sessions=%d windows=%d panes=%d", len(after.Sessions()), len(after.Windows()), len(after.Panes()))
			}
		})
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

func TestBeforeScriptDirectoryAndBorrowedSession(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	cwd := t.TempDir()
	configDir := t.TempDir()
	explicitDir := t.TempDir()
	t.Chdir(cwd)
	script := write(t, configDir, "before.sh", "#!/bin/sh\npwd > \"$1\"\n")
	if err := os.Chmod(script, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, directory, want string }{{"inherited", "", cwd}, {"explicit", explicitDir, explicitDir}} {
		marker := filepath.Join(configDir, test.name+".cwd")
		config := "session_name: " + test.name + "\nbefore_script: './before.sh " + marker + "'\nwindows:\n- panes: [blank]\n"
		if test.directory != "" {
			config += "start_directory: " + test.directory + "\n"
		}
		path := write(t, configDir, test.name+".yaml", config)
		code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json")
		if code != 0 {
			t.Fatalf("script cwd %d %s %s", code, out, diagnostic)
		}
		got, err := os.ReadFile(marker)
		if err != nil || strings.TrimSpace(string(got)) != test.want {
			t.Fatalf("script cwd got %q want %q: %v", got, test.want, err)
		}
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pane := snapshot.Panes()[0]
	t.Setenv("TMUX", server.SocketPath()+",1,0")
	t.Setenv("TMUX_PANE", pane.ID().String())
	path := write(t, configDir, "fail-append.yaml", "session_name: unused\nbefore_script: /bin/false\nwindows:\n- panes: [blank]\n")
	code, _, _ := run(t, "load", path, "-S", server.SocketPath(), "--append", "--json")
	if code != 1 {
		t.Fatalf("expected script failure, got %d", code)
	}
	fresh, err := server.Snapshot(t.Context())
	if err != nil || len(fresh.Sessions()) != len(snapshot.Sessions()) {
		t.Fatalf("borrowed session lost: %v", err)
	}
}

func TestInitialWindowUsesConfiguredBaseIndex(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	path := write(t, dir, "base.yaml", "session_name: base\noptions: {base-index: 4}\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json")
	if code != 0 {
		t.Fatalf("base-index %d %s %s", code, out, diagnostic)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Windows()[0].Index() != 4 {
		t.Fatalf("initial index %d", snapshot.Windows()[0].Index())
	}
}

func TestPythonShellAndPluginBridge(t *testing.T) {
	check := exec.Command("python3", "-c", "from importlib.metadata import version; print(version('tmuxp'))")
	version, err := check.Output()
	if err != nil || strings.TrimSpace(string(version)) != "1.74.0" {
		t.Skip("optional tmuxp 1.74.0 runtime is unavailable")
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "shell-target"}})
	code, out, diagnostic := run(t, "shell", "shell-target", "-S", server.SocketPath(), "-c", "print(session.session_name)", "--json")
	if code != 0 || !json.Valid([]byte(out)) || !strings.Contains(out, "shell-target") || diagnostic != "" {
		t.Fatalf("shell bridge %d %s %s", code, out, diagnostic)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "plugin-ran")
	write(t, dir, "native_plugin.py", "from tmuxp.plugin import TmuxpPlugin\nfrom pathlib import Path\nclass Plugin(TmuxpPlugin):\n    def before_script(self, session):\n        Path("+strconv.Quote(marker)+").write_text(session.session_name)\n")
	t.Setenv("PYTHONPATH", dir)
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	path := write(t, dir, "plugin.yaml", "session_name: bridged\nplugins: [native_plugin.Plugin]\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic = run(t, "load", path, "-S", server.SocketPath(), "-d", "--json")
	if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
		t.Fatalf("plugin bridge %d %s %s", code, out, diagnostic)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "bridged" {
		t.Fatalf("plugin did not execute: %q %v", got, err)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var target tmux.Pane
	for _, pane := range snapshot.Panes() {
		session, _ := pane.Session()
		if name, _ := session.Name(); name == "shell-target" {
			target = pane
			break
		}
	}
	if target.ID() == "" {
		t.Fatal("missing borrowed pane")
	}
	borrowed, _ := target.Session()
	pid, err := server.Cmd(t.Context(), "display-message", "-p", "#{pid}")
	if err != nil || pid.ExitCode != 0 {
		t.Fatalf("server identity: %+v %v", pid, err)
	}
	t.Setenv("TMUX", server.SocketPath()+","+strings.TrimSpace(string(pid.RawStdout))+",0")
	t.Setenv("TMUX_PANE", target.ID().String())
	for _, detached := range []bool{false, true} {
		t.Run("append-detached-"+strconv.FormatBool(detached), func(t *testing.T) {
			path := write(t, dir, "append.yaml", "session_name: append-"+strconv.FormatBool(detached)+"\nplugins: [native_plugin.Plugin]\nwindows:\n- panes: [blank]\n")
			args := []string{"load", path, "-S", server.SocketPath(), "--append", "--json"}
			if detached {
				args = append(args, "-d")
			}
			code, out, diagnostic := run(t, args...)
			if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
				t.Fatalf("plugin append %d %s %s", code, out, diagnostic)
			}
			got, err := os.ReadFile(marker)
			if err != nil || string(got) != "shell-target" {
				t.Errorf("plugin targeted %q instead of borrowed session: %v", got, err)
			}
			after, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantWindows := 3
			if detached {
				wantWindows++
			}
			if len(after.Sessions()) != 2 || len(after.Windows()) != wantWindows {
				t.Errorf("plugin append changed session ownership: sessions=%d windows=%d", len(after.Sessions()), len(after.Windows()))
			}
			retained, err := after.SessionByID(borrowed.ID())
			if err != nil {
				t.Fatal(err)
			}
			windows, _ := retained.Windows()
			if len(windows) != wantWindows-1 {
				t.Errorf("borrowed session windows=%d", len(windows))
			}
			if _, err := after.PaneByID(target.ID()); err != nil {
				t.Errorf("borrowed pane lost: %v", err)
			}
		})
	}
}

func TestAppendRejectsDifferentSocketWithSamePaneID(t *testing.T) {
	first := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	second := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	for _, server := range []tmux.Server{first, second} {
		if _, err := server.NewSession(t.Context(), tmux.NewSessionRequest{Name: "existing"}); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX", first.SocketPath()+",1,0")
	t.Setenv("TMUX_PANE", "%0")
	path := write(t, t.TempDir(), "append.yaml", "session_name: append\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", "--append", "-S", second.SocketPath(), "--json", path)
	if code != 2 || out != "" || !strings.Contains(diagnostic, "current tmux server") {
		t.Fatalf("cross-server append was accepted: %d %q %q", code, out, diagnostic)
	}
	snapshot, err := second.Snapshot(t.Context())
	if err != nil || len(snapshot.Windows()) != 1 {
		t.Fatalf("unrelated server mutated: %v windows=%d", err, len(snapshot.Windows()))
	}
}

func TestBooleanOptionsAndBeforeScriptResult(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	path := write(t, t.TempDir(), "options.yaml", "session_name: options\nbefore_script: /bin/sh -c 'printf retained'\noptions: {renumber-windows: true}\nwindows:\n- options: {automatic-rename: false, remain-on-exit: true}\n  panes: [blank]\n")
	code, out, diagnostic := run(t, "load", "-S", server.SocketPath(), "-d", "--json", path)
	if code != 0 || !strings.Contains(out, `"stdout":"retained"`) {
		t.Fatalf("boolean options/script capture: %d %q %q", code, out, diagnostic)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil || len(snapshot.Windows()) != 1 {
		t.Fatalf("window topology: %v", err)
	}
	result, err := server.Cmd(t.Context(), "show-options", "-w", "-v", "-t", snapshot.Windows()[0].ID().String(), "remain-on-exit")
	if err != nil || strings.TrimSpace(string(result.RawStdout)) != "on" {
		t.Fatalf("boolean option was not applied: %+v %v", result, err)
	}
}

func TestHumanInsideTmuxChoice(t *testing.T) {
	for _, choice := range []string{"n", "a"} {
		t.Run(choice, func(t *testing.T) {
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
			if _, err := server.NewSession(t.Context(), tmux.NewSessionRequest{Name: "original"}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMUX", server.SocketPath()+",1,0")
			t.Setenv("TMUX_PANE", "%0")
			path := write(t, t.TempDir(), "choice.yaml", "session_name: choice\nwindows:\n- panes: [blank]\n")
			var out, diagnostic bytes.Buffer
			code := cli.Run(t.Context(), []string{"load", "-S", server.SocketPath(), path}, strings.NewReader(choice+"\n"), &out, &diagnostic)
			if code != 0 || !strings.Contains(diagnostic.String(), "append") {
				t.Fatalf("interactive choice: %d %q %q", code, out.String(), diagnostic.String())
			}
			snapshot, err := server.Snapshot(t.Context())
			want := 2
			if choice == "a" {
				want = 1
			}
			if err != nil || len(snapshot.Sessions()) != want || len(snapshot.Windows()) != 2 {
				t.Fatalf("choice effects: %v sessions=%d windows=%d", err, len(snapshot.Sessions()), len(snapshot.Windows()))
			}
		})
	}
}
