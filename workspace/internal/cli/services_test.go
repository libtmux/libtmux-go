package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPairedFlagsLastOccurrence(t *testing.T) {
	for _, args := range [][]string{{"--no-startup", "--use-pythonrc", "--no-vi-mode", "--use-vi-mode"}, {"--use-pythonrc", "--no-startup", "--use-vi-mode", "--no-vi-mode"}} {
		r := &invocation{ctx: context.Background(), in: strings.NewReader(""), out: io.Discard, err: io.Discard}
		root := r.tree()
		shell, _, err := root.Find([]string{"shell"})
		if err != nil {
			t.Fatal(err)
		}
		if err := shell.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		startup := shell.Flags().Lookup("use-pythonrc").Value.String()
		vi := shell.Flags().Lookup("use-vi-mode").Value.String()
		want := "false"
		if args[0] == "--no-startup" {
			want = "true"
		}
		if startup != want || vi != want {
			t.Fatalf("%v: startup=%s vi=%s", args, startup, vi)
		}
	}
}

func TestPythonVersionCheckSurvivesOptimization(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python compatibility runtime unavailable")
	}
	dir := t.TempDir()
	metadata := filepath.Join(dir, "tmuxp-0.0.dist-info")
	if err := os.Mkdir(metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "METADATA"), []byte("Name: tmuxp\nVersion: 0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", dir)
	t.Setenv("PYTHONOPTIMIZE", "1")
	r := &invocation{ctx: t.Context(), out: io.Discard, err: io.Discard}
	if err := r.checkPython(true); err == nil {
		t.Fatal("optimized Python bypassed the tmuxp distribution version check")
	}
}

type rejectingWriter struct{}

func (rejectingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestChildDrainFailureCancelsProcess(t *testing.T) {
	for _, logFailure := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		r := &invocation{ctx: ctx, out: rejectingWriter{}, err: io.Discard, ndjson: true}
		var log io.Writer
		if logFailure {
			log = rejectingWriter{}
		}
		start := time.Now()
		_, err := r.process([]string{"/bin/sh", "-c", "printf output; sleep 5"}, "", nil, true, log)
		cancel()
		if err == nil || time.Since(start) > 700*time.Millisecond {
			t.Fatalf("log failure=%v: child did not stop promptly: %v (%s)", logFailure, err, time.Since(start))
		}
	}
}

func TestReadLeavesAndOutputPrecedence(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python compatibility runtime unavailable")
	}
	dir := t.TempDir()
	t.Setenv("TMUXP_CONFIGDIR", dir)
	t.Chdir(dir)
	write := func(name, contents string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	workspace := write("hello.yaml", "session_name: héllo\nwindows:\n- window_name: editor\n  panes: [vim]\n")
	tmuxinator := write("importer.yml", "name: imported\nwindows:\n- editor: vim\n")
	teamocil := write("teamocil.yml", "session:\n  name: imported\n  windows:\n  - name: editor\n    panes:\n    - cmd: vim\n")
	editor := write("editor.sh", "#!/bin/sh\nprintf '%s' \"$1\"\n")
	if err := os.Chmod(editor, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", editor+" 'quoted argument'")
	for _, args := range [][]string{{"ls", "--json", "--full"}, {"search", "--json", "(?<=h)éllo"}, {"search", "--json", "missing"}, {"edit", workspace, "--json"}, {"convert", workspace, "--json"}, {"import", "teamocil", teamocil, "--json"}, {"import", "tmuxinator", tmuxinator, "--json"}, {"debug-info", "--json"}} {
		code, out, diagnostic := invoke(t, args...)
		if code != 0 || !json.Valid([]byte(out)) || diagnostic != "" {
			t.Fatalf("%v: %d %q %q", args, code, out, diagnostic)
		}
	}
	code, out, diagnostic := invoke(t, "search", "--json", "missing")
	if code != 0 || strings.TrimSpace(out) != "[]" || diagnostic != "" {
		t.Fatalf("empty search %d %q %q", code, out, diagnostic)
	}
	code, out, diagnostic = invoke(t, "--color", "always", "--json", "ls", "--ndjson")
	if code != 0 || diagnostic != "" || strings.Contains(out, "\x1b") {
		t.Fatalf("NDJSON %d %q %q", code, out, diagnostic)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("invalid record %q", line)
		}
	}
	code, out, diagnostic = invoke(t, "search", "--json", "[")
	if code != 2 || out != "" || !json.Valid([]byte(diagnostic)) {
		t.Fatalf("invalid regex %d %q %q", code, out, diagnostic)
	}
}

