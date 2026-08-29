package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
)

// attachCommandOutput preserves a recorded exit status when capture fails and
// reports why output is unavailable. A missing closing mark reads to screen end.
func (t *tools) attachCommandOutput(
	ctx context.Context,
	pane tmux.Pane,
	openedPath, closedPath string,
	limits bounds,
	output *runCommandOutput,
) error {
	processPane, err := t.processPane(ctx, pane)
	if err != nil {
		if t.runtime.isTerminalError(err) || isContextError(err) {
			return err
		}
		output.OutputUnavailable = err.Error()
		return nil
	}
	pane = processPane
	opened, err := readMark(openedPath)
	if err != nil {
		reason, reasonErr := t.markMissing(ctx, pane, err)
		if reasonErr != nil {
			return reasonErr
		}
		output.OutputUnavailable = reason
		return nil
	}
	now, err := readPaneState(ctx, pane)
	if err != nil {
		if t.runtime.isTerminalError(err) || isContextError(err) {
			return err
		}
		output.OutputUnavailable = err.Error()
		return nil
	}
	// Convert absolute marks to current grid rows; tmux renumbers trimmed history.
	request := tmux.CapturePaneRequest{
		Start:       tmux.CaptureLine(opened.row - now.historySize),
		JoinWrapped: true,
	}
	// rows is how many rows the command wrote, known only where the closing
	// mark bounded the read.
	rows := 0
	// The closing row belongs to the output only when the cursor stopped part
	// way along it. A cursor at column zero means the last line ended, so that
	// row holds whatever the shell drew next, and reading it returns a prompt
	// as though the command had printed one.
	if closed, err := readMark(closedPath); err == nil {
		end := closed.row
		if closed.column == 0 {
			end--
		}
		if closed.row < opened.row || closed.moved(opened) {
			// Renumbering invalidates the opening mark. Report loss only when
			// scrollback was erased rather than moved into history.
			output.LinesMissed = closed.erased(opened) || closed.row < opened.row
			request.Start = tmux.CaptureLine(-now.historySize)
			if end < 0 {
				output.Output = nil
				return nil
			}
			request.End = tmux.CaptureLine(end - now.historySize)
		} else {
			if end < opened.row {
				// The cursor finished where it started, so the command printed
				// nothing. That is an answer rather than a failure.
				output.Output = nil
				return nil
			}
			request.End = tmux.CaptureLine(end - now.historySize)
			rows = end - opened.row + 1
		}
	}
	lines, err := pane.Capture(ctx, request)
	if err != nil {
		if t.runtime.isTerminalError(err) || isContextError(err) {
			return err
		}
		output.OutputUnavailable = err.Error()
		return nil
	}
	// tmux collapses an all-blank capture, so recover its marked row count.
	if len(lines) == 0 && rows > 0 {
		lines = make([]string, rows)
	}
	// tmux before 3.4 keeps grid padding after rejoining wrapped rows.
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}
	// Grid movement can put the wrapper echo before captured output on any path.
	lines = afterTheWrapperEcho(lines, sourceScriptFor(openedPath))
	kept, report := limits.apply(lines)
	output.Output = kept
	output.truncation = report
	return nil
}

// moved reports grid renumbering at a stable pane size. Resize-induced history
// changes are not comparable.
func (f mark) moved(opened mark) bool {
	if f.width != opened.width || f.height != opened.height {
		return false
	}
	return f.historySize != opened.historySize && f.row <= opened.row
}

// erased reports scrollback loss only while pane dimensions remain comparable.
func (f mark) erased(opened mark) bool {
	return f.moved(opened) && f.historySize < opened.historySize
}

