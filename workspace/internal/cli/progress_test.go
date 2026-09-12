package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestProgressTracksWorkAndBoundsScriptOutput(t *testing.T) {
	var out bytes.Buffer
	p := newProgress(&out, "{session}: {window_progress_rel} {session_pane_progress} {unknown}", 2, 34, 10)
	p.begin(loadPlan{Name: "界界", Windows: []windowPlan{{Name: "first", Panes: []panePlan{{}, {}}}, {Name: "second", Panes: []panePlan{{}}}}}, "example.yaml")
	if err := p.event("window-created", map[string]any{"window_name": "first", "pane_total": 2}); err != nil {
		t.Fatal(err)
	}
	if err := p.event("pane-completed", nil); err != nil {
		t.Fatal(err)
	}
	if err := p.event("pane-completed", nil); err != nil {
		t.Fatal(err)
	}
	if err := p.event("window-completed", nil); err != nil {
		t.Fatal(err)
	}
	if err := p.script("stdout", "old\nretained\n界界界界界界界界界界界界界界界界界界\x1b[31m\n"); err != nil {
		t.Fatal(err)
	}
	lines := p.frame()
	if !strings.Contains(lines[0], "1/2 2/3 {unknown}") || len(lines) != 3 || lines[1] != "retained" {
		t.Fatalf("progress state: %q", lines)
	}
	for _, line := range lines {
		if runewidth.StringWidth(line) > 34 || strings.ContainsRune(line, '\x1b') {
			t.Fatalf("unbounded or executable terminal content: %q", line)
		}
	}
	for _, preset := range []string{"default", "minimal", "window", "pane", "verbose"} {
		p.format = preset
		if strings.Contains(p.frame()[0], "{") || p.frame()[0] == preset {
			t.Fatalf("unresolved preset %s: %q", preset, p.frame()[0])
		}
	}
}

type clearFailureWriter struct{ calls int }

func (w *clearFailureWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.calls == 2 {
		return 0, io.ErrClosedPipe
	}
	return len(data), nil
}

func TestProgressPreservesClearFailure(t *testing.T) {
	w := &clearFailureWriter{}
	p := newProgress(w, "minimal", 1, 80, 24)
	p.begin(loadPlan{Name: "failure"}, "workspace.yaml")
	if err := p.event("warning", map[string]any{"message": "example"}); err == nil || w.calls != 2 {
		t.Fatalf("clear error was overwritten: %v, writes=%d", err, w.calls)
	}
}

func TestProgressZeroLinesHandsOffScriptStdout(t *testing.T) {
	var out, diagnostic bytes.Buffer
	p := newProgress(&diagnostic, "minimal", 0, 80, 24)
	p.rawOut = &out
	p.begin(loadPlan{Name: "raw"}, "workspace.yaml")
	r := &invocation{ctx: t.Context(), out: &out, err: &diagnostic, progress: p}
	w := &captureWriter{r: r, stream: "stdout"}
	if err := w.emitBytes([]byte("raw child output\n"), true); err != nil {
		t.Fatal(err)
	}
	if out.String() != "raw child output\n" || p.drawn != 0 {
		t.Fatalf("raw stream handoff failed: %q drawn=%d", out.String(), p.drawn)
	}
}

func TestHumanWarningsRemainVisibleWithoutProgress(t *testing.T) {
	var out, diagnostic bytes.Buffer
	r := &invocation{ctx: t.Context(), out: &out, err: &diagnostic, color: "never"}
	if err := r.event("warning", map[string]any{"message": "prompt timeout"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || !strings.Contains(diagnostic.String(), "prompt timeout") {
		t.Fatalf("warning was lost: stdout=%q stderr=%q", out.String(), diagnostic.String())
	}
}
