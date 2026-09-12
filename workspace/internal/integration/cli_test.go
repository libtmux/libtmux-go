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

func daemonPID(t *testing.T, server tmux.Server) string {
	t.Helper()
	result, err := server.Cmd(t.Context(), "display-message", "-p", "#{pid}")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("daemon PID: %+v %v", result, err)
	}
	value := strings.TrimSpace(string(result.RawStdout))
	if pid, err := strconv.Atoi(value); err != nil || pid <= 0 {
		t.Fatalf("invalid daemon PID %q: %v", value, err)
	}
	return value
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
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
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

func TestLoadNamesOnlyFinalInput(t *testing.T) {
	for _, plugins := range []bool{false, true} {
		t.Run("plugins-"+strconv.FormatBool(plugins), func(t *testing.T) {
			if plugins {
				version, err := exec.Command("python3", "-c", "from importlib.metadata import version; print(version('tmuxp'))").Output()
				if err != nil || strings.TrimSpace(string(version)) != "1.74.0" {
					t.Skip("optional tmuxp 1.74.0 runtime is unavailable")
				}
			}
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
			dir := t.TempDir()
			extensions := ""
			if plugins {
				write(t, dir, "name_plugin.py", "from tmuxp.plugin import TmuxpPlugin\nclass Plugin(TmuxpPlugin):\n    pass\n")
				t.Setenv("PYTHONPATH", dir)
				t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
				extensions = "plugins: [name_plugin.Plugin]\n"
			}
			first := write(t, dir, "first.yaml", "session_name: first\n"+extensions+"windows:\n- window_name: earlier\n  panes: [blank]\n")
			last := write(t, dir, "last.yaml", "session_name: last\n"+extensions+"windows:\n- window_name: final\n  panes: [blank]\n")
			code, out, diagnostic := run(t, "load", first, last, "-s", "renamed", "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json")
			if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
				t.Fatalf("multi-input naming: %d %s %s", code, out, diagnostic)
			}
			var result struct {
				Results []struct {
					Name   string `json:"session_name"`
					Reused bool   `json:"reused"`
				} `json:"results"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Results) != 2 || result.Results[0].Name != "first" || result.Results[1].Name != "renamed" || result.Results[0].Reused || result.Results[1].Reused {
				t.Errorf("unexpected input results: %+v", result.Results)
			}
			snapshot, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Sessions()) != 2 || len(snapshot.Windows()) != 2 {
				t.Errorf("unexpected topology: sessions=%d windows=%d", len(snapshot.Sessions()), len(snapshot.Windows()))
			}
			for _, window := range snapshot.Windows() {
				session, _ := window.Session()
				name, _ := session.Name()
				windowName, _ := window.Name()
				if (name != "first" || windowName != "earlier") && (name != "renamed" || windowName != "final") {
					t.Errorf("window %q loaded into session %q", windowName, name)
				}
			}
		})
	}
}

func TestLoadColorPreflightAnd256Colors(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}})
	before, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	wrapper := write(t, dir, "tmux", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_WORKSPACE_TEST_CALLS\"\nexec \"$TMUX_WORKSPACE_TEST_TMUX\" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_WORKSPACE_TEST_CALLS", calls)
	t.Setenv("TMUX_WORKSPACE_TEST_TMUX", binary)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	first := write(t, dir, "first.yaml", "session_name: first\nwindows:\n- panes: [blank]\n")
	last := write(t, dir, "last.yaml", "session_name: last\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", first, last, "-S", server.SocketPath(), "-d", "-8", "--ndjson")
	if code != 2 || out != "" || !strings.Contains(diagnostic, "unsupported_color_mode") {
		t.Errorf("expected color preflight refusal: %d %s %s", code, out, diagnostic)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Errorf("legacy color request invoked tmux: %v", err)
	}
	after, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Sessions()) != 1 || len(after.Windows()) != 1 || len(after.Panes()) != 1 || after.Sessions()[0].ID() != before.Sessions()[0].ID() || after.Windows()[0].ID() != before.Windows()[0].ID() || after.Panes()[0].ID() != before.Panes()[0].ID() {
		t.Error("legacy color request changed retained topology")
	}
	if err := os.WriteFile(calls, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, diagnostic = run(t, "load", first, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "-2", "--json")
	if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
		t.Fatalf("256-color load: %d %s %s", code, out, diagnostic)
	}
	invocations, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, invocation := range strings.Split(strings.TrimSpace(string(invocations)), "\n") {
		if !strings.Contains(" "+invocation+" ", " -2 ") {
			t.Errorf("256-color client missing -2: %s", invocation)
		}
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
				t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
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
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
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
	t.Run("before-script-preserves-borrowed-session", func(t *testing.T) {
		scriptMarker := filepath.Join(dir, "script-ran")
		script := "/bin/sh -c 'printf attempted > \"$1\"; exit 7' sh " + strconv.Quote(scriptMarker)
		path := write(t, dir, "unsafe.json", `{"session_name":"unsafe","before_script":`+strconv.Quote(script)+`,"plugins":["native_plugin.Plugin"],"windows":[{"panes":[null]}]}`)
		code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "--append", "--json")
		if code != 2 || out != "" || !strings.Contains(diagnostic, "unsupported_combination") {
			t.Errorf("expected preflight refusal: %d %s %s", code, out, diagnostic)
		}
		if _, err := os.Stat(scriptMarker); !os.IsNotExist(err) {
			t.Errorf("unsafe script executed: %v", err)
		}
		after, err := server.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := after.SessionByID(borrowed.ID()); err != nil {
			t.Errorf("borrowed session deleted: %v", err)
		}
		if _, err := after.PaneByID(target.ID()); err != nil {
			t.Errorf("borrowed pane deleted: %v", err)
		}
		if len(after.Sessions()) != 2 || len(after.Windows()) != 4 {
			t.Errorf("borrowed topology changed: sessions=%d windows=%d", len(after.Sessions()), len(after.Windows()))
		}
	})
}

func TestAppendBridgeAuthenticatesBeforeImportsAndBuild(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("optional Python runtime is unavailable")
	}
	version, err := exec.Command(python, "-c", "from importlib.metadata import version; print(version('tmuxp'))").Output()
	if err != nil || strings.TrimSpace(string(version)) != "1.74.0" {
		t.Skip("optional tmuxp 1.74.0 runtime is unavailable")
	}
	for _, boundary := range []string{"retarget", "missing", "constructor", "runtime"} {
		t.Run(boundary, func(t *testing.T) {
			options := tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}}
			first := tmuxtest.NewServerWithOptions(t.Context(), t, options)
			second := tmuxtest.NewServerWithOptions(t.Context(), t, options)
			if _, err := first.NewSession(t.Context(), tmux.NewSessionRequest{Name: "keepalive"}); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			alias := filepath.Join(dir, "socket")
			if err := os.Symlink(first.SocketPath(), alias); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMUX", alias+","+daemonPID(t, first)+",0")
			t.Setenv("TMUX_PANE", "%0")
			t.Setenv("APPEND_REAL_PYTHON", python)
			t.Setenv("APPEND_BOUNDARY", boundary)
			t.Setenv("APPEND_ALIAS", alias)
			t.Setenv("APPEND_ORIGINAL", first.SocketPath())
			t.Setenv("APPEND_REPLACEMENT", second.SocketPath())
			t.Setenv("APPEND_MARKERS", dir)
			t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
			wrapper := write(t, dir, "python", `#!/bin/sh
set -eu
if [ "$1" = -c ] && [ "$APPEND_BOUNDARY" = runtime ]; then
    rm "$APPEND_ALIAS"
    ln -s "$APPEND_REPLACEMENT" "$APPEND_ALIAS"
    : > "$APPEND_MARKERS/runtime-check"
fi
if [ "$1" = -u ]; then
    case "$APPEND_BOUNDARY" in
        retarget) rm "$APPEND_ALIAS"; ln -s "$APPEND_REPLACEMENT" "$APPEND_ALIAS" ;;
        missing) tmux -S "$APPEND_ORIGINAL" kill-session -t '$0' ;;
    esac
    : > "$APPEND_MARKERS/handed-off"
fi
exec "$APPEND_REAL_PYTHON" "$@"
`)
			if err := os.Chmod(wrapper, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMUX_WORKSPACE_PYTHON", wrapper)
			write(t, dir, "extension.py", `import os
from pathlib import Path
from tmuxp.workspace.builder.classic import ClassicWorkspaceBuilder
markers = Path(os.environ['APPEND_MARKERS'])
(markers / 'imported').touch()
class Builder(ClassicWorkspaceBuilder):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        (markers / 'constructed').touch()
        if os.environ['APPEND_BOUNDARY'] == 'constructor':
            alias = Path(os.environ['APPEND_ALIAS'])
            alias.unlink()
            alias.symlink_to(os.environ['APPEND_REPLACEMENT'])
    def build(self, *args, **kwargs):
        (markers / 'built').touch()
        return super().build(*args, **kwargs)
`)
			path := write(t, dir, "workspace.json", `{"session_name":"unwanted","workspace_builder":"extension:Builder","workspace_builder_paths":[`+strconv.Quote(dir)+`],"windows":[{"panes":[null]}]}`)
			args := []string{"load", "--append", "--json", "-S", alias}
			if boundary == "runtime" {
				script := write(t, dir, "native.sh", ": > \"$APPEND_MARKERS/native-script\"\n")
				native := write(t, dir, "native.json", `{"session_name":"native","before_script":`+strconv.Quote("/bin/sh "+strconv.Quote(script))+`,"windows":[{"panes":[null]}]}`)
				args = append(args, native)
			}
			code, out, diagnostic := run(t, append(args, path)...)
			if code != 1 || !strings.Contains(out, "append_context") || !strings.Contains(out, `"session_id":"$0"`) {
				t.Errorf("borrowed target failure was not retained: %d %q %q", code, out, diagnostic)
			}
			for _, marker := range []string{"handed-off", "imported", "constructed", "built", "runtime-check", "native-script"} {
				_, err := os.Stat(filepath.Join(dir, marker))
				want := marker == "handed-off" || boundary == "constructor" && (marker == "imported" || marker == "constructed") || boundary == "runtime" && marker == "runtime-check"
				if want && err != nil || !want && !os.IsNotExist(err) {
					t.Errorf("%s marker: %v, want present=%t", marker, err, want)
				}
			}
			for index, server := range []tmux.Server{first, second} {
				want := 1
				if index == 0 && boundary != "missing" {
					want = 2
				}
				snapshot, err := server.Snapshot(t.Context())
				if err != nil || len(snapshot.Sessions()) != want || len(snapshot.Windows()) != want {
					t.Errorf("bridge changed topology beyond fixture action: %v sessions=%d windows=%d want=%d", err, len(snapshot.Sessions()), len(snapshot.Windows()), want)
				}
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
	t.Setenv("TMUX", first.SocketPath()+","+daemonPID(t, first)+",0")
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

func TestAppendAuthenticatesDaemonBeforePython(t *testing.T) {
	for _, retarget := range []bool{false, true} {
		t.Run(strconv.FormatBool(retarget), func(t *testing.T) {
			options := tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}}
			first := tmuxtest.NewServerWithOptions(t.Context(), t, options)
			second := tmuxtest.NewServerWithOptions(t.Context(), t, options)
			pid := daemonPID(t, first)
			if pid == daemonPID(t, second) {
				t.Fatal("fixtures share a daemon")
			}
			dir := t.TempDir()
			alias := filepath.Join(dir, "inherited")
			selected := second.SocketPath()
			if err := os.Symlink(first.SocketPath(), alias); err != nil {
				t.Fatal(err)
			}
			if retarget {
				if err := os.Remove(alias); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(second.SocketPath(), alias); err != nil {
					t.Fatal(err)
				}
				selected = alias
			}
			t.Setenv("TMUX", alias+","+pid+",0")
			t.Setenv("TMUX_PANE", "%0")
			marker := filepath.Join(dir, "python-called")
			python := write(t, dir, "python", "#!/bin/sh\n: > \"$APPEND_PYTHON_MARKER\"\nexit 0\n")
			if err := os.Chmod(python, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("APPEND_PYTHON_MARKER", marker)
			t.Setenv("TMUX_WORKSPACE_PYTHON", python)
			for _, bridge := range []bool{false, true} {
				config := "session_name: unwanted\nwindows:\n- panes: [blank]\n"
				if bridge {
					config += "plugins: [never.Imported]\n"
				}
				path := write(t, dir, "workspace.yaml", config)
				for _, mode := range []string{"--json", "--ndjson"} {
					_ = os.Remove(marker)
					code, out, diagnostic := run(t, "load", "-S", selected, "--append", mode, path)
					if code != 2 || out != "" || !strings.Contains(diagnostic, "current tmux server") {
						t.Errorf("bridge=%t %s: invalid append returned %d %q %q", bridge, mode, code, out, diagnostic)
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Errorf("bridge=%t %s: Python ran before authentication: %v", bridge, mode, err)
					}
				}
			}
			for _, server := range []tmux.Server{first, second} {
				snapshot, err := server.Snapshot(t.Context())
				if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 1 || len(snapshot.Panes()) != 1 {
					t.Errorf("borrowed topology changed: %v windows=%d", err, len(snapshot.Windows()))
				}
			}
		})
	}
}

func TestAppendSupportsMatchingSocketAliasesAndCommas(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}})
	dir := t.TempDir()
	pid := daemonPID(t, server)
	path := write(t, dir, "workspace.yaml", "session_name: ignored\nwindows:\n- panes: [blank]\n")
	for _, name := range []string{"socket", "full,with,commas"} {
		alias := filepath.Join(dir, name)
		if err := os.Symlink(server.SocketPath(), alias); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMUX", alias+","+pid+",0")
		t.Setenv("TMUX_PANE", "%0")
		for _, explicit := range []bool{true, false} {
			args := []string{"load", "--append", "--json", path}
			if explicit {
				args = append(args, "-S", alias)
			}
			code, out, diagnostic := run(t, args...)
			if code != 0 || !strings.Contains(out, `"session_id":"$0"`) {
				t.Errorf("%s explicit=%t: %d %q %q", name, explicit, code, out, diagnostic)
			}
		}
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 5 {
		t.Fatalf("matching append topology: %v windows=%d", err, len(snapshot.Windows()))
	}
	if _, err := snapshot.PaneByID("%0"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWithEmptyInheritedContext(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}})
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	selected, err := tmux.NewServer(tmux.ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(selected.SocketPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(server.SocketPath(), selected.SocketPath()); err != nil {
		t.Fatal(err)
	}
	path := write(t, t.TempDir(), "workspace.yaml", "session_name: added\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", "-d", "--json", path)
	if code != 0 || !strings.Contains(out, `"session_id":"$1"`) {
		t.Fatalf("empty inherited context: %d %q %q", code, out, diagnostic)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil || len(snapshot.Sessions()) != 2 || len(snapshot.Windows()) != 2 {
		t.Fatalf("default endpoint topology: %v sessions=%d windows=%d", err, len(snapshot.Sessions()), len(snapshot.Windows()))
	}
}

func TestAppendGuardsGlobalWritesAfterScriptRetargetsEndpoint(t *testing.T) {
	options := tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}}
	first := tmuxtest.NewServerWithOptions(t.Context(), t, options)
	second := tmuxtest.NewServerWithOptions(t.Context(), t, options)
	dir := t.TempDir()
	alias := filepath.Join(dir, "inherited")
	if err := os.Symlink(first.SocketPath(), alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", alias+","+daemonPID(t, first)+",0")
	t.Setenv("TMUX_PANE", "%0")
	t.Setenv("APPEND_ALIAS", alias)
	t.Setenv("APPEND_REPLACEMENT", second.SocketPath())
	marker := filepath.Join(dir, "script-ran")
	t.Setenv("APPEND_SCRIPT_MARKER", marker)
	script := write(t, dir, "retarget.sh", "rm \"$APPEND_ALIAS\"\nln -s \"$APPEND_REPLACEMENT\" \"$APPEND_ALIAS\"\nprintf done > \"$APPEND_SCRIPT_MARKER\"\n")
	path := write(t, dir, "workspace.json", `{"session_name":"ignored","before_script":`+strconv.Quote("/bin/sh "+strconv.Quote(script))+`,"global_options":{"@APPEND_AUTH_MARKER":"written"},"windows":[{"panes":[null]}]}`)
	code, out, diagnostic := run(t, "load", "--append", "--json", path)
	if code != 1 || !strings.Contains(out, "server was replaced") {
		t.Errorf("expected guarded failure: %d %q %q", code, out, diagnostic)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("authorized script did not run: %v", err)
	}
	for _, server := range []tmux.Server{first, second} {
		result, err := server.Cmd(t.Context(), "show-options", "-gqv", "@APPEND_AUTH_MARKER")
		if err != nil || result.ExitCode != 0 || len(result.RawStdout) != 0 {
			t.Errorf("post-script global mutation: %+v %v", result, err)
		}
		snapshot, err := server.Snapshot(t.Context())
		if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 1 {
			t.Errorf("post-script topology changed: %v windows=%d", err, len(snapshot.Windows()))
		}
	}
}

func TestAppendRetainsSessionAcrossInputsAfterPaneMoves(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}})
	for _, command := range [][]string{
		{"split-window", "-h", "-t", "%0"},
		{"new-session", "-d", "-s", "other"},
	} {
		result, err := server.Cmd(t.Context(), command...)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("create topology: %+v %v", result, err)
		}
	}
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
	t.Setenv("TMUX_PANE", "%0")
	t.Setenv("APPEND_SOCKET", server.SocketPath())
	dir := t.TempDir()
	script := write(t, dir, "move.sh", "exec tmux -S \"$APPEND_SOCKET\" join-pane -s %0 -t %2\n")
	first := write(t, dir, "first.json", `{"session_name":"first","before_script":`+strconv.Quote("/bin/sh "+strconv.Quote(script))+`,"windows":[{"panes":[null]}]}`)
	second := write(t, dir, "second.yaml", "session_name: second\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", "--append", "--json", first, second)
	if code != 0 {
		t.Fatalf("multi-input append: %d %q %q", code, out, diagnostic)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range snapshot.Sessions() {
		windows, _ := session.Windows()
		want := 1
		if name, _ := session.Name(); name == "retained" {
			want = 3
		}
		if len(windows) != want {
			t.Errorf("retained append target changed: %s windows=%d want=%d; %s", session, len(windows), want, out)
		}
	}
	pane, err := snapshot.PaneByID("%0")
	if err != nil || pane.SessionID() != "$1" {
		t.Fatalf("authorized pane move was not retained: %+v %v", pane, err)
	}
}

func TestAppendLinkedPaneUsesTmuxCanonicalSession(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "initial"}})
	for _, command := range [][]string{
		{"new-session", "-d", "-s", "secondary"},
		{"link-window", "-s", "@0", "-t", "secondary:7"},
		{"select-window", "-t", "secondary:7"},
	} {
		result, err := server.Cmd(t.Context(), command...)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("link pane fixture: %+v %v", result, err)
		}
	}
	result, err := server.Cmd(t.Context(), "display-message", "-p", "-t", "%0", "#{session_id}")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("canonical session oracle: %+v %v", result, err)
	}
	want := tmux.SessionID(strings.TrimSpace(string(result.RawStdout)))
	before, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.PanesByID("%0")) != 2 {
		t.Fatal("fixture does not contain linked pane views")
	}
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
	t.Setenv("TMUX_PANE", "%0")
	path := write(t, t.TempDir(), "workspace.yaml", "session_name: ignored\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", "--append", "--json", path)
	if code != 0 {
		t.Fatalf("linked append: %d %q %q", code, out, diagnostic)
	}
	after, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, prior := range before.Sessions() {
		current, err := after.SessionByID(prior.ID())
		if err != nil {
			t.Fatal(err)
		}
		oldWindows, _ := prior.Windows()
		newWindows, _ := current.Windows()
		expected := len(oldWindows)
		if prior.ID() == want {
			expected++
		}
		if len(newWindows) != expected {
			t.Errorf("canonical target %s: %s has %d windows, want %d", want, current, len(newWindows), expected)
		}
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
			t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
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
