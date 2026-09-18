package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestLayoutPreflightLaterInputBeforeScripts(t *testing.T) {
	server := tmuxtest.NewServer(t.Context(), t)
	pid := daemonPID(t, server)
	dir := t.TempDir()
	marker := filepath.Join(dir, "script-ran")
	first, err := json.Marshal(map[string]any{
		"session_name": "layout-first", "before_script": "touch " + strconv.Quote(marker),
		"windows": []map[string]any{{"panes": []any{nil}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	one := write(t, dir, "first.json", string(first))
	two := write(t, dir, "last.json", `{"session_name":"layout-last","windows":[{"layout":"b25d,80x24,0,0,0","panes":[null,null]}]}`)
	code, out, diagnostic := run(t, "load", "-d", "--json", "-S", server.SocketPath(), "-f", server.ConfigFile(), one, two)
	if code == 0 || out != "" || !strings.Contains(diagnostic, "layout") {
		t.Errorf("load = %d %q %q, want preflight error", code, out, diagnostic)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("before_script ran before later layout refusal: %v", err)
	}
	sessions, err := server.Sessions(t.Context())
	if err != nil || len(sessions) != 1 || daemonPID(t, server) != pid {
		t.Fatalf("keeper changed: sessions=%v err=%v", sessions, err)
	}
}

func TestUnknownExecutionFieldsPrecedeScriptsAndMutations(t *testing.T) {
	for _, fields := range []string{
		`"before_scrip":"ignored"`,
		`"windows":[{"shell_command_befor":"ignored","panes":[null]}]`,
		`"windows":[{"panes":[{"shell_commmand":"ignored"}]}]`, //nolint:misspell // Deliberately misspelled execution key.
		`"windows":[{"panes":[{"shell_command":[{"cmd":"ignored","entter":false}]}]}]`,
		`"workspace_builder_options":{"pane_readines":"never"}`,
	} {
		t.Run(fields, func(t *testing.T) {
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "keeper"}})
			before, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			pid := daemonPID(t, server)
			dir := t.TempDir()
			marker := filepath.Join(dir, "script-ran")
			first, err := json.Marshal(map[string]any{
				"session_name": "validation-first", "before_script": "touch " + strconv.Quote(marker),
				"windows": []map[string]any{{"panes": []any{nil}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			one := write(t, dir, "first.json", string(first))
			invalid := map[string]any{"session_name": "validation-last", "windows": []any{map[string]any{"panes": []any{nil}}}}
			if err := json.Unmarshal([]byte("{"+fields+"}"), &invalid); err != nil {
				t.Fatal(err)
			}
			last, err := json.Marshal(invalid)
			if err != nil {
				t.Fatal(err)
			}
			two := write(t, dir, "last.json", string(last))
			code, out, diagnostic := run(t, "load", "-d", "--json", "-S", server.SocketPath(), "-f", server.ConfigFile(), one, two)
			if code != 1 || out != "" || !strings.Contains(diagnostic, "unknown field") {
				t.Errorf("load = %d %q %q, want unknown-key preflight error", code, out, diagnostic)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("before_script ran before later key refusal: %v", err)
			}
			after, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Sessions()) != 1 || len(after.Windows()) != 1 || len(after.Panes()) != 1 || after.Sessions()[0].ID() != before.Sessions()[0].ID() || after.Windows()[0].ID() != before.Windows()[0].ID() || after.Panes()[0].ID() != before.Panes()[0].ID() || daemonPID(t, server) != pid {
				t.Errorf("preflight changed retained topology: sessions=%d windows=%d panes=%d", len(after.Sessions()), len(after.Windows()), len(after.Panes()))
			}
		})
	}
}

// TestFreezeYesAnswersFormatPromptWithoutATerminal checks that --yes without
// a terminal takes the documented yaml default instead of prompting.
func TestFreezeYesAnswersFormatPromptWithoutATerminal(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "frozen-yes"},
	})
	dir := t.TempDir()
	destination := filepath.Join(dir, "frozen.yaml")
	code, out, diagnostic := run(t, "freeze", "frozen-yes", "-S", server.SocketPath(),
		"--save-to", destination, "--force", "--yes")
	if code != 0 || diagnostic != "" {
		t.Fatalf("freeze --yes without a terminal: %d %q %q", code, out, diagnostic)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("freeze --yes did not write %s: %v", destination, err)
	}
}

// TestFreezeSaveToNeedsNoConfirmationWithoutATerminal: an explicit
// --save-to is consent to that destination, so freeze must write without
// --yes even with no terminal attached. --force still governs replacing an
// existing file.
func TestFreezeSaveToNeedsNoConfirmationWithoutATerminal(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "frozen-consent"},
	})
	dir := t.TempDir()
	destination := filepath.Join(dir, "frozen.yaml")
	code, out, diagnostic := run(t, "freeze", "frozen-consent", "-S", server.SocketPath(), "--save-to", destination)
	if code != 0 || diagnostic != "" {
		t.Fatalf("freeze --save-to without --yes or a terminal: %d %q %q", code, out, diagnostic)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("freeze --save-to did not write %s: %v", destination, err)
	}
	// The negative case: --save-to alone is not --force, so an existing
	// destination is still refused.
	code, out, diagnostic = run(t, "freeze", "frozen-consent", "-S", server.SocketPath(), "--save-to", destination)
	if code == 0 || !strings.Contains(diagnostic, "destination exists") {
		t.Fatalf("freeze --save-to over an existing file without --force: %d %q %q", code, out, diagnostic)
	}
	// Same refusal, --json mode: checks the destination_exists code.
	code, out, diagnostic = run(t, "freeze", "frozen-consent", "-S", server.SocketPath(), "--save-to", destination, "--json")
	var envelope map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
		t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
	}
	if code == 0 || out != "" || envelope["code"] != "destination_exists" {
		t.Fatalf("freeze --save-to --json over an existing file: %d %q code=%v", code, out, envelope["code"])
	}
}

