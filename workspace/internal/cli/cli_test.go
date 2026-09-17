package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &out, &diagnostic)
	return code, out.String(), diagnostic.String()
}

func TestInvalidInvocationPrecedesBackend(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"--json"},
		{"--json=1", "import", "teamocil"},
		{"--ndjson=TRUE", "load"},
		{"--json", "import", "teamocil"},
		{"import", "tmuxinator", "--ndjson"},
		{"load", "x", "-2", "-8", "--json"},
		{"load", "x", "--unknown", "--json"},
		{"--json", "search"},
		{"--json", "search", "-f", "missing", "x"},
		{"--color", "invalid", "ls", "--json"},
		{"freeze", "-f", "xml", "--json"},
		{"shell", "--pdb", "--code", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, out, diagnostic := invoke(t, args...)
			if code != 2 || out != "" || !json.Valid([]byte(diagnostic)) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, diagnostic)
			}
			if strings.Contains(diagnostic, "executable") {
				t.Fatal("backend was reached before usage validation")
			}
		})
	}
}

func TestJSONExtensionUsesJSONSyntax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte("session_name: yaml\nwindows: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDocument(path); err == nil {
		t.Fatal("YAML content accepted in a JSON document")
	}
}

func TestMachineCompletionEnvelope(t *testing.T) {
	code, out, diagnostic := invoke(t, "--json", "--generate-completion", "bash")
	if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
		t.Fatalf("machine completion: %d %q %q", code, out, diagnostic)
	}
}

// TestVersionReportsItsOwnVersion checks --version prints "tmux-workspace
// <version>" with go's own version, as every port does.
func TestVersionReportsItsOwnVersion(t *testing.T) {
	code, out, diagnostic := invoke(t, "--version")
	if code != 0 || diagnostic != "" {
		t.Fatalf("--version: %d %q %q", code, out, diagnostic)
	}
	first, _, _ := strings.Cut(out, "\n")
	if first != "tmux-workspace "+Version {
		t.Fatalf("--version first line = %q, want %q", first, "tmux-workspace "+Version)
	}
	if strings.Contains(first, "(Go)") {
		t.Fatalf("--version first line still carries the port label: %q", first)
	}
	// cxx, java, rs and swift all print a bare semantic version. Go build
	// metadata is always v-prefixed, so without trimming it this port is the
	// only one that reads "v0.0.1" where its siblings read "0.0.1".
	if !regexp.MustCompile(`^tmux-workspace [0-9]+\.[0-9]+\.[0-9]+`).MatchString(first) {
		t.Fatalf("--version first line = %q, want a bare tmux-workspace <major>.<minor>.<patch>", first)
	}
	code, machine, diagnostic := invoke(t, "--version", "--json")
	if code != 0 || diagnostic != "" {
		t.Fatalf("--version --json: %d %q %q", code, machine, diagnostic)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(machine), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["version"] != Version {
		t.Fatalf("--version --json version = %v, want %q", envelope["version"], Version)
	}
}

func TestInactiveProgressEnvironmentDoesNotRejectLoad(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	for _, mode := range []string{"", "--json", "--ndjson"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"load", "-d", missing}
			if mode != "" {
				args = append(args, mode)
			}
			t.Setenv("TMUXP_PROGRESS_LINES", "")
			wantCode, wantOut, wantError := invoke(t, args...)
			for _, invalid := range []string{"not-an-integer", "-2"} {
				t.Setenv("TMUXP_PROGRESS_LINES", invalid)
				code, out, diagnostic := invoke(t, args...)
				if code != wantCode || out != wantOut || diagnostic != wantError {
					t.Errorf("inactive progress %q: got %d %q %q; want %d %q %q", invalid, code, out, diagnostic, wantCode, wantOut, wantError)
				}
			}
		})
	}
}

func TestExplicitInvalidProgressLinesStillRejectLoad(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, out, diagnostic := invoke(t, "load", "-d", "--json", "--progress-lines=-2", "absent.yaml")
	if code != 2 || out != "" || !strings.Contains(diagnostic, "progress-lines must") {
		t.Fatalf("explicit progress validation: %d %q %q", code, out, diagnostic)
	}
}

