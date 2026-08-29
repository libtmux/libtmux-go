package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/libtmux/libtmux-go/tmux"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options, environment, and hooks: the settings that explain a pane's
// behaviour when the pane itself does not.
//
// A client that reads a pane and finds nothing wrong is often looking at a
// setting: history-limit says why scrollback stopped, remain-on-exit says why
// a dead pane is still there, and a hook says why something happened that no
// tool here did. These are read because a client that cannot see them
// misdiagnoses what it can.
//
// Hooks are read-only. A hook is a command tmux runs on its own afterwards, so
// writing one is leaving something behind that outlives this connection and
// fires when nobody is watching; a person's tmux configuration is where that
// belongs. Options are writable because they are the ordinary way to make a
// pane behave, and they die with the object they are set on.

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
		value, set, err = t.tmux().RawOption(ctx, input.Name)
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
	output.Value = value
	output.Set = set
	return nil, output, nil
}

// setOptionInput writes one option.
type setOptionInput struct {
	// Name is the option to set.
	Name string `json:"name" jsonschema:"the tmux option name, such as history-limit"`
	// Value is what to set it to.
	Value string `json:"value" jsonschema:"the value to set"`
	// Scope is where to set it: server, session, window, or pane. Empty sets
	// it at pane scope, which is the narrowest and affects nothing else.
	Scope string `json:"scope,omitempty" jsonschema:"the scope to set at; empty sets at pane scope"`
	// PaneID, WindowID, and SessionName pick the object to set it on.
	PaneID string `json:"paneId,omitempty" jsonschema:"the pane to set the option on"`
	// WindowID picks the window for window scope.
	WindowID string `json:"windowId,omitempty" jsonschema:"the window to set the option on"`
	// SessionName picks the session for session scope, and resolves the others
	// when they are empty.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the session to set the option on"`
}

// setOptionOutput reports what was set.
type setOptionOutput struct {
	// Name is the option that was set.
	Name string `json:"name"`
	// Scope is where it was set.
	Scope string `json:"scope"`
	// Value is what it was set to.
	Value string `json:"value"`
}

// setOption writes one tmux option.
//
// Pane scope by default, because it is the narrowest: an option set there
// affects the pane and nothing else, where the same option set on the server
// changes every session a person has open. A client that meant the wider one
// says so.
func (t *tools) setOption(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input setOptionInput,
) (*mcp.CallToolResult, setOptionOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, setOptionOutput{}, errors.New("name is required")
	}
	scope, err := resolveScope(input.Scope)
	if err != nil {
		return nil, setOptionOutput{}, err
	}
	if err := scopeUses(scope, input.PaneID, input.WindowID); err != nil {
		return nil, setOptionOutput{}, err
	}
	output := setOptionOutput{Name: input.Name, Scope: scope, Value: input.Value}

	switch scope {
	case scopeServer:
		err = t.tmux().SetOption(ctx, input.Name, input.Value, tmux.SetOptionOptions{})
	case scopeSession:
		session, sessionErr := t.resolveSession(ctx, input.SessionName)
		if sessionErr != nil {
			return nil, output, sessionErr
		}
		err = session.SetOption(ctx, input.Name, input.Value, tmux.SetOptionOptions{})
	case scopeWindow:
		window, windowErr := t.resolveWindow(ctx, input.WindowID, input.SessionName)
		if windowErr != nil {
			return nil, output, windowErr
		}
		err = window.SetOption(ctx, input.Name, input.Value, tmux.SetOptionOptions{})
	default:
		pane, paneErr := t.resolvePane(ctx, input.PaneID, input.SessionName)
		if paneErr != nil {
			return nil, output, paneErr
		}
		err = pane.SetOption(ctx, input.Name, input.Value, tmux.SetOptionOptions{})
	}
	if err != nil {
		return nil, output, err
	}
	return nil, output, nil
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
	Name string `json:"name,omitempty" jsonschema:"one variable to read, with its value; empty lists every name with its scope and no values. Several values at once: put several of these in call_readonly_tools_batch"`
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
	// changing one needs to know which, because set_environment writes the
	// session's, which shadows the server's for that session alone.
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