// TestFreezeWithoutADestinationIsAUsageError: freeze with no --save-to and
// no machine format is refused before touching tmux, even with --yes and
// even though the target session exists. An explicit --save-to into a
// missing directory is a different failure and still fails.
func TestFreezeWithoutADestinationIsAUsageError(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "keep"},
	})
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, out, diagnostic := run(t, "freeze", "keep", "-S", server.SocketPath(), "--yes")
	if code != 2 || out != "" || !strings.Contains(diagnostic, "--save-to") {
		t.Fatalf("freeze without --save-to: %d %q %q", code, out, diagnostic)
	}
	if _, err := os.Stat(filepath.Join(home, ".tmuxp")); !os.IsNotExist(err) {
		t.Fatalf("freeze without --save-to wrote the default directory: %v", err)
	}
	// The negative case: an explicit --save-to into a missing directory is
	// a different failure and still fails.
	explicit := filepath.Join(t.TempDir(), "missing-dir", "frozen.yaml")
	code, out, diagnostic = run(t, "freeze", "keep", "-S", server.SocketPath(), "--save-to", explicit)
	if code == 0 {
		t.Fatalf("freeze --save-to into a missing directory unexpectedly succeeded: %d %q %q", code, out, diagnostic)
	}
}

// TestFreezeMissingSessionReportsSessionNotFound: a freeze target that
// does not exist reports session_not_found. Three related codes
// (workspace_not_found, invalid_workspace, unsupported_key) need no live
// server and live in the cli package's TestLoadErrorCodes.
func TestFreezeMissingSessionReportsSessionNotFound(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "keeper"},
	})
	code, out, diagnostic := run(t, "freeze", "nosuch", "-S", server.SocketPath(), "--json")
	if code != 1 || out != "" {
		t.Fatalf("freeze nosuch: %d %q %q", code, out, diagnostic)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
		t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
	}
	if envelope["code"] != "session_not_found" {
		t.Fatalf("freeze nosuch code = %v, want %q (%s)", envelope["code"], "session_not_found", diagnostic)
	}
}

// TestFreezeOnEmptyServerReportsSessionNotFound is
// TestFreezeMissingSessionReportsSessionNotFound's other case: a server
// that is running but owns zero sessions fails its underlying "list
// windows" query outright ("no current target") rather than reporting an
// empty session list, which must not surface as the generic
// operation_failed.
func TestFreezeOnEmptyServerReportsSessionNotFound(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		Config: []byte("set -g exit-empty off\n"),
	})
	if result, err := server.Cmd(t.Context(), "start-server"); err != nil || result.ExitCode != 0 {
		t.Fatalf("start-server: %+v %v", result, err)
	}
	code, out, diagnostic := run(t, "freeze", "nosuch", "-S", server.SocketPath(), "--json")
	if code != 1 || out != "" {
		t.Fatalf("freeze nosuch on an empty server: %d %q %q", code, out, diagnostic)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
		t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
	}
	if envelope["code"] != "session_not_found" {
		t.Fatalf("freeze nosuch on an empty server code = %v, want %q (%s)", envelope["code"], "session_not_found", diagnostic)
	}
}

// TestFreezeOnUnstartedServerReportsSessionNotFound is the third case: a
// socket whose server has never started holds no session either, so the
// answer is the one a missing name gets, not tmux's transport error.
func TestFreezeOnUnstartedServerReportsSessionNotFound(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "cold.sock")
	code, out, diagnostic := run(t, "freeze", "nosuch", "-S", socket, "--json")
	if code != 1 || out != "" {
		t.Fatalf("freeze nosuch on an unstarted server: %d %q %q", code, out, diagnostic)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
		t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
	}
	if envelope["code"] != "session_not_found" {
		t.Fatalf("code = %v, want %q (%s)", envelope["code"], "session_not_found", diagnostic)
	}
	if strings.Contains(diagnostic, "error connecting to") {
		t.Fatalf("message leaks tmux's socket error: %s", diagnostic)
	}
}

// TestLoadTmuxFailureReportsTmuxFailedCode: a tmux command failing while
// building -- an unknown option here -- must give errors[].code and the
// stderr record's code tmux_failed, not the generic workspace_failed/
// load_failed a stderr-only consumer could not branch on.
func TestLoadTmuxFailureReportsTmuxFailedCode(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	path := write(t, dir, "bad-option.yaml", "session_name: bad-option\nwindows:\n- window_name: w\n  options:\n    no-such-option-xyz: 1\n  panes: [blank]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-d", "--json")
	if code != 1 || out == "" {
		t.Fatalf("load with an invalid option: %d %q %q", code, out, diagnostic)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("invalid summary %q: %v", out, err)
	}
	errs, _ := summary["errors"].([]any)
	entry, _ := first(errs).(map[string]any)
	if len(errs) != 1 || entry["code"] != "tmux_failed" {
		t.Fatalf("errors[0].code = %v, want tmux_failed: %s", entry["code"], out)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
		t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
	}
	if envelope["code"] != "tmux_failed" {
		t.Fatalf("stderr record code = %v, want tmux_failed (%s)", envelope["code"], diagnostic)
	}
}

// TestLoadScriptFailureReportsScriptFailedCode: a before_script that exits
// nonzero or cannot start gives errors[].code and the stderr record's code
// script_failed, and the message does not end in a bare ": ".
func TestLoadScriptFailureReportsScriptFailedCode(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	for name, script := range map[string]string{
		"exits nonzero": "'false'",
		"cannot start":  filepath.Join(dir, "missing-script"),
	} {
		t.Run(name, func(t *testing.T) {
			path := write(t, dir, "bad-script.yaml", "session_name: bad-script\nbefore_script: "+script+"\nwindows:\n- panes: [blank]\n")
			code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-d", "--json")
			if code != 1 || out == "" {
				t.Fatalf("load with a failing before_script: %d %q %q", code, out, diagnostic)
			}
			var summary map[string]any
			if err := json.Unmarshal([]byte(out), &summary); err != nil {
				t.Fatalf("invalid summary %q: %v", out, err)
			}
			errs, _ := summary["errors"].([]any)
			entry, _ := first(errs).(map[string]any)
			message, _ := entry["message"].(string)
			if len(errs) != 1 || entry["code"] != "script_failed" {
				t.Fatalf("errors[0].code = %v, want script_failed: %s", entry["code"], out)
			}
			if strings.HasSuffix(message, ": ") {
				t.Fatalf("message keeps an empty separator: %q", message)
			}
			var envelope map[string]any
			if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
				t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
			}
			if envelope["code"] != "script_failed" {
				t.Fatalf("stderr record code = %v, want script_failed (%s)", envelope["code"], diagnostic)
			}
		})
	}
}

