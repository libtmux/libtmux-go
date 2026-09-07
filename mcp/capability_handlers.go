package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type emptyToolInput struct{}

type listWindowsCapabilityInput struct {
	Session string `json:"session,omitempty" jsonschema:"only windows in this session name"`
}

type sessionCapabilityInput struct {
	SessionID string `json:"session_id" jsonschema:"the session id, such as $1"`
}

type windowCapabilityInput struct {
	WindowID string `json:"window_id" jsonschema:"the window id, such as @1"`
}

type paneCapabilityInput struct {
	PaneID string `json:"pane_id" jsonschema:"the pane id, such as %1"`
}

type captureCapabilityInput struct {
	PaneID   string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	History  bool   `json:"history,omitempty" jsonschema:"include scrollback as well as the visible screen"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"maximum lines, keeping the newest"`
}

type captureSinceCapabilityInput struct {
	PaneID   string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"a cursor returned by an earlier capture"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"maximum new lines, keeping the newest"`
}

type searchPanesCapabilityInput struct {
	Pattern           string `json:"pattern" jsonschema:"bounded text or regular expression to search for"`
	Regex             bool   `json:"regex,omitempty" jsonschema:"treat pattern as a regular expression"`
	MaxMatchesPerPane int    `json:"max_matches_per_pane,omitempty" jsonschema:"maximum matching lines per pane"`
	MaxLines          int    `json:"max_lines,omitempty" jsonschema:"maximum matching panes to return"`
}

type findPaneCapabilityInput struct {
	WindowID string `json:"window_id" jsonschema:"the window id, such as @1"`
	Position string `json:"position" jsonschema:"top-left, top-right, bottom-left, or bottom-right"`
}

type waitForTextCapabilityInput struct {
	PaneID   string   `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Patterns []string `json:"patterns,omitempty" jsonschema:"text to wait for; any one ends the wait"`
	Stop     []string `json:"stop,omitempty" jsonschema:"failure text; any one ends the wait"`
	Regex    bool     `json:"regex,omitempty" jsonschema:"treat patterns and stops as regular expressions"`
	Timeout  float64  `json:"timeout,omitempty" jsonschema:"seconds to wait before giving up"`
	Cursor   string   `json:"cursor,omitempty" jsonschema:"a cursor returned by an earlier capture"`
	MaxLines int      `json:"max_lines,omitempty" jsonschema:"maximum observed lines to return"`
}

type tmuxVariablesCapabilityInput struct {
	Names []string `json:"names" jsonschema:"variable names matching [A-Za-z][A-Za-z0-9_]*"`
}

type tmuxVariablesCapabilityOutput struct {
	Values map[string]string `json:"values"`
}

type showOptionCapabilityInput struct {
	Name      string `json:"name" jsonschema:"the exact option name"`
	Scope     string `json:"scope,omitempty" jsonschema:"global, server, session, window, or pane"`
	Target    string `json:"target,omitempty" jsonschema:"the target required by session, window, and pane scopes"`
	Effective bool   `json:"effective,omitempty" jsonschema:"include an inherited value"`
}

type showEnvironmentCapabilityInput struct {
	Session string `json:"session,omitempty" jsonschema:"a session name; omit for the only session"`
}

type showHooksCapabilityInput struct {
	Scope  string `json:"scope,omitempty" jsonschema:"global, server, session, window, or pane"`
	Target string `json:"target,omitempty" jsonschema:"the target required by session, window, and pane scopes"`
	Name   string `json:"name,omitempty" jsonschema:"one hook name; omit to read all hooks in the scope"`
}

type readBatchOperation struct {
	Tool      string         `json:"tool" jsonschema:"the exact inspect tool name"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"that tool's arguments"`
}

type readBatchCapabilityInput struct {
	Operations []readBatchOperation `json:"operations" jsonschema:"up to sixteen inspect operations"`
	OnError    string               `json:"on_error,omitempty" jsonschema:"stop or continue; defaults to stop"`
}

type renameSessionCapabilityInput struct {
	SessionID string `json:"session_id" jsonschema:"the session id, such as $1"`
	NewName   string `json:"new_name" jsonschema:"the literal new session name"`
}

type renameWindowCapabilityInput struct {
	WindowID string `json:"window_id" jsonschema:"the window id, such as @1"`
	NewName  string `json:"new_name" jsonschema:"the literal new window name"`
}

type selectLayoutCapabilityInput struct {
	WindowID string `json:"window_id" jsonschema:"the window id, such as @1"`
	Layout   string `json:"layout" jsonschema:"a built-in tmux layout name"`
}