// showEnvironment reads what new processes in a session will inherit.
//
// This is what a pane started later will see, which is not what a pane started
// earlier got: tmux hands each process the environment as it stood when the
// process began. A client debugging why a command cannot find something reads
// this to learn what the next pane would get.
//
// tmux keeps two layers and a pane inherits both, the session's overriding the
// server's, so both are read and merged. Reading only the session's answered a
// question nobody asked: PATH lives in the server's environment, so a caller
// asking what a pane would get was told it gets no PATH at all. Each entry
// says which layer it came from.
//
// A listing carries names without values. An environment is where people keep
// credentials, and a reply that hands every value to a model puts all of them
// somewhere they cannot be taken back from -- one call on a developer's machine
// returned eleven live API tokens. Naming a variable returns its value, because
// that is a caller asking for one thing rather than receiving everything.
//
// Redacting by name pattern was the alternative and is worse: TOKEN, KEY and
// SECRET catch the variables named after what they are, miss the ones named
// after what they belong to, and leave a caller believing the reply was
// filtered. A denylist that fails open is worse than none, because it is
// trusted. This fails closed and costs a caller who wants a value one argument.
//
// The names are the answer to most of what this is asked. Which variables a
// pane will inherit, whether one is set at all, and which layer it comes from
// are all in a listing; the value matters for the few a caller then names. The
// scope is what makes that work -- provenance is the one thing a name cannot
// be reasoned back to, and it is what "does the session override the server"
// turns on -- and several named reads go in one call_readonly_tools_batch, so
// wanting a handful of values costs one round trip rather than one each.
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
			if value, ok, err = t.tmux().GetEnvironment(ctx, wanted); err != nil {
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
	serverWide, err := t.tmux().ShowEnvironment(ctx)
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

// setEnvironmentInput writes a session's environment.
type setEnvironmentInput struct {
	// SessionName is the session to write. Empty writes the only one.
	SessionName string `json:"sessionName,omitempty" jsonschema:"the session to write; empty uses the only session"`
	// Name is the variable to set.
	Name string `json:"name" jsonschema:"the variable to set"`
	// Value is what to set it to. Ignored when Unset is true.
	Value string `json:"value,omitempty" jsonschema:"the value to set"`
	// Unset removes the variable instead of setting it.
	Unset bool `json:"unset,omitempty" jsonschema:"remove the variable instead of setting it"`
}

// setEnvironmentOutput reports what changed.
type setEnvironmentOutput struct {
	// SessionName is the session that was written.
	SessionName string `json:"sessionName"`
	// Name is the variable that changed.
	Name string `json:"name"`
	// Unset reports whether it was removed rather than set.
	Unset bool `json:"unset"`
}

// setEnvironment sets what new processes in a session will inherit.
//
// It changes nothing already running. A pane that is already open keeps the
// environment it started with, so a client setting a variable and then
// wondering why the pane cannot see it needs a new pane, or respawn_pane.
func (t *tools) setEnvironment(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input setEnvironmentInput,
) (*mcp.CallToolResult, setEnvironmentOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, setEnvironmentOutput{}, errors.New("name is required")
	}
	session, err := t.resolveSession(ctx, input.SessionName)
	if err != nil {
		return nil, setEnvironmentOutput{}, err
	}
	name, _ := session.Formats().SessionName()
	output := setEnvironmentOutput{SessionName: name, Name: input.Name, Unset: input.Unset}

	if input.Unset {
		if err := session.UnsetEnvironment(ctx, input.Name); err != nil {
			return nil, output, err
		}
		return nil, output, nil
	}
	if err := session.SetEnvironment(
		ctx, input.Name, input.Value, tmux.SetEnvironmentOptions{},
	); err != nil {
		return nil, output, err
	}
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

// showHooks reads the commands tmux will run on its own.
//
// A hook is the explanation for behaviour no tool here caused: a pane that
// closed itself, a window that renamed itself, a command that ran when
// something was split. A client debugging a session it did not configure has
// no other way to see them.
//
// Reading only. A hook written here would outlive this connection and fire
// when nothing is watching, which is a change to someone's tmux rather than to
// their session; that belongs in their configuration file.
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

	result, err := t.tmux().Cmd(ctx, arguments...)
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
func addSettingsTools(server *mcp.Server, t *tools) {
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "show_option",
		Annotations: readOnly("Read a tmux Option"),
		Description: "Read one tmux option at server, session, window, or pane " +
			"scope. Options explain behaviour a pane's contents do not: " +
			"history-limit is why scrollback stopped, remain-on-exit is why a " +
			"dead pane is still there.",
	}, t.showOption)
	register(server, t, CapabilityTmuxSettings, &mcp.Tool{
		Name:        "set_option",
		Annotations: settling("Set a tmux Option"),
		Description: "Set one tmux option. Pane scope by default, which affects " +
			"that pane and nothing else; server scope changes every session the " +
			"person has open.",
	}, t.setOption)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "show_environment",
		Annotations: readOnly("Read a Session's Environment"),
		Description: "What new processes in a session will inherit. Panes " +
			"already running keep the environment they started with, so this is " +
			"what the next pane gets rather than what the current one has.",
	}, t.showEnvironment)
	register(server, t, CapabilityTmuxSettings, &mcp.Tool{
		Name:        "set_environment",
		Annotations: settling("Set a Session's Environment"),
		Description: "Set or remove a variable for processes a session starts " +
			"from now on. It changes nothing already running.",
	}, t.setEnvironment)
	register(server, t, CapabilityContentRead, &mcp.Tool{
		Name:        "show_hooks",
		Annotations: readOnly("Read tmux Hooks"),
		Description: "The commands tmux will run on its own at a given scope. " +
			"This is the explanation for behaviour no tool here caused. Pass name " +
			"to ask about one hook rather than reading the whole table. Reading " +
			"only: a hook belongs in a person's tmux configuration, not in a " +
			"connection that will end.",
	}, t.showHooks)
}
