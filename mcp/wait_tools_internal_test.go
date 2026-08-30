package mcp

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

type failingPaneObservation struct{ err error }

func (f failingPaneObservation) NextNotification(
	context.Context,
) (tmux.ControlNotification, error) {
	return tmux.ControlNotification{}, f.err
}

type onePaneNotification struct {
	notification tmux.ControlNotification
	read         bool
}

func (s *onePaneNotification) NextNotification(
	context.Context,
) (tmux.ControlNotification, error) {
	if s.read {
		return tmux.ControlNotification{}, errors.New("notification read twice")
	}
	s.read = true
	return s.notification, nil
}

func TestPaneWaitConsumesTransientNotificationPayload(t *testing.T) {
	patterns, err := compileNamedMatchers([]string{"TRANSIENT"}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	notification, err := tmux.ParseControlNotification(
		[]byte(`%output %1 TRANSIENT\015ERASED`),
	)
	if err != nil {
		t.Fatal(err)
	}
	source := &onePaneNotification{notification: notification}
	result := watchPane(
		t.Context(),
		source,
		tmux.PaneID("%1"),
		patterns,
		nil,
		0,
	)
	if result.err != nil || result.written != "TRANSIENT\nERASED" ||
		result.outcome != outcomeMatched || result.matched != "TRANSIENT" {
		t.Fatalf("watchPane() = (%q, %q, %q, %v), want transient stream match",
			result.written, result.outcome, result.matched, result.err)
	}
}

func TestPaneWaitDoesNotCallAStreamFailureIdle(t *testing.T) {
	want := errors.New("notification stream failed")
	result := watchPane(
		t.Context(),
		failingPaneObservation{err: want},
		tmux.PaneID("%1"),
		nil,
		nil,
		time.Minute,
	)
	if result.outcome != "" || !errors.Is(result.err, want) ||
		!errors.Is(result.err, errPaneObservationLost) {
		t.Fatalf("watchPane() = (%q, %v), want classified stream failure",
			result.outcome, result.err)
	}
}

func TestTheWrapperEchoIsDroppedWhereverItWraps(t *testing.T) {
	const echo = ". '/tmp/libtmux-mcp-run478228931/script'"
	for _, probe := range []struct {
		name  string
		lines []string
		echo  string
		want  []string
	}{
		{
			name:  "on one row",
			lines: []string{"$ " + echo, "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			name:  "wrapped inside the path",
			lines: []string{"prompt ❯ . '/tmp/libtmux-mcp-run478228931/scrip", "t'", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			name:  "wrapped before the closing quote",
			lines: []string{"prompt ❯ . '/tmp/libtmux-mcp-run478228931/script", "'", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			name:  "content above the echo goes too",
			lines: []string{"BEFORE", "$ " + echo, "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			// An interactive shell draws the line it read and then redraws it
			// beneath the prompt, so the echo is in the grid twice and only the
			// second one has the output after it.
			name:  "echoed once plainly and once under the prompt",
			lines: []string{"KEPT", echo, "prompt ❯ . '/tmp/libtmux-mcp-run478228931/scrip", "t'", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			// A wrapped echo can leave the grid's own blank rows behind it, and
			// a caller reading output[0] would get one of those.
			name:  "blank rows after the echo are the grid's",
			lines: []string{"$ " + echo, "", "", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			// The row can break on the echo's own space, and the padding trim
			// cannot tell that space from one the grid added.
			name:  "wrapped at the space the trim removes",
			lines: []string{"prompt ❯ .", "'/tmp/libtmux-mcp-run478228931/script'", "SURVIVES"},
			want:  []string{"SURVIVES"},
		},
		{
			// A redraw can be PARTIAL: its start overwritten, the prompt row
			// left with no marker on it, and only the tail of the path
			// surviving. Captured from a pane 30 columns wide.
			name: "the redraw was truncated to its tail",
			lines: []string{
				". '/tmp/libtmux-mcp-run2001894",
				"93/script'",
				"-project-workspace-examp.e-nam",
				"h/12438ff0-cc52-4716-a3e0-8982",
				"cript'",
				"S30b",
			},
			echo: ". '/tmp/libtmux-mcp-run200189493/script'",
			want: []string{"S30b"},
		},
		{
			// The same pane at 37, where the redraw completed. Both rows of the
			// second draw are present and the marker is on the prompt row.
			name: "the redraw completed",
			lines: []string{
				". '/tmp/libtmux-mcp-run3061711999/scr",
				"ipt'",
				"-project-workspace-example-name/12438",
				"ff0-cc52-4716-a3e0-89823f44d4e6/scrat",
				"chpad > . '/tmp/libtmux-mcp-run306171",
				"1999/script'",
				"R1b",
			},
			echo: ". '/tmp/libtmux-mcp-run3061711999/script'",
			want: []string{"R1b"},
		},
		{
			// Five characters in common is not a remnant. Output that happens
			// to end near the echo's tail has to survive, or the recovery eats
			// the answer it was called to rescue.
			name:  "output ending just short of the remnant floor survives",
			lines: []string{"$ " + echo, "ript'"},
			want:  []string{"ript'"},
		},
		{
			// Captured at width 49 on the 48->49 transition, reply and pane
			// from the same moment. No complete draw survives anywhere: the
			// first is gone and the prompt row carrying the second's start was
			// overwritten, leaving only the tail. The anchor this looked for
			// was never on the grid, so it returned everything -- including
			// the PREVIOUS command's output, which is the first row here.
			name: "no complete draw survives, only the tail",
			lines: []string{
				"H49b",
				"-project-workspace-example-name/12.38ff0-cc52-471",
				"x-mcp-run634218972/script'",
				"G49b",
			},
			echo: ". '/tmp/libtmux-mcp-run634218972/script'",
			want: []string{"G49b"},
		},
		{
			// Captured at width 48. Here the start row IS on the grid and the
			// path is intact, but it reads "> .. '" where the shell wrote
			// "> . '" -- so the anchor fails on corrupted text rather than on a
			// missing row, and lands in the same place by a second route. The
			// bare "%" is zsh's partial-line marker, which a caller would also
			// read as output.
			name: "the start row is present but corrupted",
			lines: []string{
				"H48d",
				"%",
				"-project-workspace-example-name/12438ff0-cc52-4716-a3e0-89823f44d4e6/scratchpad >.. '/tmp/libtmu",
				"mcp-run861979552/script'",
				"G48d",
			},
			echo: ". '/tmp/libtmux-mcp-run861979552/script'",
			want: []string{"G48d"},
		},
		{
			// The ugliest grid of the sweep and not a failure: starship's own
			// command leaked onto the pane as literal text. Six rows of shell
			// internals above the output, and the tail row still anchored it.
			name: "shell internals above an intact tail",
			lines: []string{
				"$(/usr/local/bin/starship prompt --terminal-width",
				"=\"$COLUMNS\" --keymap=\"${KEYMAP:-}\" --status=\"$STA",
				"RSHIP_CMD_STATUS\" --pipestatus=\"${STARSHIP_PIPE_S",
				"TATUS[*]}\" --cmd-duration=\"${STARSHIP_DURATION:-}",
				"\" --jobs=\"$STARSHIP_JOBS_COUNT\"). '/tmp/libtmux-m",
				"cp-run810943969/script'",
				"G49e",
			},
			echo: ". '/tmp/libtmux-mcp-run810943969/script'",
			want: []string{"G49e"},
		},
		{
			// The wrapper's file is always named the same, so the echo's last
			// characters never vary. rm prints exactly this, and a file called
			// transcript is all it takes -- the row ends in the echo's tail
			// without being any part of the echo.
			name:  "output that merely ends in the echo's tail survives",
			lines: []string{"$ " + echo, "start", "rm: cannot remove 'transcript'"},
			want:  []string{"start", "rm: cannot remove 'transcript'"},
		},
		{
			// The worse half: a collision below other output truncated
			// everything above it and returned a plausible partial answer.
			name:  "a collision mid-output takes nothing with it",
			lines: []string{"$ " + echo, "four", "postscript'", "tail-after"},
			want:  []string{"four", "postscript'", "tail-after"},
		},
		{
			name:  "no echo leaves the lines alone",
			lines: []string{"BEFORE", "SURVIVES"},
			want:  []string{"BEFORE", "SURVIVES"},
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			wanted := probe.echo
			if wanted == "" {
				wanted = echo
			}
			got := afterTheWrapperEcho(probe.lines, wanted)
			if !slices.Equal(got, probe.want) {
				t.Errorf("got %q, want %q", got, probe.want)
			}
		})
	}
}

func TestTheMarksSeeAGridThatMovedUnderThem(t *testing.T) {
	at := func(history, cursor, width int) mark {
		return mark{historySize: history, row: history + cursor, width: width, height: 24}
	}
	for _, probe := range []struct {
		name           string
		opened, closed mark
		moved, erased  bool
	}{
		{
			// printf '\033[2J\033[H'; echo K at width 41: the screen went into
			// the scrollback and the cursor came home by the same three rows.
			name: "clearing the screen pushes rows up", opened: at(11, 4, 41), closed: at(14, 1, 41),
			moved: true, erased: false,
		},
		{
			// The same command one column narrower, where the sums differ and
			// the ordinary arithmetic finds the output.
			name: "the same clear at a width that does not cancel", opened: at(25, 4, 38), closed: at(29, 1, 38),
			moved: false, erased: false,
		},
		{
			// printf '\033[3J'; echo S: history destroyed under the cursor.
			name: "erasing the scrollback drops rows", opened: at(1, 2, 80), closed: at(0, 3, 80),
			moved: true, erased: true,
		},
		{
			name: "a command that printed nothing", opened: at(11, 4, 41), closed: at(11, 4, 41),
			moved: false, erased: false,
		},
		{
			name: "ordinary output scrolling the pane", opened: at(11, 4, 41), closed: at(14, 20, 41),
			moved: false, erased: false,
		},
		{
			// tmux rewraps the scrollback on a width change and moves rows
			// between it and the screen on a height change, so the count falls
			// with nothing erased and nothing moved under the marks.
			name: "a pane that was resized", opened: at(78, 4, 80), closed: at(42, 1, 220),
			moved: false, erased: false,
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if got := probe.closed.moved(probe.opened); got != probe.moved {
				t.Errorf("moved = %t, want %t", got, probe.moved)
			}
			if got := probe.closed.erased(probe.opened); got != probe.erased {
				t.Errorf("erased = %t, want %t", got, probe.erased)
			}
		})
	}
}
