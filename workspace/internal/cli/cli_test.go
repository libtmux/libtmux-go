package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
