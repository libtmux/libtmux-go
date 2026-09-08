package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Setting tools expose option and environment changes. Hook inspection remains
// read-only because hooks execute commands after the originating call.

// scopeOption resolves which tmux scope a settings call means.
const (
	scopeServer  = "server"
	scopeSession = "session"
	scopeWindow  = "window"
	scopePane    = "pane"
)

// showOptionInput reads one option.
type showOptionInput struct {
	// Name is the option, such as "history-limit" or "remain-on-exit".
	Name string `json:"name" jsonschema:"the tmux option name, such as history-limit"`
	// Scope is where to read it: server, session, window, or pane. Empty reads
	// it at pane scope, where tmux's own inheritance means a pane option falls
	// back through window and session to the global value.
	Scope string `json:"scope,omitempty" jsonschema:"the scope to read at; empty reads at pane scope"`
	// PaneID, WindowID, and SessionName pick the object to read it on.
	PaneID string `json:"paneId,omitempty" jsonschema:"the pane to read the option on"`
	// WindowID picks the window for window scope.
	WindowID string `json:"windowId,omitempty" jsonschema:"the window to read the option on"`
	// SessionName picks the session for session scope, and resolves the
	// others when they are empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the session to read the option on"`
	// Effective reads the value the scope inherits when it sets none of its
	// own, which is where a global option such as mouse lives.
	Effective bool `json:"effective,omitempty" jsonschema:"include an inherited value"`
}

// showOptionOutput carries an option's value.
type showOptionOutput struct {
	// Name is the option that was read.
	Name string `json:"name"`
	// Scope is where it was read.
	Scope string `json:"scope"`
	// Value is what tmux reported.
	Value string `json:"value"`
	// Set reports whether the option had a value at all. An unset option and
	// one set to an empty string are different things, and only this
	// distinguishes them.
	Set bool `json:"set"`
	// Inherited reports a Value that came from the scope's global table rather
	// than from the object asked about, which only an effective read returns.
	// Set stays false for it, because the object itself still sets nothing.
	Inherited bool `json:"inherited,omitempty"`
}

// showOption reads one tmux option.
func (t *tools) showOption(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input showOptionInput,
) (*mcp.CallToolResult, showOptionOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, showOptionOutput{}, errors.New("name is required")
	}
	scope, err := resolveScope(input.Scope)
	if err != nil {
		return nil, showOptionOutput{}, err
	}
	if err := scopeUses(scope, input.PaneID, input.WindowID); err != nil {
		return nil, showOptionOutput{}, err
	}
	output := showOptionOutput{Name: input.Name, Scope: scope}

	var value string
	var set bool
	switch scope {
	case scopeServer:
		value, set, err = t.tmux(ctx).RawOption(ctx, input.Name)
	case scopeSession:
		session, sessionErr := t.resolveSession(ctx, input.SessionName)
		if sessionErr != nil {
			return nil, output, sessionErr
		}
		value, set, err = session.RawOption(ctx, input.Name)
	case scopeWindow:
		window, windowErr := t.resolveWindow(ctx, input.WindowID, input.SessionName)
		if windowErr != nil {
			return nil, output, windowErr
		}
		value, set, err = window.RawOption(ctx, input.Name)
	default:
		pane, paneErr := t.resolvePane(ctx, input.PaneID, input.SessionName)
		if paneErr != nil {
			return nil, output, paneErr
		}
		value, set, err = pane.RawOption(ctx, input.Name)
	}
	if err != nil {
		return nil, output, err
	}
	// tmux keeps a scope's own values and the global table it falls back to
	// apart, so a session that sets nothing reads empty for an option set with
	// `set -g`. Without this a caller can write mouse through set_mouse_enabled
	// and never read it back.
	if !set && input.Effective {
		inherited, inheritedSet, inheritedErr := t.inheritedOption(ctx, scope, input.Name)
		if inheritedErr != nil {
			return nil, output, inheritedErr
		}
		if inheritedSet {
			output.Value = inherited
			output.Inherited = true
			return nil, output, nil
		}
	}
	output.Value = value
	output.Set = set
	return nil, output, nil
}

// inheritedOption reads the global table a scope falls back to. Server options
// have no table above them, and a pane inherits window options rather than
// session ones, which is why a session option is only reachable at session
// scope.
func (t *tools) inheritedOption(
	ctx context.Context,
	scope string,
	name string,
) (string, bool, error) {
	switch scope {
	case scopeSession:
		return t.tmux(ctx).GlobalSessionScope().RawOption(ctx, name)
	case scopeWindow, scopePane:
		return t.tmux(ctx).GlobalWindowScope().RawOption(ctx, name)
	default:
		return "", false, nil
	}
}

