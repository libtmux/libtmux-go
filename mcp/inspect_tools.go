package mcp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// snapshotPaneInput selects the pane to describe.
type snapshotPaneInput struct {
	// PaneID is the tmux pane id, such as %1. Empty describes the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to describe; empty describes the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to describe when paneId is empty"`
	// IncludeHistory reads scrollback as well as the visible screen.
	IncludeHistory bool `json:"includeHistory,omitempty" jsonschema:"read scrollback as well as the visible screen"`
	// MaxLines caps how many lines come back, keeping the last ones.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many lines to return at most, keeping the last ones"`
	// MaxBytes caps the reply's size, keeping the last lines.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

// snapshotPaneOutput is a pane's contents and its state together.
type snapshotPaneOutput struct {
	// Lines is the pane's contents.
	Lines []string `json:"lines"`
	// Pane is what the pane is, where it sits, and what it runs.
	Pane paneSummary `json:"pane"`
	// Dead reports whether the pane's process has exited.
	Dead bool `json:"dead"`
	// ExitStatus is that process's status, reported only when it has exited.
	ExitStatus *int `json:"exitStatus,omitempty"`
	// truncation reports what the bounds dropped.
	truncation
}

// snapshotPane reads a pane's contents and its state in one call.
//
// Reaching for capture_pane and then list_panes costs two round trips for one
// question, and the two answers can disagree because the pane may change
// between them. This reads both from one view of the server.
func (t *tools) snapshotPane(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input snapshotPaneInput,
) (*mcp.CallToolResult, snapshotPaneOutput, error) {
	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, snapshotPaneOutput{}, err
	}
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, snapshotPaneOutput{}, err
	}
	request := tmux.CapturePaneRequest{}
	if input.IncludeHistory {
		request.Start = tmux.CaptureBoundary
	}
	lines, err := pane.Capture(ctx, request)
	if err != nil {
		return nil, snapshotPaneOutput{}, err
	}
	kept, report := limits.apply(lines)

	formats := pane.Formats()
	dead, _ := formats.PaneDead()
	output := snapshotPaneOutput{
		Lines:      kept,
		Dead:       dead,
		Pane:       t.summarize(ctx, pane),
		truncation: report,
	}
	if status, ok := formats.PaneDeadStatus(); ok && dead {
		output.ExitStatus = &status
	}
	return textResult(kept), output, nil
}

// searchPanesInput describes the panes to find and what counts as a match.
type searchPanesInput struct {
	// Text is matched against each pane's contents. Matching ignores case
	// unless MatchCase is set, because a caller repeating a word it read in
	// prose rarely reproduces the terminal's capitalisation.
	Text string `json:"text" jsonschema:"text to look for in each pane's contents"`
	// Regex reads Text as a regular expression rather than as literal text.
	Regex bool `json:"regex,omitempty" jsonschema:"read text as a regular expression"`
	// MatchCase requires the capitalisation to match too.
	MatchCase bool `json:"matchCase,omitempty" jsonschema:"require the capitalisation to match"`
	// SessionName limits the search to one session's panes.
	SessionName string `json:"sessionName,omitempty" jsonschema:"search only this session's panes"`
	// IncludeHistory searches scrollback as well as each visible screen, which
	// finds what has scrolled away at the cost of reading far more.
	IncludeHistory bool `json:"includeHistory,omitempty" jsonschema:"search scrollback as well as the visible screens"`
	// MaxMatchesPerPane caps the matching lines reported for one pane, keeping
	// the last ones. Zero uses the server's default.
	MaxMatchesPerPane int `json:"maxMatchesPerPane,omitempty" jsonschema:"how many matching lines to report per pane"`
	// MaxPanes caps how many panes are reported. Zero uses the server's
	// default.
	MaxPanes int `json:"maxPanes,omitempty" jsonschema:"how many matching panes to report at most"`
}

// searchDefaults bound a search that did not choose. A pane full of a repeated
// message would otherwise answer a one-word question with its whole scrollback.
const (
	searchDefaultMatchesPerPane = 20
	searchCeilingMatchesPerPane = 200
	searchDefaultPanes          = 20
	searchCeilingPanes          = 200
)

// matchedLine is one line of a pane that matched.
type matchedLine struct {
	// Row is the line's position in what was read, counting from zero at the
	// first line searched.
	Row int `json:"row"`
	// Text is the line itself.
	Text string `json:"text"`
}