// afterTheWrapperEcho removes the sourced wrapper line, including an echo split
// across wrapped rows.
func afterTheWrapperEcho(lines []string, echo string) []string {
	// Compared without spaces. A wrapped row that breaks on one of the echo's
	// own spaces loses it to the padding trim, which cannot tell a space the
	// grid added from a space the shell wrote.
	compacted := make([]string, len(lines))
	joined := strings.Builder{}
	ends := make([]int, len(lines))
	for i, line := range lines {
		compacted[i] = withoutSpaces(line)
		joined.WriteString(compacted[i])
		ends[i] = joined.Len()
	}
	wanted := withoutSpaces(echo)
	last := -1
	if at := strings.LastIndex(joined.String(), wanted); at >= 0 {
		for i, end := range ends {
			if end >= at+len(wanted) {
				last = i
				break
			}
		}
	}
	// Drop only full-row echo suffixes; suffixes inside ordinary output remain.
	for i := last + 1; i < len(lines); i++ {
		row := compacted[i]
		if len(row) >= echoRemnant && strings.HasSuffix(wanted, row) {
			last = i
		}
	}
	if last < 0 {
		// A shell that did not echo leaves no wrapper line to remove.
		return lines
	}
	// Recovery-only blank rows precede the command output.
	rest := lines[last+1:]
	for len(rest) > 0 && rest[0] == "" {
		rest = rest[1:]
	}
	return rest
}

// echoRemnant is the shortest observed shell-redraw suffix, including its
// closing quote.
const echoRemnant = 6

// withoutSpaces is the form rows and the echo are compared in.
func withoutSpaces(text string) string {
	return strings.ReplaceAll(text, " ", "")
}

// sourceScriptFor rebuilds the line the wrapper typed into the pane, so what is
// looked for in the grid is what was sent rather than a second spelling of it.
func sourceScriptFor(openedPath string) string {
	return ". " + shellQuote(filepath.Join(filepath.Dir(openedPath), "script"))
}

// mark is where the pane's cursor stood when the wrapper recorded it.
type mark struct {
	// historySize is how much scrollback stood above the screen, kept apart
	// from the row because an erase can drop a line of history while the
	// cursor moves down one: the sum is unchanged and the erase invisible.
	historySize int
	// width and height are the pane's size, which says whether historySize is
	// comparable at all. tmux rewraps the scrollback when a pane changes
	// width and moves rows between it and the screen when the height changes,
	// so the count moves on its own with nothing erased.
	width, height int
	// row is an absolute position in tmux's grid, being the history size plus
	// the cursor's row, which does not move when tmux renumbers the grid.
	row int
	// column is the cursor's column. Zero means the line before it ended.
	column int
}

// markMissing translates an absent opening mark into a pane-level diagnostic;
// the wrapper never ran.
func (t *tools) markMissing(ctx context.Context, pane tmux.Pane, err error) (string, error) {
	if !errors.Is(err, os.ErrNotExist) {
		return err.Error(), nil
	}
	running := ""
	if fresh, freshErr := pane.Refresh(ctx); freshErr == nil {
		running, _ = fresh.Formats().PaneCurrentCommand()
	} else if t.runtime.isTerminalError(freshErr) || isContextError(freshErr) {
		return "", freshErr
	}
	return commandNeverRanReason(running), nil
}

func commandNeverRanReason(running string) string {
	if running == "" {
		return "the command recorded no start: nothing in the pane has read the " +
			"keys yet"
	}
	if runsAShell(running) {
		return fmt.Sprintf("the command recorded no start: the pane's %s has not "+
			"read the keys yet, which a shell that is still starting does late",
			running)
	}
	return fmt.Sprintf("the pane never ran the command: it is running %s, "+
		"which took the text as its own input rather than running it; "+
		"respawn_pane gives the pane a shell again", running)
}

// unstartedReason explains a job whose wrapper recorded no opening mark. It is
// empty once the wrapper has run, so a caller can tell a slow command from one
// the pane never took.
func unstartedReason(openedAt, running string) string {
	if _, err := readMark(openedAt); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return commandNeverRanReason(running)
}

// readMark parses one cursor position recorded by the wrapper.
func readMark(path string) (mark, error) {
	recorded, err := os.ReadFile(path)
	if err != nil {
		return mark{}, err
	}
	fields := strings.Fields(string(recorded))
	if len(fields) != 5 {
		return mark{}, fmt.Errorf("unreadable pane position %q", recorded)
	}
	numbers := make([]int, 0, len(fields))
	for _, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil {
			return mark{}, fmt.Errorf("unreadable pane position %q", recorded)
		}
		numbers = append(numbers, number)
	}
	return mark{
		historySize: numbers[0],
		row:         numbers[0] + numbers[1],
		column:      numbers[2],
		width:       numbers[3],
		height:      numbers[4],
	}, nil
}