type resizeWindowCapabilityInput struct {
	WindowID string `json:"window_id" jsonschema:"the window id, such as @1"`
	Width    int    `json:"width,omitempty" jsonschema:"width in terminal cells; omit to retain it"`
	Height   int    `json:"height,omitempty" jsonschema:"height in terminal cells; omit to retain it"`
}

type resizePaneCapabilityInput struct {
	PaneID string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Width  int    `json:"width,omitempty" jsonschema:"width in terminal cells; omit to retain it"`
	Height int    `json:"height,omitempty" jsonschema:"height in terminal cells; omit to retain it"`
}

type moveWindowCapabilityInput struct {
	WindowID  string `json:"window_id" jsonschema:"the window id, such as @1"`
	SessionID string `json:"session_id" jsonschema:"the destination session id, such as $1"`
	Index     *int   `json:"index,omitempty" jsonschema:"a destination window index; omit for tmux's choice"`
}

type swapPaneCapabilityInput struct {
	PaneID      string `json:"pane_id" jsonschema:"one pane id, such as %1"`
	OtherPaneID string `json:"other_pane_id" jsonschema:"the other pane id, such as %2"`
}

type setPaneTitleCapabilityInput struct {
	PaneID string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Title  string `json:"title" jsonschema:"the literal title"`
}

type waitForChannelCapabilityInput struct {
	Channel    string  `json:"channel" jsonschema:"a server-wide tmux channel name"`
	Timeout    float64 `json:"timeout,omitempty" jsonschema:"seconds to wait before giving up"`
	DrainFirst bool    `json:"drain_first,omitempty" jsonschema:"consume a pending signal before waiting"`
}

type setEnabledCapabilityInput struct {
	Enabled bool `json:"enabled,omitempty" jsonschema:"whether the setting is enabled"`
}

type setHistoryLimitCapabilityInput struct {
	SessionID string `json:"session_id" jsonschema:"the session id, such as $1"`
	Lines     int    `json:"lines" jsonschema:"the nonnegative retained line count"`
}

type settingCapabilityOutput struct {
	Target  string `json:"target,omitempty"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
	Value   string `json:"value,omitempty"`
}

type createSessionCapabilityInput struct {
	SessionName    string `json:"session_name,omitempty" jsonschema:"a literal session name"`
	WindowName     string `json:"window_name,omitempty" jsonschema:"a literal first-window name"`
	StartDirectory string `json:"start_directory,omitempty" jsonschema:"an absolute literal start directory"`
	Width          int    `json:"width,omitempty" jsonschema:"initial width; supply with height"`
	Height         int    `json:"height,omitempty" jsonschema:"initial height; supply with width"`
}

type createWindowCapabilityInput struct {
	SessionID      string `json:"session_id" jsonschema:"the session id, such as $1"`
	WindowName     string `json:"window_name,omitempty" jsonschema:"a literal window name"`
	StartDirectory string `json:"start_directory,omitempty" jsonschema:"an absolute literal start directory"`
	Attach         bool   `json:"attach,omitempty" jsonschema:"make the new window active"`
	Direction      string `json:"direction,omitempty" jsonschema:"before or after"`
}

type splitWindowCapabilityInput struct {
	PaneID         string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Direction      string `json:"direction,omitempty" jsonschema:"below, above, left, or right"`
	Percent        int    `json:"percent,omitempty" jsonschema:"share of the split occupied by the new pane"`
	StartDirectory string `json:"start_directory,omitempty" jsonschema:"an absolute literal start directory"`
}

type respawnPaneCapabilityInput struct {
	PaneID         string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	StartDirectory string `json:"start_directory,omitempty" jsonschema:"an absolute literal start directory"`
}

type runShellCommandCapabilityInput struct {
	PaneID          string  `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Command         string  `json:"command" jsonschema:"the shell command run in the pane's interactive shell"`
	Timeout         float64 `json:"timeout,omitempty" jsonschema:"seconds to wait before giving up"`
	MaxLines        int     `json:"max_lines,omitempty" jsonschema:"maximum output lines, keeping the newest"`
	SuppressHistory bool    `json:"suppress_history,omitempty" jsonschema:"best-effort persistent history suppression"`
}

type runShellCommandCapabilityOutput struct {
	PaneID                  string   `json:"pane_id"`
	ResolvedPaneIDs         []string `json:"resolved_pane_ids"`
	ExitStatus              *int     `json:"exit_status,omitempty"`
	TimedOut                bool     `json:"timed_out"`
	Running                 string   `json:"running,omitempty"`
	Output                  []string `json:"output"`
	OutputUnavailable       string   `json:"output_unavailable,omitempty"`
	LinesMissed             bool     `json:"lines_missed,omitempty"`
	EffectiveTimeoutSeconds int      `json:"effective_timeout_seconds,omitempty"`
	TimeoutClamped          bool     `json:"timeout_clamped,omitempty"`
}