// paneMatch is one pane that matched, and what matched in it.
type paneMatch struct {
	// Pane is the pane, described as list_panes describes it.
	Pane paneSummary `json:"pane"`
	// Matches are the matching lines, the last ones when there were more.
	Matches []matchedLine `json:"matches"`
	// MoreMatches is how many further lines matched and were not reported.
	MoreMatches int `json:"moreMatches,omitempty"`
}

// searchPanesOutput reports the panes whose contents matched.
type searchPanesOutput struct {
	// Panes is every pane showing the text, with the lines that showed it.
	Panes []paneMatch `json:"panes"`
	// MorePanes is how many further panes matched and were not reported.
	MorePanes int `json:"morePanes,omitempty"`
}

// searchPanes finds the panes showing some text, and says what they showed.
//
// A client that knows what it is looking for but not where it is would
// otherwise capture every pane and search the results itself, which costs a
// call per pane and puts every pane's contents through the client.
//
// The matching lines come back with the panes. Reporting only which pane
// matched would leave a client to capture it to find out what it found, which
// is the second call this tool exists to avoid.
func (t *tools) searchPanes(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input searchPanesInput,
) (*mcp.CallToolResult, searchPanesOutput, error) {
	if strings.TrimSpace(input.Text) == "" {
		return nil, searchPanesOutput{}, fmt.Errorf("text is required")
	}
	matcher, err := compileMatcher(input.Text, input.Regex, input.MatchCase)
	if err != nil {
		return nil, searchPanesOutput{}, err
	}
	perPane := clamp(input.MaxMatchesPerPane, searchDefaultMatchesPerPane, searchCeilingMatchesPerPane)
	paneLimit := clamp(input.MaxPanes, searchDefaultPanes, searchCeilingPanes)

	snapshot, err := t.target.Snapshot(ctx)
	if err != nil {
		return nil, searchPanesOutput{}, err
	}
	// Wrapped lines are rejoined, so text that ran past the pane's width is
	// found rather than split across two rows neither of which contains it.
	request := tmux.CapturePaneRequest{JoinWrapped: true}
	if input.IncludeHistory {
		request.Start = tmux.CaptureBoundary
	}

	server := t.strict()
	socket := t.socketPath(ctx)
	caller := t.callerIdentityFor(ctx)
	matched := make([]paneMatch, 0)
	for _, pane := range snapshot.Panes() {
		if session := strings.TrimSpace(input.SessionName); session != "" {
			if name, _ := pane.Formats().SessionName(); name != session {
				continue
			}
		}
		live, err := server.Pane(ctx, pane.ID())
		if err != nil {
			// A pane that went away between the snapshot and the read is not
			// a failed search, and reporting it as one would make searching a
			// busy server fail more often than it answers.
			continue
		}
		lines, err := live.Capture(ctx, request)
		if err != nil {
			continue
		}
		hits := make([]matchedLine, 0, perPane)
		for row, line := range lines {
			if matcher(line) {
				hits = append(hits, matchedLine{Row: row, Text: line})
			}
		}
		if len(hits) == 0 {
			continue
		}
		found := paneMatch{Pane: summarizePane(pane, caller, socket)}
		if len(hits) > perPane {
			found.MoreMatches = len(hits) - perPane
			hits = hits[len(hits)-perPane:]
		}
		found.Matches = hits
		matched = append(matched, found)
	}

	output := searchPanesOutput{Panes: matched}
	if len(matched) > paneLimit {
		output.MorePanes = len(matched) - paneLimit
		output.Panes = matched[:paneLimit]
	}
	return nil, output, nil
}

// compileMatcher turns what a caller asked for into a test on one line.
//
// A literal search is the default because a caller repeating something it read
// on a terminal is quoting, not writing a pattern, and a path or a stack trace
// is full of characters a regular expression reads as syntax.
func compileMatcher(text string, asRegex, matchCase bool) (func(string) bool, error) {
	if asRegex {
		pattern := text
		if !matchCase {
			pattern = "(?i)" + pattern
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid regular expression: %w", text, err)
		}
		return compiled.MatchString, nil
	}
	if matchCase {
		return func(line string) bool { return strings.Contains(line, text) }, nil
	}
	folded := strings.ToLower(text)
	return func(line string) bool {
		return strings.Contains(strings.ToLower(line), folded)
	}, nil
}

// clamp resolves a caller's count against a default and a ceiling, on the same
// terms as the text bounds: zero means the default, and too much means the
// most this server will send.
func clamp(value, fallback, ceiling int) int {
	if value <= 0 {
		return fallback
	}
	if value > ceiling {
		return ceiling
	}
	return value
}