func first(items []any) any {
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

// TestFreezeOmitsTheDefaultShellWhateverItIsNamed reproduces macOS on Linux,
// where /bin/sh is bash: default-shell reads "sh" and the pane reports "bash".
func TestFreezeOmitsTheDefaultShellWhateverItIsNamed(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		Config: []byte("set -g default-shell /bin/sh\nset -g default-command \"/bin/bash -i\"\n"),
	})
	dir := t.TempDir()
	path := write(t, dir, "named.yaml",
		"session_name: shell-name\nwindows:\n- window_name: only\n  panes:\n  - blank\n")
	if code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json"); code != 0 {
		t.Fatalf("load %d %s %s", code, out, diagnostic)
	}
	code, out, diagnostic := run(t, "freeze", "shell-name", "-S", server.SocketPath(), "--json")
	if code != 0 {
		t.Fatalf("freeze %d %s %s", code, out, diagnostic)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	windows, _ := doc["windows"].([]any)
	if len(windows) != 1 {
		t.Fatalf("windows: %+v", doc["windows"])
	}
	window, _ := windows[0].(map[string]any)
	panes, _ := window["panes"].([]any)
	if len(panes) != 1 {
		t.Fatalf("panes: %+v", window["panes"])
	}
	pane, _ := panes[0].(map[string]any)
	if command, present := pane["shell_command"]; present {
		t.Fatalf("pane started with no command still froze shell_command %v; "+
			"the default shell is /bin/sh but it reports as bash", command)
	}
}

// TestFreezeEmitsOptionsAfterAndArrayShellCommand covers two invariants:
// freeze must write window options under options_after, not options,
// because automatic-rename: off only holds if applied after the panes exist;
// and a pane's shell_command must be omitted when the pane runs the
// session's default shell and, when emitted for any other pane, must be an
// array rather than a bare string.
func TestFreezeEmitsOptionsAfterAndArrayShellCommand(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	path := write(t, dir, "capture.yaml",
		"session_name: capture-shape\n"+
			"windows:\n"+
			"- window_name: only\n"+
			"  panes:\n"+
			"  - blank\n"+
			"  - shell_command: sleep 5\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json")
	if code != 0 {
		t.Fatalf("load %d %s %s", code, out, diagnostic)
	}
	code, out, diagnostic = run(t, "freeze", "capture-shape", "-S", server.SocketPath(), "--json")
	if code != 0 {
		t.Fatalf("freeze %d %s %s", code, out, diagnostic)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	windows, _ := doc["windows"].([]any)
	if len(windows) != 1 {
		t.Fatalf("windows: %+v", doc["windows"])
	}
	window, _ := windows[0].(map[string]any)
	if _, present := window["options"]; present {
		t.Fatalf("freeze still emits options: %+v", window)
	}
	if _, present := window["options_after"]; !present {
		t.Fatalf("freeze does not emit options_after: %+v", window)
	}
	panes, _ := window["panes"].([]any)
	if len(panes) != 2 {
		t.Fatalf("panes: %+v", window["panes"])
	}
	first, _ := panes[0].(map[string]any)
	if _, present := first["shell_command"]; present {
		t.Fatalf("default-shell pane still carries shell_command: %+v", first)
	}
	second, _ := panes[1].(map[string]any)
	command, ok := second["shell_command"].([]any)
	if !ok || len(command) != 1 || command[0] != "sleep" {
		t.Fatalf("non-default pane shell_command = %v, want an array containing sleep", second["shell_command"])
	}
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
	path := write(t, dir, "fail.yaml", "session_name: failure\nbefore_script: 'false'\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-d", "--ndjson", "--log-level", "critical")
	if code != 1 || !json.Valid([]byte(diagnostic)) || !strings.Contains(diagnostic, "script_failed") || !strings.Contains(out, `"code":"script_failed"`) {
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

// TestBeforeScriptFailureHumanMessageNamesRemovedSession: a before_script
// failure on a session this load created removes that session, so nothing
// from this input survives. The JSON envelope's
// results entry must say so (removed:true), and human mode's top error
// line must not claim "completed effects are retained" when that is false
// -- it must instead name the failure and say the session was removed.
func TestBeforeScriptFailureHumanMessageNamesRemovedSession(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	path := write(t, dir, "fail.yaml", "session_name: bsfail\nbefore_script: 'false'\nwindows:\n- panes: [echo x]\n")

	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-d", "-y", "--json")
	if code != 1 || out == "" {
		t.Fatalf("load --json: %d %q %q", code, out, diagnostic)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("invalid summary %q: %v", out, err)
	}
	results, _ := summary["results"].([]any)
	entry, _ := first(results).(map[string]any)
	if len(results) != 1 || entry["removed"] != true {
		t.Fatalf("results[0].removed = %v, want true: %s", entry["removed"], out)
	}

	code, out, diagnostic = run(t, "load", path, "-S", server.SocketPath(), "-d", "-y")
	if code != 1 || out != "" {
		t.Fatalf("load: %d %q %q", code, out, diagnostic)
	}
	if strings.Contains(diagnostic, "retained") {
		t.Fatalf("human message claims retention when the removed session left nothing to retain: %q", diagnostic)
	}
	if !strings.Contains(diagnostic, "removed") {
		t.Fatalf("human message does not say the session it created was removed: %q", diagnostic)
	}
}

// TestBeforeScriptFailureOnBorrowedSessionHumanMessageClaimsRetention is
// the positive case: a before_script failure on an appended (borrowed)
// session leaves that session in place (never killed, never owned by this
// load), so the message correctly says effects are retained.
func TestBeforeScriptFailureOnBorrowedSessionHumanMessageClaimsRetention(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "owned"},
	})
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
	t.Setenv("TMUX_PANE", snapshot.Panes()[0].ID().String())
	dir := t.TempDir()
	path := write(t, dir, "fail-append.yaml", "session_name: unused\nbefore_script: 'false'\nwindows:\n- panes: [blank]\n")

	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "--append", "-y", "--json")
	if code != 1 || out == "" {
		t.Fatalf("load --append --json: %d %q %q", code, out, diagnostic)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("invalid summary %q: %v", out, err)
	}
	results, _ := summary["results"].([]any)
	entry, _ := first(results).(map[string]any)
	if len(results) != 1 || entry["removed"] != false {
		t.Fatalf("results[0].removed = %v, want false (a borrowed session is never killed): %s", entry["removed"], out)
	}

	code, out, diagnostic = run(t, "load", path, "-S", server.SocketPath(), "--append", "-y")
	if code != 1 || out != "" {
		t.Fatalf("load --append: %d %q %q", code, out, diagnostic)
	}
	if !strings.Contains(diagnostic, "retained") {
		t.Fatalf("human message must claim retention when the borrowed session survived: %q", diagnostic)
	}
}