func TestInvalidSizePrecedesWorkspaceAndBackend(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUXP_DEFAULT_COLUMNS", "invalid")
	code, out, diagnostic := invoke(t, "load", "-d", "--json", "absent.yaml")
	if code != 2 || out != "" || !strings.Contains(diagnostic, "TMUXP_DEFAULT_COLUMNS") {
		t.Fatalf("size validation: %d %q %q", code, out, diagnostic)
	}
}

func TestLegacyColorsPrecedeWorkspaceAndBackend(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, flag := range []string{"-8", "--88-colors"} {
		for _, mode := range []string{"", "--json", "--ndjson"} {
			args := []string{"load", "absent.yaml", "also-absent.yaml", "-d", flag}
			if mode != "" {
				args = append(args, mode)
			}
			code, out, diagnostic := invoke(t, args...)
			if code != 2 || out != "" || !strings.Contains(diagnostic, "88-color") || !strings.Contains(diagnostic, "3.2a+") || (mode != "" && !json.Valid([]byte(diagnostic))) {
				t.Errorf("legacy colors %v: %d %q %q", args, code, out, diagnostic)
			}
		}
	}
}

func TestMalformedBeforeScriptPrecedesBackend(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	for _, script := range []string{"printf 'unterminated", "   "} {
		doc := document{"session_name": "invalid", "before_script": script, "windows": []any{document{"panes": []any{nil}}}}
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "invalid.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, diagnostic := invoke(t, "load", path, "-d", "--json")
		if code != 1 || out != "" || !json.Valid([]byte(diagnostic)) || !strings.Contains(diagnostic, "before_script") {
			t.Errorf("script %q reached backend: %d %q %q", script, code, out, diagnostic)
		}
	}
}