func TestMachineControlBytesAndBoundedChildOutput(t *testing.T) {
	var out strings.Builder
	r := &invocation{ctx: context.Background(), out: &out, err: io.Discard, ndjson: true, command: "shell"}
	result, err := r.process([]string{"/bin/sh", "-c", "printf 'line\\n\\033[31m\\t'; printf 'diagnostic\\n' >&2"}, "", nil, true, nil)
	if err != nil || result.Status != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("raw control bytes leaked")
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("invalid NDJSON %q", line)
		}
	}
	result, err = r.process([]string{"/bin/sh", "-c", "head -c 1100000 /dev/zero; head -c 1100000 /dev/zero >&2"}, "", nil, false, nil)
	if err != nil || !result.Truncated || len(result.Stdout) != captureLimit || len(result.Stderr) != captureLimit {
		t.Fatalf("bounded concurrent drain %+v %v", struct {
			Out, Err  int
			Truncated bool
		}{len(result.Stdout), len(result.Stderr), result.Truncated}, err)
	}
}

func TestStreamPreservesSplitUTF8(t *testing.T) {
	var out strings.Builder
	r := &invocation{ctx: t.Context(), out: &out, err: io.Discard, ndjson: true, command: "shell"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &captureWriter{r: r, stream: "stdout", emit: true, cancel: cancel}
	for _, part := range [][]byte{{0xe7}, {0x95}, {0x8c, '\n'}} {
		if _, err := w.Write(part); err != nil {
			t.Fatal(err)
		}
	}
	var reconstructed strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var event struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		reconstructed.WriteString(event.Text)
	}
	if reconstructed.String() != "界\n" || ctx.Err() != nil {
		t.Fatalf("split UTF-8 became %q", reconstructed.String())
	}
}

func TestTmuxinatorDirectoryOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUXINATOR_CONFIG", dir)
	if err := os.WriteFile(filepath.Join(dir, "example.yaml"), []byte("name: imported\nwindows:\n- editor: vim\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, diagnostic := invoke(t, "import", "tmuxinator", "example", "--json")
	if code != 0 || !strings.Contains(out, `"session_name":"imported"`) || diagnostic != "" {
		t.Fatalf("override %d %q %q", code, out, diagnostic)
	}
}

func TestReadinessValidation(t *testing.T) {
	doc := document{"session_name": "ready", "windows": []any{document{"panes": []any{"blank"}}}, "workspace_builder_options": document{"pane_readiness": "sometimes"}}
	if _, err := normalize(doc, t.TempDir()); err == nil {
		t.Fatal("invalid readiness policy was silently accepted")
	}
}

func TestCommandMetadataAndDocumentation(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, out, diagnostic := invoke(t, "--command-tree", "--json")
	var tree struct {
		Children []struct {
			Name string `json:"name"`
		} `json:"children"`
		Flags []struct {
			Name    string `json:"name"`
			Default string `json:"default"`
		} `json:"flags"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &tree) != nil || len(tree.Children) != 9 || diagnostic != "" {
		t.Fatalf("metadata %d %s %s", code, out, diagnostic)
	}
	for _, flag := range tree.Flags {
		if flag.Name == "json" && flag.Default != "false" {
			t.Fatalf("invocation changed declared default: %q", flag.Default)
		}
	}
	for _, format := range []string{"markdown", "man", "yaml"} {
		code, out, diagnostic = invoke(t, "--generate-docs", format)
		if code != 0 || !strings.Contains(out, "tmuxinator") || !strings.Contains(out, "progress-lines") || diagnostic != "" {
			t.Fatalf("docs %s: %d %s %s", format, code, out, diagnostic)
		}
	}
}
