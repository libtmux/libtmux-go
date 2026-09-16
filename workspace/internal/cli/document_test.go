package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRejectsUnknownExecutionFields(t *testing.T) {
	for _, test := range []struct {
		name, fields, scope, key string
	}{
		{"root-typo", `"before_scrip":"ignored"`, "workspace", "before_scrip"},
		{"root-misplaced", `"enter":false`, "workspace", "enter"},
		{"root-unknown-null", `"unexpected":null`, "workspace", "unexpected"},
		{"root-ignored-config", `"config":"other.conf"`, "workspace", "config"},
		{"root-ignored-socket", `"socket_name":"other"`, "workspace", "socket_name"},
		{"window-typo", `"windows":[{"shell_command_befor":"ignored","panes":[null]}]`, "window 0", "shell_command_befor"},
		{"pane-typo", `"windows":[{"panes":[{"shell_commmand":"ignored"}]}]`, "window 0 pane 0", "shell_commmand"}, //nolint:misspell // Deliberately misspelled execution key.
		{"pane-misplaced", `"windows":[{"panes":[{"options":{"synchronize-panes":true}}]}]`, "window 0 pane 0", "options"},
		{"command-typo", `"windows":[{"panes":[{"shell_command":[{"cmd":"run","entter":false}]}]}]`, "command 0", "entter"},
		{"command-misplaced", `"shell_command_before":[{"cmd":"run","suppress_history":false}]`, "command 0", "suppress_history"},
		{"command-metadata", `"shell_command_before":[{"cmd":"run","description":"ignored"}]`, "command 0", "description"},
		{"catalog-typo", `"workspace_builder_options":{"pane_readines":"never"}`, "workspace_builder_options", "pane_readines"},
		{"catalog-metadata", `"workspace_builder_options":{"description":"ignored"}`, "workspace_builder_options", "description"},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := document{"session_name": "example", "windows": []any{document{"panes": []any{nil}}}}
			if err := json.Unmarshal([]byte("{"+test.fields+"}"), &doc); err != nil {
				t.Fatal(err)
			}
			_, err := normalize(doc, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), test.scope) || !strings.Contains(err.Error(), `unknown field "`+test.key+`"`) {
				t.Fatalf("normalization must identify %s %s: %v", test.scope, test.key, err)
			}
		})
	}
}

func TestNativeMetadataAndExtensionFieldBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, extra string
		bridge      bool
	}{
		{"native", `"plugins":[],"workspace_builder_paths":["."]`, false},
		{"plugin", `"plugins":["example.Plugin"],"extension_setting":{"mode":"custom"}`, true},
		{"builder", `"workspace_builder":"example.Builder","extension_setting":{"mode":"custom"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := document{}
			content := `{"session_name":"example","description":"workspace metadata",` + test.extra + `,
			"environment":{"CUSTOM_VALUE":"present"},"options":{"@custom":"value"},
			"workspace_builder_options":{"pane_readiness":"never"},
			"windows":[{"description":{"note":"window metadata"},"options":{"@window":"value"},
			"panes":[{"description":["pane metadata"],"shell_command":[{"cmd":"echo ready","enter":false}]}]}]}`
			if err := json.Unmarshal([]byte(content), &doc); err != nil {
				t.Fatal(err)
			}
			if test.bridge {
				window := mapping(array(doc["windows"])[0])
				pane := mapping(array(window["panes"])[0])
				window["extension_setting"] = true
				pane["extension_setting"] = true
				mapping(array(pane["shell_command"])[0])["extension_setting"] = true
				mapping(doc["workspace_builder_options"])["extension_setting"] = true
			}
			before, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := normalize(doc, t.TempDir())
			if err != nil || plan.Bridge != test.bridge || plan.Readiness != "never" {
				t.Fatalf("normalization = %+v, %v", plan, err)
			}
			if plan.Windows[0].Panes[0].Commands[0].Enter || plan.Environment["CUSTOM_VALUE"] != "present" || plan.Options["@custom"] != "value" {
				t.Fatalf("supported controls changed: %+v", plan)
			}
			after, err := json.Marshal(doc)
			if err != nil || string(after) != string(before) {
				t.Fatalf("normalization changed source metadata: %s, %v", after, err)
			}
		})
	}
}

func TestUnknownExecutionFieldPrecedesBackendAndLog(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	last := filepath.Join(dir, "last.json")
	for path, content := range map[string]string{
		first: `{"session_name":"first","before_script":"touch should-not-run","windows":[{"panes":[null]}]}`,
		last:  `{"session_name":"last","windows":[{"panes":[{"shell_command":[{"cmd":"run","entter":false}]}]}]}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "log.jsonl")
	code, out, diagnostic := invoke(t, "load", "-d", "--json", "--log-file", log, first, last)
	if code != 1 || out != "" || !strings.Contains(diagnostic, "entter") || strings.Contains(diagnostic, "executable") {
		t.Fatalf("unknown execution key reached backend: %d %q %q", code, out, diagnostic)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("preflight opened the log: %v", err)
	}
}

func TestUnknownExecutionFieldOrderIsStable(t *testing.T) {
	for range 20 {
		_, err := normalize(document{"session_name": "example", "zzzz": false, "aaaa": false}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), `unknown field "aaaa"`) {
			t.Fatalf("unknown fields must use deterministic key order: %v", err)
		}
	}
}

// TestNormalizePanesEmptySequence covers D3: "panes: []" must build exactly
// like an omitted panes key -- one pane with no command -- not be refused.
func TestNormalizePanesEmptySequence(t *testing.T) {
	doc := document{"session_name": "example", "windows": []any{document{"panes": []any{}}}}
	plan, err := normalize(doc, t.TempDir())
	if err != nil {
		t.Fatalf("panes: [] must build like an omitted panes key: %v", err)
	}
	if len(plan.Windows) != 1 || len(plan.Windows[0].Panes) != 1 || len(plan.Windows[0].Panes[0].Commands) != 0 {
		t.Fatalf("panes: [] = %+v, want one pane with no command", plan.Windows)
	}
}

// TestNormalizePanesWrongTypeStillRefused is D3's negative case: a panes
// value that is present but not a sequence at all is still an error.
func TestNormalizePanesWrongTypeStillRefused(t *testing.T) {
	doc := document{"session_name": "example", "windows": []any{document{"panes": "not-a-sequence"}}}
	_, err := normalize(doc, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "panes must be a nonempty sequence") {
		t.Fatalf("panes: \"not-a-sequence\" must still be refused: %v", err)
	}
}