// TestAppendWindowIndexCollisionNamesTheIndexAndClaimsNoRetention: an
// appended window whose window_index is already taken must fail with a
// message naming the index rather than tmux's redacted new-window text, and
// -- because nothing was created -- the human summary must not claim
// retained or removed effects.
func TestAppendWindowIndexCollisionNamesTheIndexAndClaimsNoRetention(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "owned"},
	})
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
	t.Setenv("TMUX_PANE", snapshot.Panes()[0].ID().String())
	dir := t.TempDir()
	path := write(t, dir, "collide.yaml", "session_name: unused\nwindows:\n- window_index: 0\n  panes: [blank]\n")

	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "--append", "-y", "--json")
	if code != 1 || out == "" {
		t.Fatalf("append collision --json: %d %q %q", code, out, diagnostic)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("invalid summary %q: %v", out, err)
	}
	errs, _ := summary["errors"].([]any)
	failureEntry, _ := first(errs).(map[string]any)
	message, _ := failureEntry["message"].(string)
	if !strings.Contains(message, "index 0") {
		t.Fatalf("collision message does not name the index: %q", out)
	}
	results, _ := summary["results"].([]any)
	resultEntry, _ := first(results).(map[string]any)
	if len(results) != 1 || resultEntry["removed"] != false {
		t.Fatalf("results[0].removed = %v, want false: %s", resultEntry["removed"], out)
	}

	code, out, diagnostic = run(t, "load", path, "-S", server.SocketPath(), "--append", "-y")
	if code != 1 || out != "" {
		t.Fatalf("append collision human: %d %q %q", code, out, diagnostic)
	}
	if strings.Contains(diagnostic, "retained") || strings.Contains(diagnostic, "removed") {
		t.Fatalf("human message wrongly claims retention or removal for a no-effect append: %q", diagnostic)
	}
	if !strings.Contains(diagnostic, "index 0") {
		t.Fatalf("human message does not name the index: %q", diagnostic)
	}
	after, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Windows()) != 1 {
		t.Fatalf("append collision mutated the borrowed session: windows=%d", len(after.Windows()))
	}
}

