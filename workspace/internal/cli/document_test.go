package cli

import (
	"encoding/json"
	"errors"
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

// TestNormalizeAcceptsExtensionKeysAtEveryLevel: a key starting with "x-",
// at any level, is inert -- accepted and ignored at load, not a refusal. A
// top-level anchor holder such as "x-pane-defaults: &shell" is the common
// tmuxp pattern this unblocks.
func TestNormalizeAcceptsExtensionKeysAtEveryLevel(t *testing.T) {
	content := `{
		"session_name": "example", "x-pane-defaults": {"shell_command_before": ["export QA_ANCHOR=1"]},
		"workspace_builder_options": {"pane_readiness": "never", "x-note": "ignored"},
		"windows": [{
			"x-window-note": "ignored",
			"panes": [{"x-pane-note": "ignored", "shell_command": [{"cmd": "run", "x-command-note": "ignored"}]}]
		}]
	}`
	var doc document
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatal(err)
	}
	plan, err := normalize(doc, t.TempDir())
	if err != nil {
		t.Fatalf("x- prefixed keys must be inert at every level: %v", err)
	}
	if len(plan.Windows) != 1 || len(plan.Windows[0].Panes) != 1 || plan.Windows[0].Panes[0].Commands[0].Text != "run" {
		t.Fatalf("extension keys changed the build: %+v", plan.Windows)
	}
}

// TestNormalizeStillRejectsOrdinaryUnknownKeysAndSuggestsXPrefix is the
// negative case: a key that does not start with "x-" is still refused, and
// the refusal names the "x-" escape hatch.
func TestNormalizeStillRejectsOrdinaryUnknownKeysAndSuggestsXPrefix(t *testing.T) {
	doc := document{"session_name": "example", "bogus": 1, "windows": []any{document{"panes": []any{nil}}}}
	_, err := normalize(doc, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), `unknown field "bogus"`) || !strings.Contains(err.Error(), "x-") {
		t.Fatalf("unknown field must still be refused and suggest x-: %v", err)
	}
}

// TestNormalizePanesEmptySequence: "panes: []" must build exactly like an
// omitted panes key -- one pane with no command -- not be refused.
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

// TestNormalizePanesWrongTypeStillRefused is the negative case: a panes
// value that is present but not a sequence at all is still an error.
func TestNormalizePanesWrongTypeStillRefused(t *testing.T) {
	doc := document{"session_name": "example", "windows": []any{document{"panes": "not-a-sequence"}}}
	_, err := normalize(doc, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "panes must be a nonempty sequence") {
		t.Fatalf("panes: \"not-a-sequence\" must still be refused: %v", err)
	}
}

// TestNormalizeRefusalsCarryTheSharedDocumentCode: every refusal from
// normalize is a defect in the document, so machine output classifies it as
// invalid_workspace. An unknown key keeps its own more specific code.
func TestNormalizeRefusalsCarryTheSharedDocumentCode(t *testing.T) {
	for _, test := range []struct{ name, fields, code string }{
		{"layout", `"windows":[{"layout":"definitely-not-a-layout","panes":[null]}]`, "invalid_workspace"},
		{"session-name", `"session_name":"my.proj"`, "invalid_workspace"},
		{"window-index", `"windows":[{"window_index":-1,"panes":[null]}]`, "invalid_workspace"},
		{"start-directory", `"start_directory":[]`, "invalid_workspace"},
		{"unknown-key", `"before_scrip":"ignored"`, "unsupported_key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := document{"session_name": "example", "windows": []any{document{"panes": []any{nil}}}}
			if err := json.Unmarshal([]byte("{"+test.fields+"}"), &doc); err != nil {
				t.Fatal(err)
			}
			var specific *failure
			_, err := normalize(doc, t.TempDir())
			if !errors.As(err, &specific) || specific.Code != test.code || specific.Exit != 1 {
				t.Fatalf("normalize = %v, want code %s exit 1", err, test.code)
			}
		})
	}
}

// TestUnknownBuilderOptionWarnsRatherThanRefusing: workspace_builder_options
// is read by every port, and the settings inside it are not the same set
// everywhere. An unrecognised one is reported and ignored; refusing the
// document would make a workspace shared between ports unloadable.
func TestUnknownBuilderOptionWarnsRatherThanRefusing(t *testing.T) {
	doc := document{"session_name": "example", "windows": []any{document{"panes": []any{nil}}}}
	if err := json.Unmarshal([]byte(`{"workspace_builder_options":{"pane_readines":"never","pane_readiness":"always"}}`), &doc); err != nil {
		t.Fatal(err)
	}
	plan, err := normalize(doc, t.TempDir())
	if err != nil {
		t.Fatalf("normalize refused an unknown builder option: %v", err)
	}
	if plan.Readiness != "always" {
		t.Fatalf("readiness = %q, want the recognised setting honoured", plan.Readiness)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0].Code != "unsupported_key" || !strings.Contains(plan.Warnings[0].Message, "pane_readines") {
		t.Fatalf("warnings = %+v, want the unknown setting named under its own code", plan.Warnings)
	}
}

