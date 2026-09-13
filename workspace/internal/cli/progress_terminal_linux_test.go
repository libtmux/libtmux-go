//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
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
