//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

func TestProgressDefaultsRequireCapableTerminal(t *testing.T) {
	if os.Getenv("LIBTMUX_PROGRESS_TERMINAL_HELPER") == "1" {
		t.Setenv("PATH", t.TempDir())
		t.Setenv("TMUXP_PROGRESS", "")
		t.Setenv("TMUXP_PROGRESS_LINES", "invalid")
		missing := filepath.Join(t.TempDir(), "absent.yaml")
		for _, test := range []struct {
			name, term string
			rows, cols uint16
			wantStatus int
		}{
			{"dumb", "dumb", 24, 80, 1},
			{"unknown", "", 24, 80, 1},
			{"no geometry", "xterm", 0, 0, 1},
			{"too small", "xterm", 1, 1, 1},
			{"capable", "xterm", 24, 80, 2},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Setenv("TERM", test.term)
				if err := unix.IoctlSetWinsize(2, unix.TIOCSWINSZ, &unix.Winsize{Row: test.rows, Col: test.cols}); err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				code := Run(t.Context(), []string{"load", "-d", missing}, strings.NewReader(""), &out, os.Stderr)
				if code != test.wantStatus || out.Len() != 0 {
					t.Fatalf("terminal capability preflight: code=%d stdout=%q; want code=%d", code, out.String(), test.wantStatus)
				}
			})
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	process := tmuxtest.StartPTYProcess(ctx, t, os.Args[0], []string{"-test.run=^TestProgressDefaultsRequireCapableTerminal$"}, append(os.Environ(), "LIBTMUX_PROGRESS_TERMINAL_HELPER=1"))
	if err := process.Wait(ctx); err != nil {
		t.Fatalf("terminal capability helper: %v %q", err, process.Output())
	}
}

func TestProgressFollowsTerminalResize(t *testing.T) {
	if os.Getenv("LIBTMUX_PROGRESS_RESIZE_HELPER") == "1" {
		t.Setenv("TERM", "xterm")
		t.Setenv("TMUXP_PROGRESS", "")
		resize := func(rows, cols uint16) {
			t.Helper()
			if err := unix.IoctlSetWinsize(2, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
				t.Fatal(err)
			}
		}
		resize(12, 80)
		requestedLines, err := strconv.Atoi(os.Getenv("LIBTMUX_PROGRESS_RESIZE_LINES"))
		if err != nil {
			t.Fatal(err)
		}
		r := &invocation{ctx: t.Context(), out: os.Stderr, err: os.Stderr, color: "never"}
		r.startProgress(&options{progressFormat: "minimal", progressLines: requestedLines})
		if r.progress == nil {
			t.Fatal("progress did not activate")
		}
		p := r.progress
		observed, err := os.OpenFile("/proc/self/fd/2", os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = observed.Close() })
		connection, err := observed.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var fd uintptr
		if err := connection.Control(func(value uintptr) { fd = value }); err != nil {
			t.Fatal(err)
		}
		var display progressFrameWriter
		var raw bytes.Buffer
		p.mu.Lock()
		p.out, p.rawOut, p.terminal = &display, &raw, observed
		p.mu.Unlock()
		t.Cleanup(func() {
			if err := p.close(); err != nil {
				t.Error(err)
			}
		})
		p.begin(loadPlan{Name: "resizing workspace"}, "workspace.yaml")
		if err := p.script("stderr", strings.Repeat("wide output line\n", 8)); err != nil {
			t.Fatal(err)
		}
		for _, size := range []struct{ rows, cols uint16 }{{5, 11}, {24, 1}, {16, 60}, {1, 1}, {12, 80}} {
			freshLine := false
			func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				freshLine = p.drawn > 0 || p.width < 2 || p.height < 3
				display.Reset()
				display.writes = nil
				resize(size.rows, size.cols)
			}()
			deadline := time.Now().Add(time.Second)
			for {
				p.mu.Lock()
				width, height, lines := p.width, p.height, p.lines
				frame := p.frame()
				output := display.String()
				writes := append([]string(nil), display.writes...)
				p.mu.Unlock()
				if width == int(size.cols) && height == int(size.rows) {
					wantLines := max(0, height-2)
					if requestedLines >= 0 {
						wantLines = min(requestedLines, wantLines)
					}
					if width < 2 {
						wantLines = 0
					}
					if lines != wantLines || len(frame) > lines+1 {
						t.Fatalf("resized panel is unbounded: height=%d lines=%d frame=%q", height, lines, frame)
					}
					if freshLine && !strings.HasPrefix(output, "\r\n") {
						t.Fatalf("resized frame reused an unknown cursor position: %q", output)
					}
					if freshLine && lines > 0 && (len(writes) < 2 || strings.HasPrefix(writes[1], "\r") || strings.HasPrefix(writes[1], "\x1b")) {
						t.Fatalf("resized frame cleared stale cursor rows: %q", writes)
					}
					flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
					if err != nil || flags&unix.O_NONBLOCK == 0 {
						t.Fatalf("terminal observation changed descriptor flags: %x %v", flags, err)
					}
					for _, line := range frame {
						if runewidth.StringWidth(line) > width {
							t.Fatalf("resized line exceeds width %d: %q", width, line)
						}
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("progress retained %dx%d after terminal resized to %dx%d", width, height, size.cols, size.rows)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if size.cols < 2 || size.rows < 3 {
				for _, stream := range []string{"stdout", "stderr"} {
					if err := p.script(stream, "raw-"+stream); err != nil {
						t.Fatal(err)
					}
				}
				p.mu.Lock()
				forwardedOut, forwardedErr := raw.String(), display.String()
				p.mu.Unlock()
				if !strings.HasSuffix(forwardedOut, "raw-stdout") || !strings.HasSuffix(forwardedErr, "raw-stderr") {
					t.Fatalf("unusable panel consumed script output: stdout=%q stderr=%q", forwardedOut, forwardedErr)
				}
			}
		}
		return
	}
	for _, lines := range []string{"-1", "2", "0"} {
		t.Run(lines, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			process := tmuxtest.StartPTYProcess(ctx, t, os.Args[0], []string{"-test.run=^TestProgressFollowsTerminalResize$"}, append(os.Environ(), "LIBTMUX_PROGRESS_RESIZE_HELPER=1", "LIBTMUX_PROGRESS_RESIZE_LINES="+lines))
			if err := process.Wait(ctx); err != nil {
				t.Fatalf("terminal resize helper: %v %q", err, process.Output())
			}
		})
	}
}

type progressFrameWriter struct {
	bytes.Buffer
	writes []string
}

func (w *progressFrameWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, string(data))
	return w.Buffer.Write(data)
}

func (w *progressFrameWriter) WriteString(data string) (int, error) {
	w.writes = append(w.writes, data)
	return w.Buffer.WriteString(data)
}
