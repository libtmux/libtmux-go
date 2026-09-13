package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func importAndLoad(t *testing.T, server tmux.Server, dir, kind string, source map[string]any) tmux.Session {
	t.Helper()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := write(t, dir, "source.json", string(data))
	saved := filepath.Join(t.TempDir(), "imported.json")
	code, out, diagnostic := run(t, "import", kind, input, "--json", "--save-to", saved, "--workspace-format", "json")
	if code != 0 || diagnostic != "" {
		t.Fatalf("import: %d %q %q", code, out, diagnostic)
	}
	code, out, diagnostic = run(t, "load", saved, "-d", "--json", "-S", server.SocketPath(), "-f", server.ConfigFile())
	if code != 0 || diagnostic != "" {
		t.Fatalf("load imported: %d %q %q", code, out, diagnostic)
	}
	snapshot, err := server.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range snapshot.Sessions() {
		if name, _ := session.Name(); name == "imported" {
			return session
		}
	}
	t.Fatal("imported session is absent")
	return tmux.Session{}
}

func importMarker(t *testing.T, path, wanted string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var data []byte
	var err error
	for {
		data, err = os.ReadFile(path)
		if err == nil && string(data) == wanted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker %s: %q, want %q: %v", filepath.Base(path), data, wanted, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestImportedWorkspacesKeepCommandsAndRelocatedDirectories(t *testing.T) {
	for _, kind := range []string{"tmuxinator", "teamocil"} {
		t.Run(kind, func(t *testing.T) {
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "keeper"}})
			before, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			pid := daemonPID(t, server)
			dir := t.TempDir()
			root := filepath.Join(dir, "project root")
			nested := filepath.Join(root, "nested")
			if err := os.MkdirAll(nested, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			first := []any{"printf A > ordered", "printf B >> ordered"}
			panes := []any{"printf C > pane-$TMUX_PANE", "printf D > pane-$TMUX_PANE"}
			source := map[string]any{"name": "imported", "root": "project root"}
			if kind == "tmuxinator" {
				source["pre_window"] = []any{"false", "printf P > project-$TMUX_PANE"}
				source["windows"] = []any{map[string]any{"first": first}, map[string]any{"second": map[string]any{"root": "nested", "layout": "even-horizontal", "pre": []any{"false", "printf broken > forbidden"}, "panes": panes}}}
			} else {
				source["windows"] = []any{map[string]any{"name": "first", "panes": []any{map[string]any{"commands": first}}}, map[string]any{"name": "second", "root": "nested", "layout": "even-horizontal", "focus": true, "options": map[string]any{"automatic-rename": false}, "panes": []any{map[string]any{"commands": []any{"false", panes[0]}}, map[string]any{"cmd": panes[1], "focus": true}}}}
			}
			session := importAndLoad(t, server, dir, kind, source)
			windows, _ := session.Windows()
			if len(windows) != 2 {
				t.Fatalf("windows: %v", windows)
			}
			for index, window := range windows {
				name, _ := window.Name()
				if name != []string{"first", "second"}[index] || window.Index() != index {
					t.Errorf("window order: %d %q", window.Index(), name)
				}
				panes, _ := window.Panes()
				if len(panes) != index+1 {
					t.Fatalf("pane count in %s: %d", name, len(panes))
				}
				cwd := root
				if index == 1 {
					cwd = nested
				}
				for pi, pane := range panes {
					if got, _ := pane.CurrentPath(); got != cwd {
						t.Errorf("pane cwd %q want %q", got, cwd)
					}
					if kind == "tmuxinator" {
						importMarker(t, filepath.Join(cwd, "project-"+pane.ID().String()), "P")
					}
					if index == 1 {
						importMarker(t, filepath.Join(cwd, "pane-"+pane.ID().String()), []string{"C", "D"}[pi])
					}
					active, _ := pane.Active()
					wanted := pi == 0
					if kind == "teamocil" && index == 1 {
						wanted = pi == 1
					}
					if active != wanted {
						t.Errorf("pane focus %s=%t want %t", pane.ID(), active, wanted)
					}
				}
				active, _ := window.Active()
				wanted := index == 0
				if kind == "teamocil" {
					wanted = index == 1
				}
				if active != wanted {
					t.Errorf("window focus %s=%t want %t", window.ID(), active, wanted)
				}
			}
			importMarker(t, filepath.Join(root, "ordered"), "AB")
			if _, err := os.Stat(filepath.Join(nested, "forbidden")); !os.IsNotExist(err) {
				t.Errorf("window pre lost failure barrier: %v", err)
			}
			after, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := after.PaneByID(before.Panes()[0].ID()); err != nil || daemonPID(t, server) != pid || len(after.Sessions()) != 2 {
				t.Fatalf("keeper changed: %v", err)
			}
		})
	}
}

func TestImportedSynchronizationPreservesSequentialDelivery(t *testing.T) {
	for _, mode := range []string{"tmuxinator/before", "tmuxinator/after", "tmuxinator/none", "teamocil/before", "teamocil/none"} {
		t.Run(mode, func(t *testing.T) {
			parts := strings.Split(mode, "/")
			kind, phase := parts[0], parts[1]
			server := tmuxtest.NewServerWithOptions(t.Context(), t, tmuxtest.ServerOptions{FixedShell: true, InitialSession: &tmux.NewSessionRequest{Name: "keeper"}})
			before, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			t.Chdir(dir)
			panes := []any{}
			for _, letter := range []string{"A", "B", "C"} {
				command := "printf " + letter + " >> receipt-$TMUX_PANE"
				if kind == "teamocil" {
					panes = append(panes, map[string]any{"commands": []any{command}})
				} else {
					panes = append(panes, command)
				}
			}
			window := map[string]any{"panes": panes}
			source := map[string]any{"name": "imported"}
			if kind == "tmuxinator" {
				if phase != "none" {
					window["synchronize"] = phase
				}
				source["windows"] = []any{map[string]any{"delivery": window}}
			} else {
				window["name"] = "delivery"
				window["options"] = map[string]any{"synchronize-panes": phase == "before"}
				source["windows"] = []any{window}
			}
			session := importAndLoad(t, server, dir, kind, source)
			windows, _ := session.Windows()
			if len(windows) != 1 {
				t.Fatalf("windows: %v", windows)
			}
			actual, _ := windows[0].Panes()
			if len(actual) != 3 {
				t.Fatalf("panes: %v", actual)
			}
			for index, pane := range actual {
				wanted := []string{"A", "B", "C"}[index]
				if phase == "before" {
					wanted = "ABC"[index:]
				}
				importMarker(t, filepath.Join(dir, "receipt-"+pane.ID().String()), wanted)
			}
			options, err := server.Cmd(t.Context(), "show-options", "-A", "-w", "-v", "-t", windows[0].ID().String(), "synchronize-panes")
			wanted := "off"
			if phase != "none" {
				wanted = "on"
			}
			if err != nil || options.ExitCode != 0 || strings.TrimSpace(string(options.RawStdout)) != wanted {
				t.Errorf("synchronization readback: %+v %v", options, err)
			}
			after, err := server.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := after.PaneByID(before.Panes()[0].ID()); err != nil || len(after.Sessions()) != 2 {
				t.Fatalf("keeper changed: %v", err)
			}
		})
	}
}