// getPaneInfoInput selects the pane to describe.
type getPaneInfoInput struct {
	// PaneID is the tmux pane id. Empty describes the active pane.
	PaneID string `json:"paneId,omitempty" jsonschema:"the tmux pane id to describe; empty describes the active pane"`
	// SessionName picks the session when PaneID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to describe when paneId is empty"`
}

// getPaneInfoOutput is one pane's state without its contents.
type getPaneInfoOutput struct {
	// Pane is what the pane is, where it sits, and what it runs.
	Pane paneSummary `json:"pane"`
	// Title is the pane's title, which a program may set.
	Title string `json:"title"`
	// Path is the pane's current working directory.
	Path string `json:"path"`
	// PID is the process tmux started in the pane.
	PID int `json:"pid"`
	// Dead reports whether that process has exited.
	Dead bool `json:"dead"`
	// ExitStatus is its status, reported only when it has exited.
	ExitStatus *int `json:"exitStatus,omitempty"`
	// Zoomed reports whether the pane is filling its window.
	Zoomed bool `json:"zoomed"`
	// InMode reports whether the pane is in copy mode or another mode, where
	// keys are read by tmux rather than by the program in the pane.
	InMode bool `json:"inMode"`
	// HistoryLines is how many lines of scrollback the pane holds.
	HistoryLines int `json:"historyLines"`
	// HistoryLimit is how many it will keep before discarding the oldest.
	HistoryLimit int `json:"historyLimit"`
}

// getPaneInfo answers what a pane is without reading what it shows.
//
// snapshot_pane answers this and the contents together, which is the right
// call when the contents are wanted. This is for when they are not: whether a
// process is still alive, how much scrollback there is to ask for, whether the
// pane is in a mode that will eat the keys a client is about to send.
func (t *tools) getPaneInfo(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input getPaneInfoInput,
) (*mcp.CallToolResult, getPaneInfoOutput, error) {
	pane, err := t.resolvePane(ctx, input.PaneID, input.SessionName)
	if err != nil {
		return nil, getPaneInfoOutput{}, err
	}
	formats := pane.Formats()
	title, _ := formats.PaneTitle()
	path, _ := formats.PaneCurrentPath()
	pid, _ := formats.PanePID()
	dead, _ := formats.PaneDead()
	zoomed, _ := formats.WindowZoomedFlag()
	// tmux reports the mode as a number rather than a flag, so any mode at all
	// is what a client needs to know: keys sent to a pane in one are read by
	// tmux rather than by the program in it.
	mode, _ := formats.PaneInMode()
	historyLines, _ := formats.HistorySize()
	historyLimit, _ := formats.HistoryLimit()
	output := getPaneInfoOutput{
		Pane:         t.summarize(ctx, pane),
		Title:        title,
		Path:         path,
		PID:          pid,
		Dead:         dead,
		Zoomed:       zoomed,
		InMode:       mode != 0,
		HistoryLines: historyLines,
		HistoryLimit: historyLimit,
	}
	if status, ok := formats.PaneDeadStatus(); ok && dead {
		output.ExitStatus = &status
	}
	return nil, output, nil
}

// getWindowInfoInput selects the window to describe.
type getWindowInfoInput struct {
	// WindowID is the tmux window id such as @1. Empty describes the current
	// window.
	WindowID string `json:"windowId,omitempty" jsonschema:"the tmux window id to describe; empty describes the current window"`
	// SessionName picks the session when WindowID is empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to describe when windowId is empty"`
}

// getWindowInfoOutput is one window's state and its panes.
type getWindowInfoOutput struct {
	// Window is what the window is and where it sits.
	Window windowSummary `json:"window"`
	// Layout is tmux's own description of how the panes are arranged, which
	// select_layout accepts back.
	Layout string `json:"layout"`
	// Width and Height are the window's size in cells.
	Width int `json:"width"`
	// Height is the window's height in cells.
	Height int `json:"height"`
	// Zoomed reports whether one pane is filling the window.
	Zoomed bool `json:"zoomed"`
	// Panes are the window's panes.
	Panes []paneSummary `json:"panes"`
}

