package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPreservesCommandGroupsAndSavedContext(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUX_WORKSPACE_PYTHON", filepath.Join(dir, "missing-python"))
	tests := []struct {
		kind, source string
		commands     [][]string
		focus        int
		sync         string
	}{
		{"tmuxinator", `{"name":"imported","pre_window":["false","project"],"windows":[{"first":["blank","pane"]},{"second":{"root":"nested","pre":["false","window"],"synchronize":"after","panes":[["one","two"],"three"]}}]}`, [][]string{{"false; project", "blank", "pane"}, {"false; project", "false && window", "one", "two"}}, 0, "after"},
		// Teamocil evaluates no templates, so this markup is ordinary
		// text and must survive the import verbatim.
		{"teamocil", `{"session":{"name":"imported","windows":[{"name":"first","panes":[{"commands":["false","echo <%= literal %>"]}]},{"name":"second","root":"nested","focus":true,"options":{"automatic-rename":false},"panes":[{"commands":["one","two"]},{"cmd":"three","focus":true}]}]}}`, [][]string{{"false; echo <%= literal %>"}, {"one; two"}}, 1, ""},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			source := filepath.Join(dir, test.kind+".json")
			destination := filepath.Join(t.TempDir(), "saved.json")
			if err := os.WriteFile(source, []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			code, out, diagnostic := invoke(t, "import", test.kind, source, "--json", "--save-to", destination, "--workspace-format", "json")
			if code != 0 || diagnostic != "" {
				t.Fatalf("import: %d %q %q", code, out, diagnostic)
			}
			doc, err := readDocument(destination)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := normalize(doc, filepath.Dir(destination))
			if err != nil {
				t.Fatal(err)
			}
			if plan.Directory != cwd || len(plan.Windows) != 2 || len(plan.Windows[0].Panes) != 1 || len(plan.Windows[1].Panes) != 2 || plan.Windows[1].Directory != filepath.Join(cwd, "nested") {
				t.Fatalf("saved context/order: %+v", plan)
			}
			for wi, wanted := range test.commands {
				got := plan.Windows[wi].Panes[0].Commands
				if len(got) != len(wanted) {
					t.Fatalf("window %d commands: %+v", wi, got)
				}
				for i, command := range got {
					if command.Text != wanted[i] {
						t.Errorf("window %d command %d: %q want %q", wi, i, command.Text, wanted[i])
					}
				}
			}
			if !plan.Windows[0].Panes[0].Focus || !plan.Windows[1].Panes[test.focus].Focus {
				t.Fatalf("pane focus: %+v", plan.Windows)
			}
			if test.sync == "after" && plan.Windows[1].OptionsAfter["synchronize-panes"] != "on" {
				t.Fatal("lost after synchronization")
			}
		})
	}
}

func TestImportRefusesInvalidSourceBeforePublishing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tests := []struct{ kind, source, field string }{
		{"tmuxinator", `{"name":"x","root":"<%= cwd %>","windows":[{"one":null}]}`, "ERB"},
		{"tmuxinator", `{"name":"x","windows":[{"one":["echo <%= value %>"]}]}`, "ERB"},
		{"tmuxinator", `{"name":"x","windows":[{"<%= dynamic_window %>":null}]}`, "ERB"},
		{"tmuxinator", `{"name":null,"project_name":"x","windows":[{"one":null}]}`, "aliases"},
		{"tmuxinator", `{"name":"x","pre":"run","windows":[{"one":null}]}`, "pre"},
		{"tmuxinator", `{"name":"x","post":"run","windows":[{"one":null}]}`, "post"},
		{"tmuxinator", `{"name":"x","socket_name":"socket","windows":[{"one":null}]}`, "socket_name"},
		{"tmuxinator", `{"name":"x","cli_args":"-f config","windows":[{"one":null}]}`, "cli_args"},
		{"tmuxinator", `{"name":"x","rbenv":"3","windows":[{"one":null}]}`, "rbenv"},
		{"tmuxinator", `{"name":"x","windows":[{"one":{"panes":[{"title":"run"}]}}]}`, "pane"},
		{"tmuxinator", `{"name":"x","windows":[{"one":null,"two":null}]}`, "one name"},
		{"tmuxinator", `{"name":"x","project_name":"x","windows":[{"one":null}]}`, "aliases"},
		{"tmuxinator", `{"name":null,"windows":[{"one":null}]}`, "name"},
		{"tmuxinator", `{"name":3,"windows":[{"one":null}]}`, "name"},
		{"tmuxinator", `{"name":"x","root":false,"windows":[{"one":null}]}`, "root"},
		{"tmuxinator", `{"name":"x","windows":[]}`, "windows"},
		{"tmuxinator", `{"name":"x","windows":[{"one":{"pre":"run"}}]}`, "panes"},
		{"tmuxinator", `{"name":"x","windows":[{"one":{"synchronize":"sometimes"}}]}`, "synchronize"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","clear":true}]}`, "clear"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","filters":{"after":"run"}}]}`, "filters"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","panes":[{"cmd":"run","width":20}]}]}`, "width"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","panes":[{"commands":"run"}]}]}`, "commands"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","panes":[{"commands":[],"cmd":"run"}]}]}`, "aliases"},
		{"teamocil", `{"name":"x","windows":[{"name":{},"panes":[{}]}]}`, "name"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","focus":{}}]}`, "focus"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","options":{"remain-on-exit":{}}}]}`, "scalar"},
		{"teamocil", `{"name":"x","windows":[{"name":"one","panes":[{}],"splits":[{}]}]}`, "aliases"},
	}
	for _, test := range tests {
		t.Run(test.kind+"/"+test.field, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source.json")
			destination := filepath.Join(dir, "saved.json")
			if !json.Valid([]byte(test.source)) {
				t.Fatal("invalid fixture JSON")
			}
			if err := os.WriteFile(source, []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, save := range []bool{false, true} {
				if err := os.WriteFile(destination, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"import", test.kind, source, "--json"}
				if save {
					args = append(args, "--save-to", destination, "--force")
				}
				code, out, diagnostic := invoke(t, args...)
				data, err := os.ReadFile(destination)
				if code != 1 || out != "" || !strings.Contains(diagnostic, test.field) || err != nil || string(data) != "original" {
					t.Errorf("save=%t: %d %q %q destination=%q err=%v", save, code, out, diagnostic, data, err)
				}
			}
		})
	}
}