// TestLoadStartedEventReportsInputs covers the started event's input-count
// field name: a cross-port measurement settled on "inputs"; go and ts were
// the two that carried "input_count".
func TestLoadStartedEventReportsInputs(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	first := write(t, dir, "first.yaml", "session_name: first\nwindows:\n- panes: [blank]\n")
	second := write(t, dir, "second.yaml", "session_name: second\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", first, second, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--ndjson")
	if code != 0 || diagnostic != "" {
		t.Fatalf("load --ndjson: %d %s %s", code, out, diagnostic)
	}
	line, _, _ := strings.Cut(out, "\n")
	var started map[string]any
	if err := json.Unmarshal([]byte(line), &started); err != nil {
		t.Fatal(err)
	}
	if started["event"] != "started" {
		t.Fatalf("first record was %v, want started: %s", started["event"], line)
	}
	if _, present := started["input_count"]; present {
		t.Fatalf("started event still carries input_count: %s", line)
	}
	if inputs, ok := started["inputs"].(float64); !ok || inputs != 2 {
		t.Fatalf("started event inputs = %v, want 2: %s", started["inputs"], line)
	}
}

// TestScriptOutputNamesItsInput: with several workspaces in one load, every
// script-output record says which input wrote it.
func TestScriptOutputNamesItsInput(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	sources := make([]string, 0, 2)
	for _, name := range []string{"first", "second"} {
		script := filepath.Join(dir, name+".sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '"+name+"\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, write(t, dir, name+".yaml",
			"session_name: "+name+"\nbefore_script: "+script+"\nwindows:\n- panes: [blank]\n"))
	}
	code, out, diagnostic := run(t, "load", sources[0], sources[1], "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--ndjson")
	if code != 0 || diagnostic != "" {
		t.Fatalf("load --ndjson: %d %s %s", code, out, diagnostic)
	}
	seen := map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["event"] != "script-output" {
			continue
		}
		index, present := record["input_index"]
		if !present {
			t.Fatalf("script-output carries no input_index: %s", line)
		}
		text, _ := record["text"].(string)
		seen[strings.TrimSpace(text)] = index
	}
	if seen["first"] != float64(0) || seen["second"] != float64(1) {
		t.Fatalf("script output attributed to %v, want first=0 second=1", seen)
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

type publicationWriter struct {
	bytes.Buffer
	action   string
	event    string
	selected bool
	cancel   context.CancelFunc
}

func (w *publicationWriter) Write(data []byte) (int, error) {
	var record map[string]any
	if json.Unmarshal(data, &record) == nil && (record["results"] != nil || (w.event != "" && record["event"] == w.event)) {
		w.selected = true
	}
	if w.selected && w.action == "write" {
		return 0, io.ErrClosedPipe
	}
	if w.selected && w.action == "cancel" {
		w.cancel()
	}
	return w.Buffer.Write(data)
}

func (w *publicationWriter) Flush() error {
	if w.selected && w.action == "flush" {
		return io.ErrClosedPipe
	}
	return nil
}

func TestLoadPublicationRetainsResults(t *testing.T) {
	type testCase struct{ mode, action, event string }
	cases := []testCase{}
	for _, mode := range []string{"--json", "--ndjson"} {
		for _, action := range []string{"write", "flush", "cancel"} {
			cases = append(cases, testCase{mode, action, ""})
		}
	}
	cases = append(cases, testCase{"--ndjson", "write", "workspace-completed"})
	for _, test := range cases {
		t.Run(test.mode+"-"+test.action+"-"+test.event, func(t *testing.T) {
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
			dir := t.TempDir()
			path := write(t, dir, "workspace.yaml", "session_name: completed\nwindows:\n- panes: [blank]\n")
			args := []string{"load", "-S", server.SocketPath(), "-d", test.mode, path}
			if test.event != "" {
				args = append(args, write(t, dir, "later.yaml", "session_name: later\nwindows:\n- panes: [blank]\n"))
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			out := &publicationWriter{action: test.action, event: test.event, cancel: cancel}
			var diagnostic bytes.Buffer
			code := cli.Run(ctx, args, strings.NewReader(""), out, &diagnostic)
			wantCode, wantStatus := 1, "ok"
			if test.action == "cancel" {
				wantCode = 130
			}
			if test.event != "" {
				wantStatus = "partial"
			}
			var result struct {
				Code   string `json:"code"`
				Result struct {
					Status  string `json:"status"`
					Results []struct {
						SessionID string `json:"session_id"`
						Stage     string `json:"stage"`
					} `json:"results"`
				} `json:"result"`
			}
			if err := json.Unmarshal(diagnostic.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if code != wantCode || result.Result.Status != wantStatus || len(result.Result.Results) != 1 {
				t.Errorf("completed result lost: code=%d diagnostic=%s", code, diagnostic.String())
			} else if got := result.Result.Results[0]; got.SessionID != "$0" || got.Stage != "completed" {
				t.Errorf("incorrect retained result: %+v", got)
			}
			if test.action == "cancel" && result.Code != "interrupted" {
				t.Errorf("cancellation replaced: %+v", result)
			}
			snapshot, err := server.Snapshot(t.Context())
			if err != nil || len(snapshot.Sessions()) != 1 || len(snapshot.Windows()) != 1 || len(snapshot.Panes()) != 1 {
				t.Fatalf("completed topology changed or later input ran: %v sessions=%d windows=%d panes=%d", err, len(snapshot.Sessions()), len(snapshot.Windows()), len(snapshot.Panes()))
			}
			if snapshot.Sessions()[0].ID() != "$0" {
				t.Errorf("retained different session: %s", snapshot.Sessions()[0].ID())
			}
		})
	}
}

func TestLoadLogLevelsAndRegularFiles(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	for _, level := range []string{"info", "debug", "critical"} {
		path := write(t, dir, level+".yaml", "session_name: logged-"+level+"\nbefore_script: /bin/sh -c 'printf child-out; printf child-err >&2'\nwindows:\n- panes: [blank]\n")
		logPath := filepath.Join(dir, level+".log")
		if level == "info" {
			write(t, dir, level+".log", "retained\n")
			if err := os.Chmod(logPath, 0o640); err != nil {
				t.Fatal(err)
			}
		}
		code, out, diagnostic := run(t, "load", "-S", server.SocketPath(), "-d", "--json", "--log-level", level, "--log-file", logPath, path)
		if code != 0 || diagnostic != "" || !json.Valid([]byte(out)) || !strings.Contains(out, "child-out") || !strings.Contains(out, "child-err") {
			t.Fatalf("level=%s: %d %q %q", level, code, out, diagnostic)
		}
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		wantMode := os.FileMode(0o600)
		if level == "info" {
			wantMode = 0o640
			if !strings.HasPrefix(string(data), "retained\n") {
				t.Fatal("existing log content was replaced")
			}
			data = data[len("retained\n"):]
		}
		info, err := os.Stat(logPath)
		if err != nil || info.Mode().Perm() != wantMode {
			t.Fatalf("log permissions: %v %v", info, err)
		}
		if strings.Contains(string(data), "child-out") != (level == "debug") || strings.Contains(string(data), "child-err") != (level == "debug") {
			t.Errorf("level=%s leaked or lost child text: %s", level, data)
		}
		completed, streams := false, map[string]bool{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var record struct {
				Message string `json:"msg"`
				Data    struct {
					Stream string `json:"stream"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatalf("invalid log record: %q %v", line, err)
			}
			completed = completed || record.Message == "completed"
			if record.Message == "script-output" {
				streams[record.Data.Stream] = true
			}
		}
		if completed != (level != "critical") || (streams["stdout"] && streams["stderr"]) != (level == "debug") || (level == "critical" && len(data) != 0) {
			t.Errorf("level=%s completed=%t streams=%v log=%s", level, completed, streams, data)
		}
	}
}

// TestLoadFailureWarningReachesTheLogAtErrorLevel: a load-stage failure's
// warning must reach --log-file even at --log-level error, which drops an
// ordinary warning. Its slog level is escalated to Error so the file's own
// error-level threshold does not drop the record before it is written.
func TestLoadFailureWarningReachesTheLogAtErrorLevel(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	path := write(t, dir, "fail.yaml", "session_name: log-failure\nbefore_script: 'false'\nwindows:\n- panes: [blank]\n")
	logPath := filepath.Join(dir, "fail.log")
	code, _, _ := run(t, "load", path, "-S", server.SocketPath(), "-d", "--json", "--log-level", "error", "--log-file", logPath)
	if code != 1 {
		t.Fatalf("load with a failing before_script: %d", code)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record struct {
			Message string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid log record: %q %v", line, err)
		}
		found = found || record.Message == "warning"
	}
	if !found {
		t.Fatalf("--log-level error dropped the load failure: %s", data)
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

// TestNoLayoutChangeFollowsAPanesOwnCommand: a multi-pane window's layout
// must settle before that pane's own command runs, never after -- a resize
// that lands once a pane has already been sent its command races the
// shell's own prompt redraw and can leave a stray partial-line marker on
// screen. A shim tmux on PATH records every dispatched command; the last
// select-layout for the window must precede the last send-keys.
func TestNoLayoutChangeFollowsAPanesOwnCommand(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not on PATH")
	}
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	shimDir := t.TempDir()
	log := filepath.Join(shimDir, "argv.log")
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(log) + "\nexec " + shellQuote(realTmux) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := write(t, t.TempDir(), "multi.yaml", "session_name: multi\nwindows:\n- layout: main-vertical\n  panes: [echo A, echo B, echo C]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-d", "--json")
	if code != 0 || diagnostic != "" {
		t.Fatalf("load: %d %q %q", code, out, diagnostic)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var relevant []string
	lastLayout, lastSendKeys := -1, -1
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		// The library wraps every dispatched command in an if-shell daemon
		// identity check, so the subcommand name arrives quoted.
		isLayout, isSendKeys := strings.Contains(line, "'select-layout'"), strings.Contains(line, "'send-keys'")
		if isLayout || isSendKeys {
			relevant = append(relevant, line)
			if isLayout {
				lastLayout = len(relevant) - 1
			}
			if isSendKeys {
				lastSendKeys = len(relevant) - 1
			}
		}
	}
	if lastSendKeys < 0 {
		t.Fatalf("no send-keys command dispatched: %q", relevant)
	}
	if lastLayout > lastSendKeys {
		t.Fatalf("a layout change followed the last pane's command: %s", strings.Join(relevant, "\n"))
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestBeforeScriptDirectoryAndBorrowedSession(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	cwd := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "workspace files")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	explicitDir := t.TempDir()
	t.Chdir(cwd)
	script := write(t, configDir, "before.sh", "#!/bin/sh\npwd > \"$1\"\n")
	if err := os.Chmod(script, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, directory, want string }{{"inherited", "", cwd}, {"explicit", explicitDir, explicitDir}} {
		marker := filepath.Join(configDir, test.name+".cwd")
		config := "session_name: " + test.name + "\nbefore_script: './before.sh " + strconv.Quote(marker) + "'\nwindows:\n- panes: [blank]\n"
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
	path := write(t, configDir, "fail-append.yaml", "session_name: unused\nbefore_script: 'false'\nwindows:\n- panes: [blank]\n")
	code, _, _ := run(t, "load", path, "-S", server.SocketPath(), "--append", "--json")
	if code != 1 {
		t.Fatalf("expected script failure, got %d", code)
	}
	fresh, err := server.Snapshot(t.Context())
	if err != nil || len(fresh.Sessions()) != len(snapshot.Sessions()) {
		t.Fatalf("borrowed session lost: %v", err)
	}
}

// TestPaneDefaultDirectoryIsInvocationDirectory checks that with no
// start_directory, panes start in the invocation directory, as in tmuxp.
func TestPaneDefaultDirectoryIsInvocationDirectory(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	invocationDir := t.TempDir()
	configDir := t.TempDir()
	t.Chdir(invocationDir)
	path := write(t, configDir, "no-directory.yaml",
		"session_name: no-directory\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "-d", "--json")
	if code != 0 {
		t.Fatalf("load %d %s %s", code, out, diagnostic)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pane := snapshot.Panes()[0]
	got, ok := pane.CurrentPath()
	if !ok {
		t.Fatal("pane_current_path unavailable")
	}
	// Resolve both sides: the macOS runner's TempDir is a symlink
	// (/var -> /private/var) that tmux reports resolved.
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	wantResolved, err := filepath.EvalSymlinks(invocationDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("pane started in %q, want the invocation directory %q (not the workspace file's directory %q)",
			got, invocationDir, configDir)
	}
}

func TestNeutralExtensionMetadataStaysNative(t *testing.T) {
	for _, field := range []string{"plugins: []", "workspace_builder_paths: ['.']"} {
		for _, appendMode := range []bool{false, true} {
			t.Run(field+"/append="+strconv.FormatBool(appendMode), func(t *testing.T) {
				server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "retained"}})
				before, err := server.Snapshot(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				pythonMarker := filepath.Join(dir, "python-marker")
				t.Setenv("GO_EXTENSION_PYTHON_MARKER", pythonMarker)
				python := write(t, dir, "python", "#!/bin/sh\nprintf python > \"$GO_EXTENSION_PYTHON_MARKER\"\nexit 97\n")
				if err := os.Chmod(python, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TMUX_WORKSPACE_PYTHON", python)
				marker := filepath.Join(dir, "script-marker")
				script := write(t, dir, "before.sh", "printf native > \"$1\"\n")
				path := write(t, dir, "workspace.yaml", "session_name: native\nbefore_script: "+strconv.Quote("/bin/sh "+script+" "+marker)+"\nwindows: [{window_name: added, panes: [blank]}]\n"+field+"\n")
				args := []string{"load", path, "-S", server.SocketPath(), "-f", server.ConfigFile(), "--json"}
				wantSessions := 2
				if appendMode {
					t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
					t.Setenv("TMUX_PANE", before.Panes()[0].ID().String())
					args = append(args, "--append")
					wantSessions = 1
				} else {
					args = append(args, "-d")
				}
				code, out, diagnostic := run(t, args...)
				if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
					t.Errorf("native load: %d %s %s", code, out, diagnostic)
				}
				if got, err := os.ReadFile(marker); err != nil || string(got) != "native" {
					t.Errorf("native script: %q %v", got, err)
				}
				if _, err := os.Stat(pythonMarker); !os.IsNotExist(err) {
					t.Errorf("neutral metadata invoked Python: %v", err)
				}
				after, err := server.Snapshot(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if len(after.Sessions()) != wantSessions || len(after.Windows()) != 2 || len(after.Panes()) != 2 {
					t.Errorf("native topology: sessions=%d windows=%d panes=%d", len(after.Sessions()), len(after.Windows()), len(after.Panes()))
				}
				if _, err := after.SessionByID(before.Sessions()[0].ID()); err != nil {
					t.Errorf("borrowed session lost: %v", err)
				}
				if _, err := after.PaneByID(before.Panes()[0].ID()); err != nil {
					t.Errorf("borrowed pane lost: %v", err)
				}
			})
		}
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

// TestBridgeScriptFailureReportsScriptFailedCode: a plugin/builder document
// runs through the Python bridge subprocess, not a tmux command, so its
// nonzero exit must not report tmux_failed. A fake TMUX_WORKSPACE_PYTHON
// answers checkPython's plain "-c" version probe with success and only
// fails the real bridge invocation ("-u -c ..."), so this needs no
// installed tmuxp.
func TestBridgeScriptFailureReportsScriptFailedCode(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true})
	dir := t.TempDir()
	python := write(t, dir, "python", "#!/bin/sh\nif [ \"$1\" = -u ]; then exit 7; fi\nexit 0\n")
	if err := os.Chmod(python, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_WORKSPACE_PYTHON", python)
	path := write(t, dir, "plugin.yaml", "session_name: bridge-fail\nplugins: [fake.Plugin]\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "-d", "--json")
	if code != 1 || out == "" {
		t.Fatalf("bridge load with a failing python bridge: %d %q %q", code, out, diagnostic)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("invalid summary %q: %v", out, err)
	}
	errs, _ := summary["errors"].([]any)
	entry, _ := first(errs).(map[string]any)
	if len(errs) != 1 || entry["code"] != "script_failed" {
		t.Fatalf("errors[0].code = %v, want script_failed (a python bridge exit is not a tmux command failing): %s", entry["code"], out)
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
	t.Run("append", func(t *testing.T) {
		path := write(t, dir, "append.yaml", "session_name: append-target\nplugins: [native_plugin.Plugin]\nwindows:\n- panes: [blank]\n")
		code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "--append", "--json")
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
		if len(after.Sessions()) != 2 || len(after.Windows()) != 3 {
			t.Errorf("plugin append changed session ownership: sessions=%d windows=%d", len(after.Sessions()), len(after.Windows()))
		}
		retained, err := after.SessionByID(borrowed.ID())
		if err != nil {
			t.Fatal(err)
		}
		windows, _ := retained.Windows()
		if len(windows) != 2 {
			t.Errorf("borrowed session windows=%d", len(windows))
		}
		if _, err := after.PaneByID(target.ID()); err != nil {
			t.Errorf("borrowed pane lost: %v", err)
		}
	})
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
		if len(after.Sessions()) != 2 || len(after.Windows()) != 3 {
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

func TestScriptPublicationPreservesCleanup(t *testing.T) {
	for _, test := range []struct {
		appendMode bool
		status     int
	}{{false, 7}, {true, 7}, {false, 0}} {
		t.Run(strconv.FormatBool(test.appendMode)+"-status-"+strconv.Itoa(test.status), func(t *testing.T) {
			appendMode := test.appendMode
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "original"}})
			before, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			marker := filepath.Join(dir, "finished")
			t.Setenv("SCRIPT_COMPLETION_MARKER", marker)
			prior := write(t, dir, "prior.yaml", "session_name: prior\nwindows:\n- panes: [blank]\n")
			failed := write(t, dir, "failed.yaml", "session_name: failed\nbefore_script: /bin/sh -c 'printf finished > \"$SCRIPT_COMPLETION_MARKER\"; exit "+strconv.Itoa(test.status)+"'\nwindows:\n- panes: [blank]\n")
			args := []string{"load", "-S", server.SocketPath(), "--ndjson", prior, failed}
			if appendMode {
				t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
				t.Setenv("TMUX_PANE", before.Panes()[0].ID().String())
				args = append(args, "--append")
			} else {
				args = append(args, "-d")
			}
			out := &publicationWriter{action: "write", event: "script-completed"}
			var diagnostic bytes.Buffer
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			code := cli.Run(ctx, args, strings.NewReader(""), out, &diagnostic)
			var result struct {
				Result struct {
					Results []struct {
						SessionID string `json:"session_id"`
						Stage     string `json:"stage"`
					} `json:"results"`
					Scripts []struct {
						Result struct {
							Status int `json:"child_status"`
						} `json:"result"`
					} `json:"scripts"`
				} `json:"result"`
			}
			if err := json.Unmarshal(diagnostic.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			content, markerErr := os.ReadFile(marker)
			if code != 1 || markerErr != nil || string(content) != "finished" || len(result.Result.Scripts) != 1 || result.Result.Scripts[0].Result.Status != test.status {
				t.Fatalf("known child outcome lost: code=%d marker=%q err=%v diagnostic=%s", code, content, markerErr, diagnostic.String())
			}
			priorID, failedID, sessions := "$1", "$2", 2
			if appendMode {
				priorID, failedID, sessions = "$0", "$0", 1
			}
			if len(result.Result.Results) != 2 || result.Result.Results[0].SessionID != priorID || result.Result.Results[0].Stage != "completed" || result.Result.Results[1].SessionID != failedID || result.Result.Results[1].Stage != "failed" {
				t.Errorf("prior/failed input outcomes lost: %s", diagnostic.String())
			}
			after, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			windows := 2
			if !appendMode && test.status == 0 {
				sessions, windows = 3, 3
			}
			if len(after.Sessions()) != sessions || len(after.Windows()) != windows || len(after.Panes()) != windows {
				t.Errorf("cleanup damaged prior effects or retained failed owned session: sessions=%d windows=%d panes=%d", len(after.Sessions()), len(after.Windows()), len(after.Panes()))
			}
			originalRetained := false
			for _, window := range after.Windows() {
				originalRetained = originalRetained || window.ID() == before.Windows()[0].ID()
			}
			if !originalRetained {
				t.Error("original window was removed")
			}
			for _, session := range after.Sessions() {
				if name, _ := session.Name(); test.status != 0 && name == "failed" {
					t.Error("failed owned session survived publication failure")
				}
			}
		})
	}
}

func activeWindowID(t *testing.T, server tmux.Server, session string) string {
	t.Helper()
	result, err := server.Cmd(t.Context(), "display-message", "-p", "-t", session, "#{window_id}")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("active window of %s: %+v %v", session, result, err)
	}
	return strings.TrimSpace(string(result.RawStdout))
}

// TestAppendDoesNotMoveTheClientUnlessFocused: appending windows to the
// current session must not change which window is active unless an
// appended window sets focus: true. The undocumented default that leaks in
// otherwise is the first-window-wins default for a fresh load reaching
// into a session the user already owns.
func TestAppendDoesNotMoveTheClientUnlessFocused(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "owned"},
	})
	before, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	original := activeWindowID(t, server, "owned")
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
	t.Setenv("TMUX_PANE", before.Panes()[0].ID().String())

	dir := t.TempDir()
	unfocused := write(t, dir, "unfocused.yaml", "session_name: ignored\nwindows:\n- window_name: one\n  panes: [blank]\n- window_name: two\n  panes: [blank]\n")
	code, out, diagnostic := run(t, "load", unfocused, "-S", server.SocketPath(), "--append", "--json")
	if code != 0 {
		t.Fatalf("append: %d %s %s", code, out, diagnostic)
	}
	if got := activeWindowID(t, server, "owned"); got != original {
		t.Fatalf("append without focus moved the client: %s, want %s", got, original)
	}

	focused := write(t, dir, "focused.yaml", "session_name: ignored\nwindows:\n- window_name: three\n  panes: [blank]\n- window_name: four\n  focus: true\n  panes: [blank]\n")
	code, out, diagnostic = run(t, "load", focused, "-S", server.SocketPath(), "--append", "--json")
	if code != 0 {
		t.Fatalf("append: %d %s %s", code, out, diagnostic)
	}
	after, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var wantID string
	for _, window := range after.Windows() {
		if name, _ := window.Name(); name == "four" {
			wantID = window.ID().String()
		}
	}
	if wantID == "" {
		t.Fatal("appended window \"four\" not found")
	}
	if got := activeWindowID(t, server, "owned"); got != wantID {
		t.Fatalf("append with focus: true did not move the client: %s, want %s", got, wantID)
	}
}

// TestAppendHumanSummaryNamesTheAppendedSession: appending into the current
// session reports "Appended <session>", naming the session that received
// the windows rather than the workspace document's own session name.
func TestAppendHumanSummaryNamesTheAppendedSession(t *testing.T) {
	server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
		FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "home"},
	})
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
	t.Setenv("TMUX_PANE", snapshot.Panes()[0].ID().String())
	path := write(t, t.TempDir(), "unused.yaml", "session_name: ctxw\nwindows:\n- panes: [blank]\n")
	code, out, diagnostic := run(t, "load", path, "-S", server.SocketPath(), "--append", "-y")
	if code != 0 || diagnostic != "" {
		t.Fatalf("append: %d %q %q", code, out, diagnostic)
	}
	if !strings.Contains(out, "Appended home") {
		t.Fatalf("summary does not name the appended session: %q", out)
	}
	if strings.Contains(out, "ctxw") {
		t.Fatalf("summary names the workspace's own session name: %q", out)
	}
}

// TestDetachedBeatsAppendBuildsANewSession: -d --append builds a new
// detached session and ignores --append, both inside and outside tmux,
// matching tmuxp.
func TestDetachedBeatsAppendBuildsANewSession(t *testing.T) {
	for _, insideTmux := range []bool{false, true} {
		t.Run(strconv.FormatBool(insideTmux), func(t *testing.T) {
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{
				FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "home"},
			})
			args := []string{"load", "-S", server.SocketPath(), "-d", "--append"}
			if insideTmux {
				snapshot, err := server.Snapshot(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("TMUX", server.SocketPath()+","+daemonPID(t, server)+",0")
				t.Setenv("TMUX_PANE", snapshot.Panes()[0].ID().String())
			}
			path := write(t, t.TempDir(), "detached.yaml", "session_name: detached-wins\nwindows:\n- panes: [blank]\n")
			code, out, diagnostic := run(t, append(args, path)...)
			if code != 0 || diagnostic != "" {
				t.Fatalf("-d --append: %d %q %q", code, out, diagnostic)
			}
			snapshot, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Sessions()) != 2 {
				t.Fatalf("-d --append did not build a separate session: sessions=%d", len(snapshot.Sessions()))
			}
			home, err := snapshot.SessionByID(mustSessionID(t, snapshot, "home"))
			if err != nil {
				t.Fatal(err)
			}
			windows, _ := home.Windows()
			if len(windows) != 1 {
				t.Fatalf("-d --append appended into the current session: home windows=%d", len(windows))
			}
		})
	}
}

func mustSessionID(t *testing.T, snapshot tmux.Snapshot, name string) tmux.SessionID {
	t.Helper()
	for _, session := range snapshot.Sessions() {
		if sessionName, _ := session.Name(); sessionName == name {
			return session.ID()
		}
	}
	t.Fatalf("session %q not found", name)
	return ""
}