// scopeUses reports whether a scope reads the target a caller named, so an
// argument the scope cannot use is refused rather than discarded.
//
// A caller who means a pane and writes session gets a session-wide answer, and
// nothing in the reply says the pane they named was thrown away. Refusing is
// the same choice resolving a target makes: refused rather than guessed.
func scopeUses(scope, paneID, windowID string) error {
	switch scope {
	case scopePane:
		if strings.TrimSpace(windowID) != "" {
			return fmt.Errorf("windowId is not read at %s scope; use scope "+
				"window, or drop windowId", scope)
		}
		return nil
	case scopeWindow:
		if strings.TrimSpace(paneID) != "" {
			return fmt.Errorf("paneId is not read at %s scope; use scope pane, "+
				"or drop paneId", scope)
		}
		return nil
	default:
		for name, value := range map[string]string{
			"paneId": paneID, "windowId": windowID,
		} {
			if strings.TrimSpace(value) != "" {
				return fmt.Errorf("%s is not read at %s scope; name the scope "+
					"that reads it, or drop %s", name, scope, name)
			}
		}
		return nil
	}
}

// resolveScope reads the scope a settings call named.
func resolveScope(requested string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "":
		return scopePane, nil
	case scopeServer:
		return scopeServer, nil
	case scopeSession:
		return scopeSession, nil
	case scopeWindow:
		return scopeWindow, nil
	case scopePane:
		return scopePane, nil
	default:
		return "", fmt.Errorf(
			"scope %q is not server, session, window, or pane", requested)
	}
}

// showEnvironmentInput reads a session's environment.
type showEnvironmentInput struct {
	// SessionName is the session to read. Empty reads the only one.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the session to read; empty uses the only session"`
	// Name reads one variable, with its value. Empty lists the names only.
	Name string `json:"name,omitempty" jsonschema:"one variable to read, with its value; empty lists every name with its scope and no values. Several values at once: put several of these in call_read_tools_batch"`
	// MaxLines and MaxBytes bound the listing, whose size belongs to the
	// environment rather than to the request.
	MaxLines int `json:"maxLines,omitempty" jsonschema:"how many variables to return at most"`
	// MaxBytes bounds the same listing by size.
	MaxBytes int `json:"maxBytes,omitempty" jsonschema:"how many bytes of listing to return at most"`
}

// environmentEntry is one variable in a session's environment.
type environmentEntry struct {
	// Name is the variable.
	Name string `json:"name"`
	// Value is what new processes will see. It is present when this variable
	// was asked for by name and absent from a listing, which carries names
	// alone.
	Value string `json:"value,omitempty"`
	// Removed reports that tmux will unset this variable for new processes
	// rather than set it, which is a thing tmux can be told to do and which no
	// value alone expresses.
	Removed bool `json:"removed,omitempty"`
	// Scope is the layer this value came from: "session" when the session sets
	// it, "server" when it comes from the server-wide environment. A caller
	// reading one needs to know which scope supplied it. A session value shadows
	// the server value for that session alone.
	Scope string `json:"scope"`
}

// The layers tmux resolves an inherited variable through, named in a reply so
// a caller knows which one it is looking at.
const (
	environmentScopeServer  = "server"
	environmentScopeSession = "session"
)

// showEnvironmentOutput carries a session's environment.
type showEnvironmentOutput struct {
	// SessionName is the session that was read.
	SessionName string `json:"sessionName"`
	// Variables are its environment entries, sorted by name.
	Variables []environmentEntry `json:"variables"`
	// ValuesWithheld reports that this reply lists names without values, which
	// is what a listing returns. Asking for one variable by name returns its
	// value.
	ValuesWithheld bool `json:"valuesWithheld,omitempty"`
	// truncation reports what the bounds dropped.
	truncation
}

// showEnvironment reports what newly created panes inherit. Listings merge
// server then session scope and withhold values; naming one variable returns it.
func (t *tools) showEnvironment(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input showEnvironmentInput,
) (*mcp.CallToolResult, showEnvironmentOutput, error) {
	session, err := t.resolveSession(ctx, input.SessionName)
	if err != nil {
		return nil, showEnvironmentOutput{}, err
	}
	name, _ := session.Formats().SessionName()
	output := showEnvironmentOutput{SessionName: name}

	if wanted := strings.TrimSpace(input.Name); wanted != "" {
		value, ok, err := session.GetEnvironment(ctx, wanted)
		if err != nil {
			return nil, output, err
		}
		scope := environmentScopeSession
		if !ok {
			if value, ok, err = t.tmux(ctx).GetEnvironment(ctx, wanted); err != nil {
				return nil, output, err
			}
			scope = environmentScopeServer
		}
		output.Variables = []environmentEntry{}
		if ok {
			output.Variables = []environmentEntry{{
				Name: wanted, Value: value.Value, Removed: value.Removed,
				Scope: scope,
			}}
		}
		return nil, output, nil
	}

	// The server's layer first, then the session's over the top of it, which is
	// the order tmux resolves them in for a new process.
	merged := map[string]environmentEntry{}
	serverWide, err := t.tmux(ctx).ShowEnvironment(ctx)
	if err != nil {
		return nil, output, err
	}
	for key, value := range serverWide {
		merged[key] = environmentEntry{
			Name: key, Value: value.Value, Removed: value.Removed,
			Scope: environmentScopeServer,
		}
	}
	sessionWide, err := session.ShowEnvironment(ctx)
	if err != nil {
		return nil, output, err
	}
	for key, value := range sessionWide {
		merged[key] = environmentEntry{
			Name: key, Value: value.Value, Removed: value.Removed,
			Scope: environmentScopeSession,
		}
	}
	listed := slices.Collect(maps.Values(merged))
	slices.SortFunc(listed, func(a, b environmentEntry) int {
		return strings.Compare(a.Name, b.Name)
	})
	// Names alone, and said so rather than left to be noticed.
	for index := range listed {
		listed[index].Value = ""
	}
	output.ValuesWithheld = true

	limits, err := resolveBounds(input.MaxLines, input.MaxBytes)
	if err != nil {
		return nil, showEnvironmentOutput{}, err
	}
	names := make([]string, 0, len(listed))
	for _, entry := range listed {
		names = append(names, entry.Name)
	}
	kept, report := limits.apply(names)
	output.Variables = listed[len(listed)-len(kept):]
	output.truncation = report
	return nil, output, nil
}

