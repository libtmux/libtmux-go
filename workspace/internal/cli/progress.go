package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

var progressPresets = map[string]string{
	"default": "Loading workspace: {session} {bar} {progress} {window}",
	"minimal": "Loading workspace: {session} [{window_progress}]",
	"window":  "Loading workspace: {session} {window_bar} {window_progress_rel}",
	"pane":    "Loading workspace: {session} {pane_bar} {session_pane_progress}",
	"verbose": "Loading workspace: {session} [window {window_index} of {window_total} · pane {pane_index} of {pane_total}] {window}",
}

type progressPresenter struct {
	rawOut                                                                                            io.Writer
	mu                                                                                                sync.Mutex
	out                                                                                               io.Writer
	format                                                                                            string
	width, height, lines, drawn, tick                                                                 int
	session, path, window                                                                             string
	windowIndex, windowTotal, windowsDone, paneIndex, paneTotal, panesDone, sessionPanes, sessionDone int
	scriptLines                                                                                       []string
	partial                                                                                           map[string]string
	waiting                                                                                           bool
	panelStdout                                                                                       bool
	err                                                                                               error
	once                                                                                              sync.Once
	decorate                                                                                          func(string, string) string

	stop, done chan struct{}
}

func newProgress(out io.Writer, format string, lines, width, height int) *progressPresenter {
	if lines == -1 {
		lines = max(0, height-2)
	}
	return &progressPresenter{out: out, format: format, width: max(1, width), height: height, lines: min(lines, max(0, height-2)), partial: map[string]string{}, stop: make(chan struct{}), done: make(chan struct{})}
}

func (r *invocation) progressEnabled(o *options) bool {
	return !r.machine() && !o.noProgress && os.Getenv("TMUXP_PROGRESS") != "0" && terminal(r.err)
}

func sharedTerminal(first, second io.Writer) bool {
	if !terminal(first) || !terminal(second) {
		return false
	}
	firstInfo, firstErr := first.(*os.File).Stat()
	secondInfo, secondErr := second.(*os.File).Stat()
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}

func (r *invocation) startProgress(o *options) {
	if !r.progressEnabled(o) {
		return
	}
	width, height := 80, 24
	if file, ok := r.err.(*os.File); ok {
		if w, h, err := term.GetSize(int(file.Fd())); err == nil && w > 0 && h > 0 {
			width, height = w, h
		}
	}
	r.progress = newProgress(r.err, o.progressFormat, o.progressLines, width, height)
	p := r.progress
	p.rawOut = r.out
	p.panelStdout = sharedTerminal(r.out, r.err)
	p.decorate = func(role, text string) string { return r.styleFor(r.err, role, text) }
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-ticker.C:
				p.mu.Lock()
				p.tick++
				p.draw()
				p.mu.Unlock()
			}
		}
	}()
}

func (p *progressPresenter) close() error {
	p.once.Do(func() { close(p.stop) })
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
	return p.err
}

func (p *progressPresenter) begin(plan loadPlan, path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.session, p.path = plan.Name, path
	p.windowIndex, p.windowTotal, p.windowsDone = 0, len(plan.Windows), 0
	p.paneIndex, p.paneTotal, p.panesDone, p.sessionPanes, p.sessionDone = 0, 0, 0, 0, 0
	p.window, p.scriptLines, p.partial = "", nil, map[string]string{}
	for _, w := range plan.Windows {
		p.sessionPanes += len(w.Panes)
	}
	p.draw()
}

func (p *progressPresenter) event(event string, data map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch event {
	case "window-created":
		p.windowIndex++
		p.window = textValue(data["window_name"])
		p.paneTotal, _ = data["pane_total"].(int)
		p.paneIndex, p.panesDone = 0, 0
	case "pane-created":
		p.paneIndex++
	case "pane-completed":
		p.panesDone++
		p.sessionDone++
	case "window-completed":
		p.windowsDone++
	case "script-started":
		p.clear()
		p.waiting = true
	case "script-completed":
		p.waiting = false
	case "workspace-completed":
		p.clear()
		p.session = ""
	case "warning":
		p.clear()
		if p.err != nil {
			return p.err
		}
		_, p.err = fmt.Fprintln(p.out, "warning: "+safeTerminal(textValue(data["message"])))
	}
	p.draw()
	return p.err
}

func safeTerminal(value string) string {
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			fmt.Fprintf(&out, "\\x%02x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (p *progressPresenter) script(stream, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if p.lines == 0 {
		p.clear()
		if p.err != nil {
			return p.err
		}
		p.waiting = true
		writer := p.out
		if stream == "stdout" && p.rawOut != nil {
			writer = p.rawOut
		}
		_, p.err = io.WriteString(writer, text)
		return p.err
	}
	if stream == "stdout" && p.rawOut != nil && !p.panelStdout {
		_, p.err = io.WriteString(p.rawOut, text)
		return p.err
	}
	parts := strings.Split(p.partial[stream]+text, "\n")
	for i, part := range parts {
		parts[i] = runewidth.Truncate(safeTerminal(part), p.width, "…")
	}
	p.partial[stream] = parts[len(parts)-1]
	p.scriptLines = append(p.scriptLines, parts[:len(parts)-1]...)
	if len(p.scriptLines) > p.lines {
		p.scriptLines = p.scriptLines[len(p.scriptLines)-p.lines:]
	}
	p.draw()
	return p.err
}