func TestMalformedLayoutInLaterInputPrecedesBackend(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	for path, content := range map[string]string{
		first:  `{"session_name":"first","windows":[{"panes":[null]}]}`,
		second: `{"session_name":"second","windows":[{"layout":"32d2,80x24,0,0{}","panes":[null]}]}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, out, diagnostic := invoke(t, "load", "-d", "--json", first, second)
	if code == 0 || out != "" || !strings.Contains(diagnostic, "layout") || strings.Contains(diagnostic, "executable") {
		t.Fatalf("layout must fail before backend resolution: %d %q %q", code, out, diagnostic)
	}
}

func TestBridgeAppendScriptPrecedesRuntime(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUX_WORKSPACE_PYTHON", filepath.Join(t.TempDir(), "missing-python"))
	for field, value := range map[string]string{"plugins": `["example.Plugin"]`, "workspace_builder": `"example.Builder"`} {
		path := filepath.Join(t.TempDir(), "unsafe.json")
		content := `{"session_name":"unsafe","before_script":"/bin/false","windows":[{"panes":[null]}],"` + field + `":` + value + `}`
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, diagnostic := invoke(t, "load", path, "--append", "--json")
		if code != 2 || out != "" || !strings.Contains(diagnostic, "unsupported_combination") || !strings.Contains(diagnostic, "before_script") {
			t.Errorf("%s reached runtime: %d %q %q", field, code, out, diagnostic)
		}
	}
}

func TestAppendResolvesItsServerBeforeThePreflight(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	directory := t.TempDir()
	path := filepath.Join(directory, "ambiguous.yaml")
	// An abbreviation whose meaning depends on the tmux version is what sends
	// the preflight to a daemon for an answer.
	if err := os.WriteFile(path, []byte("session_name: ambiguous\nwindows:\n- layout: main-h\n  panes: [blank, blank]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, diagnostic := invoke(t, "load", "--append", "-d", "-S", filepath.Join(directory, "absent.sock"), path)
	if code != 2 || out != "" || !strings.Contains(diagnostic, "--append requires TMUX") {
		t.Fatalf("append resolution: %d %q %q", code, out, diagnostic)
	}
}

func TestHelpWithoutTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, path := range []string{"", "load", "ls", "search", "edit", "freeze", "convert", "import", "import teamocil", "import tmuxinator", "shell", "debug-info"} {
		args := append(strings.Fields(path), "--help")
		code, out, diagnostic := invoke(t, args...)
		if code != 0 || !strings.Contains(out, "Usage:") || diagnostic != "" {
			t.Fatalf("%v: %d %q %q", args, code, out, diagnostic)
		}
	}
}

// TestShellHelpDocumentsThePythonOverride checks --help names
// TMUX_WORKSPACE_PYTHON, the override every bridge failure recommends.
func TestShellHelpDocumentsThePythonOverride(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, out, diagnostic := invoke(t, "shell", "--help")
	if code != 0 || diagnostic != "" {
		t.Fatalf("shell --help: %d %q %q", code, out, diagnostic)
	}
	if !strings.Contains(out, "TMUX_WORKSPACE_PYTHON") {
		t.Fatalf("shell --help does not mention TMUX_WORKSPACE_PYTHON: %q", out)
	}
}

func TestConvertPreservesUnknownDocumentAndProtectsFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "workspace.yaml")
	if err := os.WriteFile(source, []byte("session_name: demo\nplugins: [example.Plugin]\nwindows: []\ncustom: {answer: 42}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, diagnostic := invoke(t, "convert", source, "--json")
	if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" || !strings.Contains(out, "answer") {
		t.Fatalf("%d %q %q", code, out, diagnostic)
	}
	destination := filepath.Join(dir, "workspace.json")
	if err := os.WriteFile(destination, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, diagnostic = invoke(t, "convert", source, "--save-to", destination, "--yes", "--json")
	content, err := os.ReadFile(destination)
	if err != nil || code != 1 || string(content) != "untouched" || !json.Valid([]byte(diagnostic)) {
		t.Fatalf("overwrite protection: %d %q %q %v", code, content, diagnostic, err)
	}
}

func TestPublishedWorkspaceCarriesTheExpectedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.yaml")
	if err := atomicWrite(path, []byte("session_name: first\n"), false); err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o666) &^ processUmask()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want {
		t.Fatalf("new workspace mode %v; want %v (%v)", info.Mode().Perm(), want, err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("session_name: second\n"), true); err != nil {
		t.Fatal(err)
	}
	if info, err = os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("replacement changed the mode to %v (%v)", info.Mode().Perm(), err)
	}
}

func TestHomeExpansionKeepsTrailingElements(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WORKSPACE_LEAF", "leaf")
	for _, test := range []struct{ value, want string }{
		{"~", home},
		{"~/", home},
		{"~/projects", filepath.Join(home, "projects")},
		{"~/projects/~", filepath.Join(home, "projects", "~")},
		{"~/~", filepath.Join(home, "~")},
		{"~/projects/$WORKSPACE_LEAF", filepath.Join(home, "projects", "leaf")},
		{"relative/~", "relative/~"},
	} {
		if got := expand(test.value); got != test.want {
			t.Errorf("expand(%q) = %q; want %q", test.value, got, test.want)
		}
	}
}

func TestCaptureDestinationStaysInTheWorkspaceDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".tmuxp")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := func(name string) error {
		r := &invocation{ctx: t.Context(), in: strings.NewReader(""), out: io.Discard, err: io.Discard}
		return r.documentResult(&options{yes: true, quiet: true}, document{"session_name": name}, "", "yaml", nil)
	}
	for _, name := range []string{"", ".", "..", "../../escape", "nested/name", `back\slash`, "title\x1b]2;x\a"} {
		var specific *failure
		if err := capture(name); !errors.As(err, &specific) || specific.Code != "unsafe_destination" {
			t.Errorf("session name %q derived a destination: %v", name, err)
		}
	}
	if err := capture("$HOME"); err != nil {
		t.Fatal(err)
	}
	if !isFile(filepath.Join(directory, "$HOME.yaml")) {
		t.Error("a plain session name did not publish into the workspace directory")
	}
}

func TestDocumentRejectsTrailingYAML(t *testing.T) {
	_, err := decodeDocument([]byte("session_name: first\n---\nsession_name: second\n"))
	if err == nil {
		t.Fatal("accepted a silently discarded YAML document")
	}
}

func TestNormalizeShorthandAndCommandState(t *testing.T) {
	doc, err := decodeDocument([]byte("session_name: example\nshell_command_before: echo root\nwindows:\n- window_shell: /bin/sh\n  panes:\n  - pane\n  - [echo one, echo two]\n  - shell_command:\n    - {cmd: first, sleep_after: 0.2, enter: false}\n    - second\n    - {cmd: third, sleep_after: 0, enter: true}\n    - fourth\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := normalize(doc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	panes := plan.Windows[0].Panes
	if len(panes[0].Commands) != 1 || len(panes[1].Commands) != 3 || panes[1].Shell != "/bin/sh" || !panes[1].SuppressHistory {
		t.Fatalf("bad shorthand/inheritance: %+v", panes)
	}
	commands := panes[2].Commands
	if commands[2].Enter || commands[2].SleepAfter == 0 || !commands[4].Enter || commands[4].SleepAfter != 0 {
		t.Fatalf("command state did not carry/reset: %+v", commands)
	}
}

func TestNormalizeExtensionSelection(t *testing.T) {
	for _, test := range []struct {
		name, field string
		bridge      bool
	}{
		{"ordinary", "", false},
		{"empty-plugins", "plugins: []", false},
		{"builder-paths", "workspace_builder_paths: ['.']", false},
		{"plugin", "plugins: [example.Plugin]", true},
		{"builder", "workspace_builder: example.Builder\nworkspace_builder_paths: ['.']", true},
		{"malformed-plugins", "plugins: invalid", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc, err := decodeDocument([]byte("session_name: example\nbefore_script: /bin/echo native\nwindows: [{panes: [blank]}]\n" + test.field + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			plan, err := normalize(doc, t.TempDir())
			if err != nil || plan.Bridge != test.bridge {
				t.Fatalf("bridge=%t want=%t: %v", plan.Bridge, test.bridge, err)
			}
			if !test.bridge && (len(plan.BeforeScript) != 2 || plan.BeforeScript[0] != "/bin/echo" || plan.BeforeScript[1] != "native") {
				t.Fatalf("native before_script was not normalized: %v", plan.BeforeScript)
			}
		})
	}
}

func TestMissingPanesCreatesBlankPane(t *testing.T) {
	plan, err := normalize(document{"session_name": "minimal", "windows": []any{document{"window_name": "shell"}}}, t.TempDir())
	if err != nil || len(plan.Windows) != 1 || len(plan.Windows[0].Panes) != 1 {
		t.Fatalf("missing panes: %+v %v", plan, err)
	}
}

// TestPaneReadinessTimeoutIsQuietByDefault covers the pane-readiness
// question: tmuxp's own _wait_for_pane_ready logs a missed deadline at debug
// level, never as a user-facing warning, because concurrent pane creation
// occasionally outrunning a 2-second shell-prompt wait is expected, not a
// misconfiguration. pane_readiness_timeout now matches that by requiring
// --log-level info or debug before it reaches NDJSON or stderr; an unrelated
// warning code stays visible at the default level.
func TestPaneReadinessTimeoutIsQuietByDefault(t *testing.T) {
	for _, test := range []struct {
		name, logLevel string
		wantRecord     bool
	}{
		{"default", "", false},
		{"warning", "warning", false},
		{"info", "info", true},
		{"debug", "debug", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			r := &invocation{ctx: t.Context(), out: &out, err: io.Discard, ndjson: true, command: "load", logLevel: test.logLevel}
			data := map[string]any{"code": "pane_readiness_timeout", "message": "pane prompt did not move the cursor within two seconds"}
			if err := r.event("warning", data); err != nil {
				t.Fatal(err)
			}
			if got := out.Len() > 0; got != test.wantRecord {
				t.Fatalf("logLevel=%q wrote a record=%v, want %v: %q", test.logLevel, got, test.wantRecord, out.String())
			}
		})
	}
	var out bytes.Buffer
	r := &invocation{ctx: t.Context(), out: &out, err: io.Discard, ndjson: true, command: "load"}
	data := map[string]any{"code": "some_other_condition", "message": "unrelated"}
	if err := r.event("warning", data); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("an unrelated warning must stay visible at the default log level")
	}
}

// errorCode decodes the {"code":...} envelope --json/--ndjson write to
// stderr on failure.
func errorCode(t *testing.T, diagnostic string) string {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
		t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
	}
	code, _ := envelope["code"].(string)
	return code
}

// TestLoadErrorCodes checks the machine error codes that need no live tmux
// server; session_not_found is covered in the integration package.
func TestLoadErrorCodes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	tests := []struct {
		name, content, code string
		named               bool
	}{
		{"missing-file", "", "workspace_not_found", true},
		{"malformed-document", "a: [\n", "invalid_workspace", false},
		// The outer loop wraps normalize's error with the failing input's
		// path (privatePath(path)+": "+message); that prefix must survive
		// turning the inner error into a *failure, not just get replaced by
		// its bare, unprefixed message.
		{"unsupported-key", "session_name: x\nbogus: 1\nwindows:\n- panes: [null]\n", "unsupported_key", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, test.name+".yaml")
			if test.content != "" {
				if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			code, out, diagnostic := invoke(t, "load", "-d", "--json", path)
			if code != 1 || out != "" {
				t.Fatalf("%s: %d %q %q", test.name, code, out, diagnostic)
			}
			if got := errorCode(t, diagnostic); got != test.code {
				t.Fatalf("%s: code = %q, want %q (%s)", test.name, got, test.code, diagnostic)
			}
			if test.named && !strings.Contains(diagnostic, filepath.Base(path)) {
				t.Fatalf("%s: error dropped the path: %s", test.name, diagnostic)
			}
		})
	}
}

// TestErrorEnvelopeCarriesSchemaVersion: every stderr error record is
// {"schema_version":1,"code":...,"message":...}, not just
// {"code":...,"message":...}.
func TestErrorEnvelopeCarriesSchemaVersion(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, diagnostic := invoke(t, "load", "-d", "--json", filepath.Join(t.TempDir(), "missing.yaml"))
	var envelope map[string]any
	if err := json.Unmarshal([]byte(diagnostic), &envelope); err != nil {
		t.Fatalf("invalid error envelope %q: %v", diagnostic, err)
	}
	if version, ok := envelope["schema_version"].(float64); !ok || version != 1 {
		t.Fatalf("schema_version = %v, want 1 (%s)", envelope["schema_version"], diagnostic)
	}
}

// TestLoadReportsTmuxUnavailableWhenExecutableMissing: a missing tmux
// executable must report tmux_unavailable, not the generic operation_failed
// serverFor's raw "resolve tmux executable" error fell through to.
func TestLoadReportsTmuxUnavailableWhenExecutableMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.yaml")
	if err := os.WriteFile(path, []byte("session_name: ok\nwindows:\n- panes: [blank]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, diagnostic := invoke(t, "load", "-d", "--yes", "--json", path)
	if code != 1 || out != "" {
		t.Fatalf("%d %q %q", code, out, diagnostic)
	}
	if got := errorCode(t, diagnostic); got != "tmux_unavailable" {
		t.Fatalf("code = %q, want tmux_unavailable (%s)", got, diagnostic)
	}
}

// TestPromptClassifiesUnansweredInputAsConfirmationRequired: a confirmation
// that is needed but impossible -- no terminal, no answer on stdin -- must
// report confirmation_required. Every command that prompts routes through
// this one method, so it is tested directly rather than through a specific
// command.
func TestPromptClassifiesUnansweredInputAsConfirmationRequired(t *testing.T) {
	var out bytes.Buffer
	r := &invocation{ctx: t.Context(), in: strings.NewReader(""), err: &out}
	_, err := r.prompt("Freeze session (y/n)", "n")
	var specific *failure
	if !errors.As(err, &specific) || specific.Code != "confirmation_required" {
		t.Fatalf("prompt without input = %v, want confirmation_required", err)
	}
}
