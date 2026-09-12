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

func TestMissingPanesCreatesBlankPane(t *testing.T) {
	plan, err := normalize(document{"session_name": "minimal", "windows": []any{document{"window_name": "shell"}}}, t.TempDir())
	if err != nil || len(plan.Windows) != 1 || len(plan.Windows[0].Panes) != 1 {
		t.Fatalf("missing panes: %+v %v", plan, err)
	}
}