func progressBar(done, total int) string {
	filled := 0
	if total > 0 {
		filled = min(10, done*10/total)
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", 10-filled) + "]"
}

func fraction(done, total int) string { return fmt.Sprintf("%d/%d", done, total) }

func (p *progressPresenter) frame() []string {
	format, ok := progressPresets[p.format]
	if !ok {
		format = p.format
	}
	windowProgress, paneProgress := "", ""
	if p.windowIndex > 0 {
		windowProgress = fraction(p.windowIndex, p.windowTotal)
	}
	if p.paneIndex > 0 {
		paneProgress = fraction(p.paneIndex, p.paneTotal)
	}
	progress := windowProgress + " win"
	if paneProgress != "" {
		progress += " · " + paneProgress + " pane"
	}
	bar, icon := progressBar(p.sessionDone, p.sessionPanes), ""
	if p.waiting {
		bar = "[" + strings.Repeat("░", p.tick%10) + "█" + strings.Repeat("░", 9-p.tick%10) + "]"
		icon = "⏸"
	}
	values := map[string]string{
		"session": p.session, "workspace_path": p.path, "window": p.window,
		"window_progress": windowProgress, "window_progress_rel": fraction(p.windowsDone, p.windowTotal),
		"pane_progress": paneProgress, "pane_progress_rel": fraction(p.panesDone, p.paneTotal),
		"session_pane_progress": fraction(p.sessionDone, p.sessionPanes), "progress": progress,
		"bar": bar, "window_bar": progressBar(p.windowsDone, p.windowTotal), "pane_bar": bar,
		"status_icon": icon, "summary": fmt.Sprintf("[%d win, %d panes]", p.windowsDone, p.sessionDone),
	}
	counts := map[string]int{"window_index": p.windowIndex, "window_total": p.windowTotal, "windows_done": p.windowsDone, "windows_remaining": p.windowTotal - p.windowsDone, "pane_index": p.paneIndex, "pane_total": p.paneTotal, "pane_done": p.panesDone, "pane_remaining": p.paneTotal - p.panesDone, "session_pane_total": p.sessionPanes, "session_panes_done": p.sessionDone, "session_panes_remaining": p.sessionPanes - p.sessionDone, "overall_percent": p.sessionDone * 100 / max(1, p.sessionPanes)}
	for key, value := range counts {
		values[key] = strconv.Itoa(value)
	}
	pairs := []string{}
	for key, value := range values {
		pairs = append(pairs, "{"+key+"}", safeTerminal(value))
	}
	header := strings.NewReplacer(pairs...).Replace(format)
	lines := []string{header}
	lines = append(lines, p.scriptLines...)
	for _, stream := range []string{"stdout", "stderr"} {
		if p.partial[stream] != "" && p.lines > 0 {
			lines = append(lines, p.partial[stream])
		}
	}
	if len(lines) > p.lines+1 {
		lines = append(lines[:1], lines[len(lines)-p.lines:]...)
	}
	for i, line := range lines {
		lines[i] = runewidth.Truncate(safeTerminal(line), p.width, "…")
	}
	return lines
}

func (p *progressPresenter) clear() {
	if p.drawn == 0 || p.err != nil {
		return
	}
	var out strings.Builder
	for i := 0; i < p.drawn; i++ {
		out.WriteString("\r\x1b[2K")
		if i < p.drawn-1 {
			out.WriteString("\x1b[1A")
		}
	}
	_, p.err = io.WriteString(p.out, out.String())
	p.drawn = 0
}

func (p *progressPresenter) draw() {
	if p.session == "" || p.err != nil || (p.waiting && p.lines == 0) {
		return
	}
	p.clear()
	if p.err != nil {
		return
	}
	lines := p.frame()
	if p.decorate != nil {
		for i, line := range lines {
			role := "secondary"
			if i == 0 {
				role = "heading"
			}
			lines[i] = p.decorate(role, line)
		}
	}
	_, p.err = io.WriteString(p.out, strings.Join(lines, "\n"))
	p.drawn = len(lines)
}

func sessionDimensions() (int, int, error) {
	width, height := 80, 24
	for _, dimension := range []struct {
		names  []string
		target *int
	}{
		{[]string{"TMUXP_DEFAULT_COLUMNS", "COLUMNS"}, &width},
		{[]string{"TMUXP_DEFAULT_ROWS", "ROWS"}, &height},
	} {
		for _, name := range dimension.names {
			if raw := os.Getenv(name); raw != "" {
				n, err := strconv.Atoi(raw)
				if err != nil || n < 1 || n > 65535 {
					return 0, 0, usage("%s must be 1..65535", name)
				}
				*dimension.target = n
				break
			}
		}
	}
	if value, ok := os.LookupEnv("TMUXP_DETECT_TERMINAL_SIZE"); ok && value != "1" {
		return 0, 0, nil
	}
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		width, height = w, h
	}
	for _, dimension := range []struct {
		name   string
		target *int
	}{{"COLUMNS", &width}, {"LINES", &height}} {
		if raw := os.Getenv(dimension.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 65535 {
				return 0, 0, usage("%s must be 1..65535", dimension.name)
			}
			*dimension.target = n
		}
	}
	return width, height, nil
}