// TestStartDirectoryWarningsAndNullHandling: a start_directory that does not
// exist still builds -- tmux falls back to $HOME -- so the typo has to be
// said out loud or the panes come up somewhere else and the load reports
// success. An explicit YAML null is the absent key, not a refusal and not a
// crash: bare "~" is null in YAML, where "~/" is the string.
func TestStartDirectoryWarningsAndNullHandling(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "definitely", "not", "here")
	t.Run("missing", func(t *testing.T) {
		doc := document{"session_name": "example", "start_directory": missing, "windows": []any{document{"panes": []any{nil}}}}
		plan, err := normalize(doc, base)
		if err != nil {
			t.Fatalf("a missing start_directory must still build: %v", err)
		}
		if plan.Directory != missing {
			t.Fatalf("directory = %q, want %q", plan.Directory, missing)
		}
		if len(plan.Warnings) != 1 || plan.Warnings[0].Code != "start_directory_missing" || !strings.Contains(plan.Warnings[0].Message, missing) || !strings.Contains(plan.Warnings[0].Message, "$HOME") {
			t.Fatalf("warnings = %+v, want the path named under a code of its own", plan.Warnings)
		}
	})
	t.Run("null", func(t *testing.T) {
		doc := document{"session_name": "example", "start_directory": nil, "windows": []any{document{"panes": []any{nil}}}}
		plan, err := normalize(doc, base)
		if err != nil || plan.Directory != "" || len(plan.Warnings) != 0 {
			t.Fatalf("an explicit null start_directory must read as absent: %v %q %+v", err, plan.Directory, plan.Warnings)
		}
	})
}

// TestInvalidLayoutMessageDescribesTheDocument: the layout validator's own
// text describes a library call and the tmux release that made the check
// necessary. What the user wrote is a layout name in a window.
func TestInvalidLayoutMessageDescribesTheDocument(t *testing.T) {
	doc := document{"session_name": "example", "windows": []any{document{"layout": "definitely-not-a-layout", "panes": []any{nil}}}}
	_, err := normalize(doc, t.TempDir())
	if err == nil {
		t.Fatal("an unusable layout must be refused")
	}
	if !strings.Contains(err.Error(), `"definitely-not-a-layout"`) {
		t.Fatalf("message does not name the layout: %v", err)
	}
	for _, leaked := range []string{"invalid server command request", "select-layout", "3.3a", "socket"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("message carries implementation wording %q: %v", leaked, err)
		}
	}
}

// TestRefusalsNameTheLineTheyAreOn: fixing one mistake and rerunning against
// a sixty-line YAML file is the most common thing a user does with a config
// tool, and a refusal that names no position makes it a search.
func TestRefusalsNameTheLineTheyAreOn(t *testing.T) {
	for _, test := range []struct{ name, text, want string }{
		{
			"window layout",
			"session_name: example\nwindows:\n  - window_name: one\n    panes: [blank]\n  - window_name: two\n    layout: definitely-not-a-layout\n    panes: [blank]\n",
			"line 6:",
		},
		{
			"unknown window key",
			"session_name: example\nwindows:\n  - window_name: one\n    panes: [blank]\n  - window_name: two\n    shell_command_befor: oops\n    panes: [blank]\n",
			"line 6:",
		},
		{
			"pane boolean",
			"session_name: example\nwindows:\n  - window_name: one\n    panes:\n      - shell_command: echo hi\n        focus: maybe\n",
			"line 6:",
		},
		{
			"session name",
			"session_name: my.proj\nwindows:\n  - panes: [blank]\n",
			"line 1:",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc, lines, err := decodeDocument([]byte(test.text))
			if err != nil {
				t.Fatal(err)
			}
			_, err = normalizeSource(doc, t.TempDir(), lines)
			if err == nil {
				t.Fatal("this document must be refused")
			}
			if !strings.HasPrefix(err.Error(), test.want) {
				t.Fatalf("refusal = %q, want it to start with %q", err, test.want)
			}
		})
	}
}

// TestEveryWindowsProblemIsReportedAtOnce: reporting the first problem puts
// the user in a loop of fix one, rerun, find the next. Every window is
// checked, and each problem is reported with the line it is on.
func TestEveryWindowsProblemIsReportedAtOnce(t *testing.T) {
	text := "session_name: example\n" +
		"windows:\n" +
		"  - window_name: one\n" +
		"    layout: definitely-not-a-layout\n" +
		"    panes: [blank]\n" +
		"  - window_name: two\n" +
		"    shell_command_befor: oops\n" +
		"    panes: [blank]\n"
	doc, lines, err := decodeDocument([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	_, err = normalizeSource(doc, t.TempDir(), lines)
	if err == nil {
		t.Fatal("this document must be refused")
	}
	for _, want := range []string{"line 4:", "line 7:"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %q, want it to carry %q", err, want)
		}
	}
}