// getWindowInfo answers what one window is and what is in it.
func (t *tools) getWindowInfo(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input getWindowInfoInput,
) (*mcp.CallToolResult, getWindowInfoOutput, error) {
	window, err := t.resolveWindow(ctx, input.WindowID, input.SessionName)
	if err != nil {
		return nil, getWindowInfoOutput{}, err
	}
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		return nil, getWindowInfoOutput{}, err
	}
	formats := window.Formats()
	layout, _ := formats.WindowLayout()
	width, _ := formats.WindowWidth()
	height, _ := formats.WindowHeight()
	zoomed, _ := formats.WindowZoomedFlag()

	caller := t.callerIdentityFor(ctx)
	socket := t.socketPath(ctx)
	summaries := make([]paneSummary, 0, len(panes))
	for _, pane := range panes {
		summaries = append(summaries, summarizePane(pane, caller, socket))
	}
	return nil, getWindowInfoOutput{
		Window: summarizeWindow(window, len(panes)),
		Layout: layout,
		Width:  width,
		Height: height,
		Zoomed: zoomed,
		Panes:  summaries,
	}, nil
}

// getSessionInfoInput selects the session to describe.
type getSessionInfoInput struct {
	// SessionName is the exact session name. Empty describes the only session,
	// and is refused when there is more than one.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the exact session name; empty uses the only session"`
}

// getSessionInfoOutput is one session's state and its windows.
type getSessionInfoOutput struct {
	// Session is what the session is.
	Session sessionSummary `json:"session"`
	// Path is the session's working directory, which new windows inherit.
	Path string `json:"path"`
	// Created is when the session was started, as tmux reports it.
	Created string `json:"created"`
	// ActiveWindowID is the window the session is showing.
	ActiveWindowID string `json:"activeWindowId"`
	// Windows are the session's windows.
	Windows []windowSummary `json:"windows"`
}

// getSessionInfo answers what one session is and what is in it.
func (t *tools) getSessionInfo(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input getSessionInfoInput,
) (*mcp.CallToolResult, getSessionInfoOutput, error) {
	session, err := t.resolveSession(ctx, input.SessionName)
	if err != nil {
		return nil, getSessionInfoOutput{}, err
	}
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		return nil, getSessionInfoOutput{}, err
	}
	formats := session.Formats()
	path, _ := formats.SessionPath()
	output := getSessionInfoOutput{
		Session: summarizeSession(session, len(windows)),
		Path:    path,
	}
	if created, ok := formats.SessionCreated(); ok {
		output.Created = created.UTC().Format("2006-01-02T15:04:05Z")
	}
	if active, err := session.ResolveActiveWindow(ctx); err == nil {
		output.ActiveWindowID = active.ID().String()
	}
	for _, window := range windows {
		panes, err := window.SearchPanes(ctx, nil)
		if err != nil {
			continue
		}
		output.Windows = append(output.Windows, summarizeWindow(window, len(panes)))
	}
	sort.Slice(output.Windows, func(i, j int) bool {
		return output.Windows[i].Index < output.Windows[j].Index
	})
	return nil, output, nil
}

// addInspectTools advertises the tools that describe what tmux holds.
func addInspectTools(server *mcp.Server, t *tools) {
	register(server, t, &mcp.Tool{
		Name:        "snapshot_pane",
		Annotations: readOnly("Snapshot a tmux Pane"),
		Description: "One pane's contents together with what it is, where it " +
			"sits, and whether its process has exited. Prefer this to " +
			"capture_pane followed by list_panes: one call, and the two halves " +
			"cannot disagree.",
	}, t.snapshotPane)
	register(server, t, &mcp.Tool{
		Name:        "search_panes",
		Annotations: readOnly("Search tmux Panes"),
		Description: "Find which panes show some text, and what they showed. " +
			"Answers \"which pane has the failing test\" in one call, where " +
			"capturing each pane in turn costs one per pane.",
	}, t.searchPanes)
	register(server, t, &mcp.Tool{
		Name:        "get_pane_info",
		Annotations: readOnly("Describe a tmux Pane"),
		Description: "One pane's state without its contents: what it runs, its " +
			"process id, whether that process has exited and with what status, " +
			"how much scrollback there is, and whether the pane is in a mode " +
			"that will eat the keys you send it.",
	}, t.getPaneInfo)
	register(server, t, &mcp.Tool{
		Name:        "get_window_info",
		Annotations: readOnly("Describe a tmux Window"),
		Description: "One window's size, layout string, and panes. The layout " +
			"string is what select_layout accepts back, so a layout worth " +
			"keeping can be read here and applied later.",
	}, t.getWindowInfo)
	register(server, t, &mcp.Tool{
		Name:        "get_session_info",
		Annotations: readOnly("Describe a tmux Session"),
		Description: "One session's windows, working directory, and when it was " +
			"created.",
	}, t.getSessionInfo)
}