// showHooksInput reads the hooks in force.
type showHooksInput struct {
	// Scope is where to read them: server, session, window, or pane. Empty
	// reads them at pane scope.
	Scope string `json:"scope,omitempty" jsonschema:"the scope to read at; empty reads at pane scope"`
	// PaneID, WindowID, and SessionName pick the object to read them on.
	PaneID string `json:"paneId,omitempty" jsonschema:"the pane to read hooks on"`
	// WindowID picks the window for window scope.
	WindowID string `json:"windowId,omitempty" jsonschema:"the window to read hooks on"`
	// SessionName picks the session for session scope, and resolves the others
	// when they are empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the session to read hooks on"`
	// Name reports one hook rather than the table. A caller checking whether
	// something is hooked knows the name it is asking about, and the whole
	// table is most of a reply it will discard.
	Name string `json:"name,omitempty" jsonschema:"report only this hook, such as pane-died; empty reports every hook in force"`
}

// hook is one command tmux runs on its own.
type hook struct {
	// Name is the event, such as "pane-died" or "after-split-window".
	Name string `json:"name"`
	// Command is what tmux runs when it happens.
	Command string `json:"command"`
}

// showHooksOutput carries the hooks found.
type showHooksOutput struct {
	// Scope is where they were read.
	Scope string `json:"scope"`
	// Hooks are the hooks set there, sorted by name. Always an array: a scope
	// with none is something a caller iterates zero times rather than a key it
	// has to test for.
	Hooks []hook `json:"hooks"`
}

// showHooks reports commands tmux may run on future events. This server does
// not expose hook mutation.
func (t *tools) showHooks(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input showHooksInput,
) (*mcp.CallToolResult, showHooksOutput, error) {
	scope, err := resolveScope(input.Scope)
	if err != nil {
		return nil, showHooksOutput{}, err
	}
	if err := scopeUses(scope, input.PaneID, input.WindowID); err != nil {
		return nil, showHooksOutput{}, err
	}
	output := showHooksOutput{Scope: scope}

	// tmux reports hooks through show-hooks, whose output is one hook per line
	// as a name and the command it runs. The typed accessors cover the hooks
	// tmux documents; this reports whatever is actually set, including hooks a
	// version knows and this build's catalog does not.
	arguments := []string{"show-hooks"}
	switch scope {
	case scopeServer:
		arguments = append(arguments, "-g")
	case scopeSession:
		session, sessionErr := t.resolveSession(ctx, input.SessionName)
		if sessionErr != nil {
			return nil, output, sessionErr
		}
		arguments = append(arguments, "-t", session.ID().String())
	case scopeWindow:
		window, windowErr := t.resolveWindow(ctx, input.WindowID, input.SessionName)
		if windowErr != nil {
			return nil, output, windowErr
		}
		arguments = append(arguments, "-w", "-t", window.ID().String())
	default:
		pane, paneErr := t.resolvePane(ctx, input.PaneID, input.SessionName)
		if paneErr != nil {
			return nil, output, paneErr
		}
		arguments = append(arguments, "-p", "-t", pane.ID().String())
	}

	result, err := t.tmux(ctx).Cmd(ctx, arguments...)
	if err != nil {
		return nil, output, err
	}
	output.Hooks = make([]hook, 0, len(result.Stdout))
	for _, line := range result.Stdout {
		name, command, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found || name == "" {
			continue
		}
		// tmux indexes a hook that has several commands as name[0], name[1].
		// A caller naming the hook means all of them, so the index is not part
		// of what is compared.
		if input.Name != "" && input.Name != name && input.Name != hookBaseName(name) {
			continue
		}
		output.Hooks = append(output.Hooks, hook{Name: name, Command: command})
	}
	slices.SortFunc(output.Hooks, func(a, b hook) int {
		return strings.Compare(a.Name, b.Name)
	})
	return nil, output, nil
}

// hookBaseName strips the index tmux appends when one hook runs several
// commands, so that "pane-died" matches "pane-died[0]" as a caller means it to.
func hookBaseName(name string) string {
	if base, _, found := strings.Cut(name, "["); found {
		return base
	}
	return name
}

// addSettingsTools advertises the tools for options, environment, and hooks.
