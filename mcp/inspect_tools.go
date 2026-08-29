package mcp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type snapshotPaneInput struct {
	PaneID         string `json:"paneId,omitempty" jsonschema:"the tmux pane id to describe; empty describes the active pane"`
	SessionName    string `json:"sessionName,omitempty" jsonschema:"which session's active pane to describe when paneId is empty"`
	IncludeHistory bool   `json:"includeHistory,omitempty" jsonschema:"read scrollback as well as the visible screen"`
	MaxLines       int    `json:"maxLines,omitempty" jsonschema:"how many lines to return at most, keeping the last ones"`
	MaxBytes       int    `json:"maxBytes,omitempty" jsonschema:"how many bytes to return at most, keeping the last lines"`
}

type snapshotPaneOutput struct {
	Lines      []string    `json:"lines"`
	Pane       paneSummary `json:"pane"`
	Dead       bool        `json:"dead"`
	ExitStatus *int        `json:"exitStatus,omitempty"`
	truncation
}

// snapshotPane returns pane state and a bounded capture in one tool call.
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

type searchPanesInput struct {
	Text              string `json:"text" jsonschema:"text to look for in each pane's contents"`
	Regex             bool   `json:"regex,omitempty" jsonschema:"read text as a regular expression"`
	MatchCase         bool   `json:"matchCase,omitempty" jsonschema:"require the capitalisation to match"`
	SessionName       string `json:"sessionName,omitempty" jsonschema:"search only this session's panes"`
	IncludeHistory    bool   `json:"includeHistory,omitempty" jsonschema:"search scrollback as well as the visible screens"`
	MaxMatchesPerPane int    `json:"maxMatchesPerPane,omitempty" jsonschema:"how many matching lines to report per pane"`
	MaxPanes          int    `json:"maxPanes,omitempty" jsonschema:"how many matching panes to report at most"`
}

// searchDefaults bound omitted counts and repeated matches.
const (
	searchDefaultMatchesPerPane = 20
	searchCeilingMatchesPerPane = 200
	searchDefaultPanes          = 20
	searchCeilingPanes          = 200
)

type matchedLine struct {
	Row  int    `json:"row"`
	Text string `json:"text"`
}

type paneMatch struct {
	Pane        paneSummary   `json:"pane"`
	Matches     []matchedLine `json:"matches"`
	MoreMatches int           `json:"moreMatches,omitempty"`
}

type searchPanesOutput struct {
	Panes     []paneMatch `json:"panes"`
	MorePanes int         `json:"morePanes,omitempty"`
}

// searchPanes returns matching lines with each pane to avoid follow-up captures.
func (t *tools) searchPanes(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input searchPanesInput,
) (*mcp.CallToolResult, searchPanesOutput, error) {
	if strings.TrimSpace(input.Text) == "" {
		return nil, searchPanesOutput{}, errors.New("text is required")
	}
	matcher, err := compileMatcher(input.Text, input.Regex, input.MatchCase)
	if err != nil {
		return nil, searchPanesOutput{}, err
	}
	perPane := clamp(input.MaxMatchesPerPane, searchDefaultMatchesPerPane, searchCeilingMatchesPerPane)
	paneLimit := clamp(input.MaxPanes, searchDefaultPanes, searchCeilingPanes)

	snapshot, err := t.tmux().Snapshot(ctx)
	if err != nil {
		return nil, searchPanesOutput{}, err
	}
	// Rejoin wrapped rows before matching.
	request := tmux.CapturePaneRequest{JoinWrapped: true}
	if input.IncludeHistory {
		request.Start = tmux.CaptureBoundary
	}

	server := t.tmux()
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
			// Ignore panes that disappear after the snapshot.
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

// compileMatcher defaults to literal text because terminal output commonly
// contains regular-expression syntax.
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

// clamp applies a default and an upper bound.
func clamp(value, fallback, ceiling int) int {
	if value <= 0 {
		return fallback
	}
	if value > ceiling {
		return ceiling
	}
	return value
}

type getPaneInfoInput struct {
	PaneID      string `json:"paneId,omitempty" jsonschema:"the tmux pane id to describe; empty describes the active pane"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's active pane to describe when paneId is empty"`
}

type getPaneInfoOutput struct {
	Pane         paneSummary `json:"pane"`
	Title        string      `json:"title"`
	Path         string      `json:"path"`
	PID          int         `json:"pid"`
	Dead         bool        `json:"dead"`
	ExitStatus   *int        `json:"exitStatus,omitempty"`
	Zoomed       bool        `json:"zoomed"`
	InMode       bool        `json:"inMode"`
	HistoryLines int         `json:"historyLines"`
	HistoryLimit int         `json:"historyLimit"`
}

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
	// Any tmux mode intercepts keys before the pane program receives them.
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

type getWindowInfoInput struct {
	WindowID    string `json:"windowId,omitempty" jsonschema:"the tmux window id to describe; empty describes the current window"`
	SessionName string `json:"sessionName,omitempty" jsonschema:"which session's current window to describe when windowId is empty"`
}

type getWindowInfoOutput struct {
	Window windowSummary `json:"window"`
	Layout string        `json:"layout"`
	Width  int           `json:"width"`
	Height int           `json:"height"`
	Zoomed bool          `json:"zoomed"`
	Panes  []paneSummary `json:"panes"`
}

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

type getSessionInfoInput struct {
	SessionName string `json:"sessionName,omitempty" jsonschema:"the exact session name; empty uses the only session"`
}

type getSessionInfoOutput struct {
	Session        sessionSummary  `json:"session"`
	Path           string          `json:"path"`
	Created        string          `json:"created"`
	ActiveWindowID string          `json:"activeWindowId"`
	Windows        []windowSummary `json:"windows"`
}

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
	slices.SortFunc(output.Windows, func(a, b windowSummary) int {
		return cmp.Compare(a.Index, b.Index)
	})
	return nil, output, nil
}

func addInspectTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "snapshot_pane",
		Annotations: readOnly("Snapshot a tmux Pane"),
		Description: "One pane's contents together with what it is, where it " +
			"sits, and whether its process has exited. Prefer this to " +
			"capture_pane followed by get_pane_info when one response is enough. " +
			"State and content are collected sequentially, not atomically.",
	}, t.snapshotPane)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "search_panes",
		Annotations: readOnly("Search tmux Panes"),
		Description: "Find which panes show some text, and what they showed. " +
			"Answers \"which pane has the failing test\" in one call, where " +
			"capturing each pane in turn costs one per pane.",
	}, t.searchPanes)
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "get_pane_info",
		Annotations: readOnly("Describe a tmux Pane"),
		Description: "One pane's state without its contents: what it runs, its " +
			"process id, whether that process has exited and with what status, " +
			"how much scrollback there is, and whether the pane is in a mode " +
			"that will eat the keys you send it.",
	}, t.getPaneInfo)
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "get_window_info",
		Annotations: readOnly("Describe a tmux Window"),
		Description: "One window's size, layout string, and panes. The layout " +
			"string is what select_layout accepts back, so a layout worth " +
			"keeping can be read here and applied later.",
	}, t.getWindowInfo)
	register(server, t, CapabilityMetadataRead, &mcp.Tool{
		Name:        "get_session_info",
		Annotations: readOnly("Describe a tmux Session"),
		Description: "One session's windows, working directory, and when it was " +
			"created.",
	}, t.getSessionInfo)
}