type sendKeysCapabilityInput struct {
	PaneID  string   `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Keys    []string `json:"keys" jsonschema:"key names or literal strings to send"`
	Literal bool     `json:"literal,omitempty" jsonschema:"send strings literally instead of as key names"`
}

type sendKeysCapabilityOutput struct {
	PaneID          string   `json:"pane_id"`
	ResolvedPaneIDs []string `json:"resolved_pane_ids"`
	Sent            int      `json:"sent"`
}

type sendKeysOperation struct {
	PaneID  string   `json:"pane_id"`
	Keys    []string `json:"keys"`
	Literal bool     `json:"literal,omitempty"`
}

type sendKeysBatchCapabilityInput struct {
	Operations []sendKeysOperation `json:"operations" jsonschema:"up to sixty-four ordered pane-input operations"`
	OnError    string              `json:"on_error,omitempty" jsonschema:"stop or continue; defaults to stop"`
}

type sendKeysBatchCapabilityResult struct {
	PaneID          string   `json:"pane_id"`
	ResolvedPaneIDs []string `json:"resolved_pane_ids"`
	Sent            int      `json:"sent"`
	Error           string   `json:"error,omitempty"`
}

type sendKeysBatchCapabilityOutput struct {
	Results   []sendKeysBatchCapabilityResult `json:"results"`
	Completed int                             `json:"completed"`
	Failed    int                             `json:"failed,omitempty"`
}

type pasteTextCapabilityInput struct {
	PaneID string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	Text   string `json:"text" jsonschema:"the literal text to paste"`
	Enter  bool   `json:"enter,omitempty" jsonschema:"append a newline that submits the text"`
}

type pasteTextCapabilityOutput struct {
	PaneID string `json:"pane_id"`
	Bytes  int    `json:"bytes"`
}

type setSynchronizePanesCapabilityInput struct {
	WindowID string `json:"window_id" jsonschema:"the window id, such as @1"`
	Enabled  bool   `json:"enabled,omitempty" jsonschema:"whether pane input is synchronized"`
}

type clearPaneScrollbackCapabilityOutput struct {
	PaneID string `json:"pane_id"`
}

type killPaneCapabilityInput struct {
	PaneID      string `json:"pane_id" jsonschema:"the pane id, such as %1"`
	ConfirmSelf bool   `json:"confirm_self,omitempty" jsonschema:"permit ending the pane this MCP process runs in"`
}

type killWindowCapabilityInput struct {
	WindowID    string `json:"window_id" jsonschema:"the window id, such as @1"`
	ConfirmSelf bool   `json:"confirm_self,omitempty" jsonschema:"permit ending the pane this MCP process runs in"`
}

type killSessionCapabilityInput struct {
	SessionID   string `json:"session_id" jsonschema:"the session id, such as $1"`
	ConfirmSelf bool   `json:"confirm_self,omitempty" jsonschema:"permit ending the pane this MCP process runs in"`
}

var tmuxVariableName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func (t *tools) catalogListSessions(ctx context.Context, request *sdk.CallToolRequest, _ emptyToolInput) (*sdk.CallToolResult, listSessionsOutput, error) {
	return t.listSessions(ctx, request, listSessionsInput{})
}

func (t *tools) catalogListWindows(ctx context.Context, request *sdk.CallToolRequest, input listWindowsCapabilityInput) (*sdk.CallToolResult, listWindowsOutput, error) {
	return t.listWindows(ctx, request, listWindowsInput{SessionName: input.Session})
}

func (t *tools) catalogListPanes(ctx context.Context, request *sdk.CallToolRequest, _ emptyToolInput) (*sdk.CallToolResult, listPanesOutput, error) {
	return t.listPanes(ctx, request, listPanesInput{})
}

func (t *tools) catalogGetServerInfo(ctx context.Context, request *sdk.CallToolRequest, _ emptyToolInput) (*sdk.CallToolResult, getServerInfoOutput, error) {
	return t.getServerInfo(ctx, request, getServerInfoInput{})
}

func (t *tools) catalogGetSessionInfo(ctx context.Context, request *sdk.CallToolRequest, input sessionCapabilityInput) (*sdk.CallToolResult, getSessionInfoOutput, error) {
	session, err := t.tmux(ctx).Session(ctx, tmux.SessionID(input.SessionID))
	if err != nil {
		return nil, getSessionInfoOutput{}, notFound(err, "session", input.SessionID, "list_sessions")
	}
	name, _ := session.Formats().SessionName()
	return t.getSessionInfo(ctx, request, getSessionInfoInput{SessionName: name})
}

func (t *tools) catalogGetWindowInfo(ctx context.Context, request *sdk.CallToolRequest, input windowCapabilityInput) (*sdk.CallToolResult, getWindowInfoOutput, error) {
	return t.getWindowInfo(ctx, request, getWindowInfoInput{WindowID: input.WindowID})
}

func (t *tools) catalogGetPaneInfo(ctx context.Context, request *sdk.CallToolRequest, input paneCapabilityInput) (*sdk.CallToolResult, getPaneInfoOutput, error) {
	return t.getPaneInfo(ctx, request, getPaneInfoInput{PaneID: input.PaneID})
}

func (t *tools) catalogCapturePane(ctx context.Context, request *sdk.CallToolRequest, input captureCapabilityInput) (*sdk.CallToolResult, capturePaneOutput, error) {
	return t.capturePane(ctx, request, capturePaneInput{
		PaneID: input.PaneID, IncludeHistory: input.History, MaxLines: input.MaxLines,
	})
}

func (t *tools) catalogCaptureSince(ctx context.Context, request *sdk.CallToolRequest, input captureSinceCapabilityInput) (*sdk.CallToolResult, captureSinceOutput, error) {
	return t.captureSince(ctx, request, captureSinceInput{
		PaneID: input.PaneID, Cursor: input.Cursor, MaxLines: input.MaxLines,
	})
}

func (t *tools) catalogSnapshotPane(ctx context.Context, request *sdk.CallToolRequest, input captureCapabilityInput) (*sdk.CallToolResult, snapshotPaneOutput, error) {
	return t.snapshotPane(ctx, request, snapshotPaneInput{
		PaneID: input.PaneID, IncludeHistory: input.History, MaxLines: input.MaxLines,
	})
}

func (t *tools) catalogSearchPanes(ctx context.Context, request *sdk.CallToolRequest, input searchPanesCapabilityInput) (*sdk.CallToolResult, searchPanesOutput, error) {
	return t.searchPanes(ctx, request, searchPanesInput{
		Text: input.Pattern, Regex: input.Regex, MaxMatchesPerPane: input.MaxMatchesPerPane,
		MaxPanes: input.MaxLines,
	})
}

func (t *tools) catalogFindPaneByPosition(ctx context.Context, _ *sdk.CallToolRequest, input findPaneCapabilityInput) (*sdk.CallToolResult, findPaneByPositionOutput, error) {
	position := strings.ToLower(strings.TrimSpace(input.Position))
	if position != "top-left" && position != "top-right" && position != "bottom-left" && position != "bottom-right" {
		return nil, findPaneByPositionOutput{}, fmt.Errorf("position %q is not a window corner", input.Position)
	}
	window, err := t.tmux(ctx).Window(ctx, tmux.WindowID(input.WindowID))
	if err != nil {
		return nil, findPaneByPositionOutput{}, notFound(err, "window", input.WindowID, "list_windows")
	}
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		return nil, findPaneByPositionOutput{}, err
	}
	if len(panes) == 0 {
		return nil, findPaneByPositionOutput{}, nil
	}
	best := panes[0]
	for _, candidate := range panes[1:] {
		if cornerBefore(position, readPaneGeometry(candidate), readPaneGeometry(best)) {
			best = candidate
		}
	}
	return nil, findPaneByPositionOutput{
		PaneID: best.ID().String(), Found: true, Geometry: readPaneGeometry(best),
	}, nil
}

func cornerBefore(position string, candidate, current paneGeometry) bool {
	candidateRight := candidate.Left + candidate.Width
	currentRight := current.Left + current.Width
	candidateBottom := candidate.Top + candidate.Height
	currentBottom := current.Top + current.Height
	switch position {
	case "top-left":
		return candidate.Top < current.Top || candidate.Top == current.Top && candidate.Left < current.Left
	case "top-right":
		return candidate.Top < current.Top || candidate.Top == current.Top && candidateRight > currentRight
	case "bottom-left":
		return candidateBottom > currentBottom || candidateBottom == currentBottom && candidate.Left < current.Left
	default:
		return candidateBottom > currentBottom || candidateBottom == currentBottom && candidateRight > currentRight
	}
}

func (t *tools) catalogWaitForText(ctx context.Context, request *sdk.CallToolRequest, input waitForTextCapabilityInput) (*sdk.CallToolResult, waitForTextOutput, error) {
	timeout := int(input.Timeout)
	if input.Timeout > 0 && timeout == 0 {
		timeout = 1
	}
	return t.waitForText(ctx, request, waitForTextInput{
		PaneID: input.PaneID, Patterns: input.Patterns, Stop: input.Stop,
		Regex: input.Regex, SinceEntry: input.Cursor != "", TimeoutSeconds: timeout,
		MaxLines: input.MaxLines,
	})
}

func (t *tools) catalogTmuxVariables(ctx context.Context, _ *sdk.CallToolRequest, input tmuxVariablesCapabilityInput) (*sdk.CallToolResult, tmuxVariablesCapabilityOutput, error) {
	if len(input.Names) == 0 || len(input.Names) > 32 {
		return nil, tmuxVariablesCapabilityOutput{}, errors.New("names must contain between 1 and 32 variables")
	}
	output := tmuxVariablesCapabilityOutput{Values: make(map[string]string, len(input.Names))}
	for _, name := range input.Names {
		if !tmuxVariableName.MatchString(name) {
			return nil, tmuxVariablesCapabilityOutput{}, fmt.Errorf("tmux variable %q is not a valid name", name)
		}
		result, err := t.tmux(ctx).Cmd(ctx, "display-message", "-p", "#{"+name+"}")
		if err != nil {
			return nil, tmuxVariablesCapabilityOutput{}, err
		}
		if len(result.Stdout) > 0 {
			output.Values[name] = result.Stdout[0]
		} else {
			output.Values[name] = ""
		}
	}
	return nil, output, nil
}

func (t *tools) catalogShowOption(ctx context.Context, request *sdk.CallToolRequest, input showOptionCapabilityInput) (*sdk.CallToolResult, showOptionOutput, error) {
	legacy, err := optionTargetInput(ctx, t, input.Scope, input.Target)
	if err != nil {
		return nil, showOptionOutput{}, err
	}
	legacy.Name = input.Name
	return t.showOption(ctx, request, legacy)
}

func optionTargetInput(ctx context.Context, t *tools, scope, target string) (showOptionInput, error) {
	normalized := strings.ToLower(strings.TrimSpace(scope))
	if normalized == "global" {
		normalized = scopeServer
	}
	input := showOptionInput{Scope: normalized}
	switch normalized {
	case "", scopePane:
		input.PaneID = target
	case scopeWindow:
		input.WindowID = target
	case scopeSession:
		if strings.HasPrefix(target, "$") {
			session, err := t.tmux(ctx).Session(ctx, tmux.SessionID(target))
			if err != nil {
				return showOptionInput{}, err
			}
			input.SessionName, _ = session.Formats().SessionName()
		} else {
			input.SessionName = target
		}
	case scopeServer:
	default:
		return showOptionInput{}, fmt.Errorf("scope %q is not server, session, window, or pane", scope)
	}
	return input, nil
}

func (t *tools) catalogShowEnvironment(ctx context.Context, request *sdk.CallToolRequest, input showEnvironmentCapabilityInput) (*sdk.CallToolResult, showEnvironmentOutput, error) {
	return t.showEnvironment(ctx, request, showEnvironmentInput{SessionName: input.Session})
}

func (t *tools) catalogShowHooks(ctx context.Context, request *sdk.CallToolRequest, input showHooksCapabilityInput) (*sdk.CallToolResult, showHooksOutput, error) {
	target, err := optionTargetInput(ctx, t, input.Scope, input.Target)
	if err != nil {
		return nil, showHooksOutput{}, err
	}
	return t.showHooks(ctx, request, showHooksInput{
		Scope: target.Scope, PaneID: target.PaneID, WindowID: target.WindowID,
		SessionName: target.SessionName, Name: input.Name,
	})
}

func (t *tools) catalogReadBatch(ctx context.Context, request *sdk.CallToolRequest, input readBatchCapabilityInput) (*sdk.CallToolResult, batchOutput, error) {
	calls := make([]batchCall, 0, len(input.Operations))
	for _, operation := range input.Operations {
		calls = append(calls, batchCall(operation))
	}
	return t.runReadBatch(ctx, request, batchInput{Calls: calls, OnError: input.OnError})
}

func (t *tools) catalogRenameSession(ctx context.Context, request *sdk.CallToolRequest, input renameSessionCapabilityInput) (*sdk.CallToolResult, renameSessionOutput, error) {
	session, err := t.tmux(ctx).Session(ctx, tmux.SessionID(input.SessionID))
	if err != nil {
		return nil, renameSessionOutput{}, err
	}
	name, _ := session.Formats().SessionName()
	return t.renameSession(ctx, request, renameSessionInput{
		SessionName: name,
		Name:        literalizeTmuxFormat(input.NewName),
	})
}

func (t *tools) catalogRenameWindow(ctx context.Context, request *sdk.CallToolRequest, input renameWindowCapabilityInput) (*sdk.CallToolResult, renameWindowOutput, error) {
	return t.renameWindow(ctx, request, renameWindowInput{
		WindowID: input.WindowID,
		Name:     literalizeTmuxFormat(input.NewName),
	})
}

func (t *tools) catalogSelectWindow(ctx context.Context, request *sdk.CallToolRequest, input windowCapabilityInput) (*sdk.CallToolResult, selectWindowOutput, error) {
	return t.selectWindow(ctx, request, selectWindowInput(input))
}

func (t *tools) catalogSelectPane(ctx context.Context, request *sdk.CallToolRequest, input paneCapabilityInput) (*sdk.CallToolResult, selectPaneOutput, error) {
	return t.selectPane(ctx, request, selectPaneInput(input))
}

func (t *tools) catalogSelectLayout(ctx context.Context, request *sdk.CallToolRequest, input selectLayoutCapabilityInput) (*sdk.CallToolResult, selectLayoutOutput, error) {
	return t.selectLayout(ctx, request, selectLayoutInput{WindowID: input.WindowID, Layout: input.Layout})
}

func (t *tools) catalogResizeWindow(ctx context.Context, request *sdk.CallToolRequest, input resizeWindowCapabilityInput) (*sdk.CallToolResult, resizeWindowOutput, error) {
	return t.resizeWindow(ctx, request, resizeWindowInput{WindowID: input.WindowID, Width: input.Width, Height: input.Height})
}

func (t *tools) catalogResizePane(ctx context.Context, request *sdk.CallToolRequest, input resizePaneCapabilityInput) (*sdk.CallToolResult, resizePaneOutput, error) {
	return t.resizePane(ctx, request, resizePaneInput{PaneID: input.PaneID, Width: input.Width, Height: input.Height})
}

func (t *tools) catalogMoveWindow(ctx context.Context, request *sdk.CallToolRequest, input moveWindowCapabilityInput) (*sdk.CallToolResult, moveWindowOutput, error) {
	session, err := t.tmux(ctx).Session(ctx, tmux.SessionID(input.SessionID))
	if err != nil {
		return nil, moveWindowOutput{}, err
	}
	name, _ := session.Formats().SessionName()
	return t.moveWindow(ctx, request, moveWindowInput{
		WindowID: input.WindowID, SessionName: name, Index: input.Index,
	})
}

func (t *tools) catalogSwapPane(ctx context.Context, request *sdk.CallToolRequest, input swapPaneCapabilityInput) (*sdk.CallToolResult, swapPaneOutput, error) {
	return t.swapPane(ctx, request, swapPaneInput{PaneID: input.PaneID, WithPaneID: input.OtherPaneID})
}

func (t *tools) catalogSetPaneTitle(ctx context.Context, request *sdk.CallToolRequest, input setPaneTitleCapabilityInput) (*sdk.CallToolResult, setPaneTitleOutput, error) {
	return t.setPaneTitle(ctx, request, setPaneTitleInput{
		PaneID: input.PaneID,
		Title:  literalizeTmuxFormat(input.Title),
	})
}

func (t *tools) catalogWaitForChannel(ctx context.Context, request *sdk.CallToolRequest, input waitForChannelCapabilityInput) (*sdk.CallToolResult, waitForChannelOutput, error) {
	timeout := int(input.Timeout)
	if input.Timeout > 0 && timeout == 0 {
		timeout = 1
	}
	return t.waitForChannel(ctx, request, waitForChannelInput{
		Channel: input.Channel, TimeoutSeconds: timeout,
	})
}

func (t *tools) catalogSignalChannel(ctx context.Context, request *sdk.CallToolRequest, input signalChannelInput) (*sdk.CallToolResult, signalChannelOutput, error) {
	return t.signalChannel(ctx, request, input)
}

func (t *tools) catalogSetMouseEnabled(ctx context.Context, _ *sdk.CallToolRequest, input setEnabledCapabilityInput) (*sdk.CallToolResult, settingCapabilityOutput, error) {
	value := booleanOption(input.Enabled)
	// mouse is a session option, so the server scope refuses it. The global
	// session scope is what `set -g mouse` writes, and it is what a caller
	// asking to turn mouse handling on for the server means.
	scope := t.tmux(ctx).GlobalSessionScope()
	if err := scope.SetOption(ctx, "mouse", value, tmux.SetOptionOptions{}); err != nil {
		return nil, settingCapabilityOutput{}, err
	}
	return nil, settingCapabilityOutput{Name: "mouse", Enabled: new(input.Enabled)}, nil
}

func (t *tools) catalogSetHistoryLimit(ctx context.Context, _ *sdk.CallToolRequest, input setHistoryLimitCapabilityInput) (*sdk.CallToolResult, settingCapabilityOutput, error) {
	if input.Lines < 0 {
		return nil, settingCapabilityOutput{}, errors.New("lines must not be negative")
	}
	session, err := t.tmux(ctx).Session(ctx, tmux.SessionID(input.SessionID))
	if err != nil {
		return nil, settingCapabilityOutput{}, err
	}
	value := strconv.Itoa(input.Lines)
	if err := session.SetOption(ctx, "history-limit", value, tmux.SetOptionOptions{}); err != nil {
		return nil, settingCapabilityOutput{}, err
	}
	return nil, settingCapabilityOutput{Target: input.SessionID, Name: "history-limit", Value: value}, nil
}

func booleanOption(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func (t *tools) catalogCreateSession(ctx context.Context, request *sdk.CallToolRequest, input createSessionCapabilityInput) (*sdk.CallToolResult, createSessionOutput, error) {
	return t.createSession(ctx, request, createSessionInput{
		Name:           literalizeTmuxFormat(input.SessionName),
		WindowName:     literalizeTmuxFormat(input.WindowName),
		StartDirectory: literalizeTmuxFormat(input.StartDirectory),
		Width:          input.Width,
		Height:         input.Height,
	})
}

func (t *tools) catalogCreateWindow(ctx context.Context, request *sdk.CallToolRequest, input createWindowCapabilityInput) (*sdk.CallToolResult, createWindowOutput, error) {
	session, err := t.tmux(ctx).Session(ctx, tmux.SessionID(input.SessionID))
	if err != nil {
		return nil, createWindowOutput{}, err
	}
	name, _ := session.Formats().SessionName()
	return t.createWindow(ctx, request, createWindowInput{
		SessionName:    name,
		Name:           literalizeTmuxFormat(input.WindowName),
		StartDirectory: literalizeTmuxFormat(input.StartDirectory),
		Attach:         input.Attach, Direction: input.Direction,
	})
}

func (t *tools) catalogSplitWindow(ctx context.Context, request *sdk.CallToolRequest, input splitWindowCapabilityInput) (*sdk.CallToolResult, splitWindowOutput, error) {
	return t.splitWindow(ctx, request, splitWindowInput{
		PaneID: input.PaneID, Direction: input.Direction, Percentage: input.Percent,
		StartDirectory: literalizeTmuxFormat(input.StartDirectory),
	})
}

func (t *tools) catalogRespawnPane(ctx context.Context, request *sdk.CallToolRequest, input respawnPaneCapabilityInput) (*sdk.CallToolResult, respawnPaneOutput, error) {
	return t.respawnPane(ctx, request, respawnPaneInput{
		PaneID:         input.PaneID,
		StartDirectory: literalizeTmuxFormat(input.StartDirectory),
	})
}

func literalizeTmuxFormat(value string) string {
	return strings.ReplaceAll(value, "#", "##")
}

func (t *tools) catalogRunShellCommand(ctx context.Context, request *sdk.CallToolRequest, input runShellCommandCapabilityInput) (*sdk.CallToolResult, runShellCommandCapabilityOutput, error) {
	timeout := int(input.Timeout)
	if input.Timeout > 0 && timeout == 0 {
		timeout = 1
	}
	result, output, err := t.runCommand(ctx, request, runCommandInput{
		PaneID: input.PaneID, Command: input.Command, TimeoutSeconds: timeout,
		MaxLines: input.MaxLines, SuppressHistory: input.SuppressHistory,
	})
	converted := runShellCommandCapabilityOutput{
		PaneID: output.PaneID, ResolvedPaneIDs: output.ResolvedPaneIDs,
		ExitStatus: output.ExitStatus, TimedOut: output.TimedOut,
		Running: output.Running, Output: output.Output, OutputUnavailable: output.OutputUnavailable,
		LinesMissed:             output.LinesMissed,
		EffectiveTimeoutSeconds: output.EffectiveTimeoutSeconds,
		TimeoutClamped:          output.TimeoutClamped,
	}
	if converted.Output == nil {
		converted.Output = []string{}
	}
	if converted.ResolvedPaneIDs == nil {
		converted.ResolvedPaneIDs = []string{}
	}
	return result, converted, err
}

func (t *tools) catalogSendKeys(ctx context.Context, request *sdk.CallToolRequest, input sendKeysCapabilityInput) (*sdk.CallToolResult, sendKeysCapabilityOutput, error) {
	result, output, err := t.sendKeysBatch(ctx, request, sendKeysBatchInput{
		PaneID: input.PaneID, Keys: input.Keys, Literal: input.Literal,
	}, "send_keys")
	return result, sendKeysCapabilityOutput{
		PaneID: output.PaneID, ResolvedPaneIDs: output.ResolvedPaneIDs, Sent: output.Sent,
	}, err
}

func (t *tools) catalogSendKeysBatch(ctx context.Context, request *sdk.CallToolRequest, input sendKeysBatchCapabilityInput) (*sdk.CallToolResult, sendKeysBatchCapabilityOutput, error) {
	if len(input.Operations) == 0 || len(input.Operations) > 64 {
		return nil, sendKeysBatchCapabilityOutput{}, errors.New("operations must contain between 1 and 64 calls")
	}
	onError, err := resolveOnError(input.OnError)
	if err != nil {
		return nil, sendKeysBatchCapabilityOutput{}, err
	}
	output := sendKeysBatchCapabilityOutput{Results: make([]sendKeysBatchCapabilityResult, 0, len(input.Operations))}
	for _, operation := range input.Operations {
		_, sent, callErr := t.sendKeysBatch(ctx, request, sendKeysBatchInput{
			PaneID: operation.PaneID, Keys: operation.Keys, Literal: operation.Literal,
		}, "send_keys_batch")
		row := sendKeysBatchCapabilityResult{
			PaneID: sent.PaneID, ResolvedPaneIDs: sent.ResolvedPaneIDs, Sent: sent.Sent,
		}
		if callErr != nil {
			row.Error = callErr.Error()
			output.Failed++
			output.Results = append(output.Results, row)
			if onError == onErrorStop {
				break
			}
			continue
		}
		output.Completed++
		output.Results = append(output.Results, row)
	}
	return nil, output, nil
}

func (t *tools) catalogPasteText(ctx context.Context, request *sdk.CallToolRequest, input pasteTextCapabilityInput) (*sdk.CallToolResult, pasteTextCapabilityOutput, error) {
	result, output, err := t.pasteText(
		ctx, request,
		pasteTextInput{PaneID: input.PaneID, Text: input.Text, Enter: input.Enter},
	)
	converted := pasteTextCapabilityOutput(output)
	return result, converted, err
}

func (t *tools) catalogSetSynchronizePanes(ctx context.Context, _ *sdk.CallToolRequest, input setSynchronizePanesCapabilityInput) (*sdk.CallToolResult, settingCapabilityOutput, error) {
	window, err := t.tmux(ctx).Window(ctx, tmux.WindowID(input.WindowID))
	if err != nil {
		return nil, settingCapabilityOutput{}, err
	}
	if err := window.SetOption(ctx, "synchronize-panes", booleanOption(input.Enabled), tmux.SetOptionOptions{}); err != nil {
		return nil, settingCapabilityOutput{}, err
	}
	return nil, settingCapabilityOutput{Target: input.WindowID, Name: "synchronize-panes", Enabled: new(input.Enabled)}, nil
}

func (t *tools) catalogClearPaneScrollback(ctx context.Context, request *sdk.CallToolRequest, input paneCapabilityInput) (*sdk.CallToolResult, clearPaneScrollbackCapabilityOutput, error) {
	pane, err := t.resolvePaneToWrite(ctx, request, input.PaneID, "", "clearing its scrollback")
	if err != nil {
		return nil, clearPaneScrollbackCapabilityOutput{}, err
	}
	if err := pane.ClearHistory(ctx, tmux.ClearHistoryRequest{}); err != nil {
		return nil, clearPaneScrollbackCapabilityOutput{}, err
	}
	return nil, clearPaneScrollbackCapabilityOutput{PaneID: pane.ID().String()}, nil
}

func (t *tools) catalogKillPane(ctx context.Context, request *sdk.CallToolRequest, input killPaneCapabilityInput) (*sdk.CallToolResult, killPaneOutput, error) {
	return t.killPane(ctx, request, killPaneInput(input))
}

func (t *tools) catalogKillWindow(ctx context.Context, request *sdk.CallToolRequest, input killWindowCapabilityInput) (*sdk.CallToolResult, killWindowOutput, error) {
	return t.killWindow(ctx, request, killWindowInput(input))
}

func (t *tools) catalogKillSession(ctx context.Context, request *sdk.CallToolRequest, input killSessionCapabilityInput) (*sdk.CallToolResult, killSessionOutput, error) {
	session, err := t.tmux(ctx).Session(ctx, tmux.SessionID(input.SessionID))
	if err != nil {
		return nil, killSessionOutput{}, err
	}
	name, _ := session.Formats().SessionName()
	return t.killSession(ctx, request, killSessionInput{SessionName: name, ConfirmSelf: input.ConfirmSelf})
}
