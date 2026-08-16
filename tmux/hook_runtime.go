package tmux

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// SetHooksOptions controls a bulk hook replacement. Its zero value preserves
// existing indices before applying the supplied sparse values.
type SetHooksOptions struct {
	// ClearExisting unsets every existing index before applying Values.
	ClearExisting bool
}

// SetHooksResult reports only side effects confirmed before return.
type SetHooksResult struct {
	// Cleared reports that all existing hook indices were confirmed cleared.
	Cleared bool
	// AppliedIndices is an owned ascending list of indices confirmed applied.
	AppliedIndices []int
}

// Hooks returns a freshly decoded, caller-owned view of known global
// session-scope hooks, including inherited values. A read failure is returned
// rather than answered with zero values.
func (s GlobalSessionScope) Hooks(ctx context.Context) (ServerHookValues, error) {
	values, err := readTypedOptionValues(
		ctx,
		s.server,
		[]string{"-g"},
		generatedHookDefinitions[:],
		generatedOptionScopeSession,
		true,
	)
	if err != nil {
		return ServerHookValues{}, err
	}
	return newServerHookValues(values), nil
}

// Hooks returns a freshly decoded, caller-owned view of known global window
// hooks, including defaults. A read failure is returned rather than answered
// with zero values; context errors propagate.
func (s GlobalWindowScope) Hooks(ctx context.Context) (WindowHookValues, error) {
	values, err := readTypedOptionValues(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedHookDefinitions[:],
		generatedOptionScopeWindow,
		true,
	)
	if err != nil {
		return WindowHookValues{}, err
	}
	return newWindowHookValues(values), nil
}

// Hooks returns a freshly decoded, caller-owned view of known hooks at this
// stable session target, including inherited values. A read failure is returned
// rather than answered with zero values.
func (s Session) Hooks(ctx context.Context) (SessionHookValues, error) {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return SessionHookValues{}, err
	}
	values, err := readTypedOptionValues(
		ctx,
		server,
		scope,
		generatedHookDefinitions[:],
		generatedOptionScopeSession,
		true,
	)
	if err != nil {
		return SessionHookValues{}, err
	}
	return newSessionHookValues(values), nil
}

// Hooks returns a freshly decoded, caller-owned view of known hooks at this
// exact window target, including inherited values. The receiver's exact linked
// session context controls tmux format evaluation. A read failure is returned
// rather than answered with zero values.
func (w Window) Hooks(ctx context.Context) (WindowHookValues, error) {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return WindowHookValues{}, err
	}
	values, err := readTypedOptionValues(
		ctx,
		server,
		scope,
		generatedHookDefinitions[:],
		generatedOptionScopeWindow,
		true,
	)
	if err != nil {
		return WindowHookValues{}, err
	}
	return newWindowHookValues(values), nil
}

// Hooks returns a freshly decoded, caller-owned view of known hooks at this
// exact pane target, including inherited values. The receiver's exact linked
// session context controls tmux format evaluation. A read failure is returned
// rather than answered with zero values.
func (p Pane) Hooks(ctx context.Context) (PaneHookValues, error) {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return PaneHookValues{}, err
	}
	values, err := readTypedOptionValues(
		ctx,
		server,
		scope,
		generatedHookDefinitions[:],
		generatedOptionScopePane,
		true,
	)
	if err != nil {
		return PaneHookValues{}, err
	}
	return newPaneHookValues(values), nil
}

// RawHook returns one exact global session-scope hook value. A successful
// string is caller-owned; ok reports presence. Targeted reads do not use list
// leniency, and completed failures return a secret-safe option error.
// An unindexed empty hook array is ambiguous; use Hooks for typed presence.
func (s GlobalSessionScope) RawHook(ctx context.Context, name string) (string, bool, error) {
	return rawOption(ctx, s.server, []string{"-g"}, name, true)
}

// RawHook returns one exact global window-hook value. A successful
// string is caller-owned; ok reports presence and completed failures are returned.
// An unindexed empty hook array is ambiguous; use Hooks for typed
// presence.
func (s GlobalWindowScope) RawHook(
	ctx context.Context,
	name string,
) (string, bool, error) {
	return rawOption(ctx, s.server, []string{"-g", "-w"}, name, true)
}

// RawHook returns one exact session hook value at this stable session target.
// A successful string is caller-owned; ok reports presence and completed
// failures are returned.
// An unindexed empty hook array is ambiguous; use Hooks for typed presence.
func (s Session) RawHook(ctx context.Context, name string) (string, bool, error) {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return "", false, err
	}
	return rawOption(ctx, server, scope, name, true)
}

// RawHook returns one exact window hook value at this exact window target. A
// successful string is caller-owned; the receiver's exact linked session
// context controls tmux format evaluation, and completed failures are returned.
func (w Window) RawHook(ctx context.Context, name string) (string, bool, error) {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return "", false, err
	}
	return rawOption(ctx, server, scope, name, true)
}

// RawHook returns one exact pane hook value at this exact pane target. A
// successful string is caller-owned; the receiver's exact linked session
// context controls tmux format evaluation, and completed failures are returned.
func (p Pane) RawHook(ctx context.Context, name string) (string, bool, error) {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return "", false, err
	}
	return rawOption(ctx, server, scope, name, true)
}

// SetHook stores a global session-scope hook without refreshing models.
// Completed failures are secret-safe option errors; cancellation does not prove
// tmux did not accept the mutation.
func (s GlobalSessionScope) SetHook(ctx context.Context, name string, command string) error {
	return changeHook(ctx, s.server, []string{"-g"}, generatedOptionScopeSession, name, command, "")
}

// SetHook stores a global window hook without refreshing models.
// Completed failures are secret-safe option errors; cancellation does not prove
// tmux did not accept the mutation.
func (s GlobalWindowScope) SetHook(ctx context.Context, name string, command string) error {
	return changeHook(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		command,
		"",
	)
}

// SetHook stores a session hook at this stable session target without refreshing
// models. Completed failures are secret-safe option errors; cancellation does
// not prove tmux did not accept the mutation.
func (s Session) SetHook(ctx context.Context, name string, command string) error {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeSession, name, command, "")
}

// SetHook stores a window hook at this exact window target without refreshing
// models. Completed failures are secret-safe option errors; cancellation does
// not prove tmux did not accept the mutation.
func (w Window) SetHook(ctx context.Context, name string, command string) error {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeWindow, name, command, "")
}

// SetHook stores a pane hook at this exact pane target without refreshing
// models. Completed failures are secret-safe option errors; cancellation does
// not prove tmux did not accept the mutation.
func (p Pane) SetHook(ctx context.Context, name string, command string) error {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopePane, name, command, "")
}

// AppendHook appends a global session-scope hook without refreshing models.
// Completed failures are secret-safe option errors; cancellation does not prove
// tmux did not accept the append.
func (s GlobalSessionScope) AppendHook(ctx context.Context, name string, command string) error {
	return changeHook(ctx, s.server, []string{"-g"}, generatedOptionScopeSession, name, command, "-a")
}

// AppendHook appends a command to a global window hook without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the append.
func (s GlobalWindowScope) AppendHook(ctx context.Context, name string, command string) error {
	return changeHook(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		command,
		"-a",
	)
}

// AppendHook appends a session hook at this stable session target without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the append.
func (s Session) AppendHook(ctx context.Context, name string, command string) error {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeSession, name, command, "-a")
}

// AppendHook appends a window hook at this exact window target without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the append.
func (w Window) AppendHook(ctx context.Context, name string, command string) error {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeWindow, name, command, "-a")
}

// AppendHook appends a pane hook at this exact pane target without refreshing
// models. Completed failures are secret-safe option errors; cancellation does
// not prove tmux did not accept the append.
func (p Pane) AppendHook(ctx context.Context, name string, command string) error {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopePane, name, command, "-a")
}

// UnsetHook removes every matching global session-scope hook index without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the unset.
func (s GlobalSessionScope) UnsetHook(ctx context.Context, name string) error {
	return changeHook(ctx, s.server, []string{"-g"}, generatedOptionScopeSession, name, "", "-u")
}

// UnsetHook removes every matching global window-hook index without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the unset.
func (s GlobalWindowScope) UnsetHook(ctx context.Context, name string) error {
	return changeHook(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		"",
		"-u",
	)
}

// UnsetHook removes every matching session hook index at this stable session
// target without refreshing models. Completed failures are secret-safe option
// errors; cancellation does not prove the unset was accepted.
func (s Session) UnsetHook(ctx context.Context, name string) error {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeSession, name, "", "-u")
}

// UnsetHook removes every matching window hook index at this exact window
// target without refreshing models. Completed failures are secret-safe option
// errors; cancellation does not prove the unset was accepted.
func (w Window) UnsetHook(ctx context.Context, name string) error {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeWindow, name, "", "-u")
}

// UnsetHook removes every matching pane hook index at this exact pane target
// without refreshing models. Completed failures are secret-safe option errors;
// cancellation does not prove the unset was accepted.
func (p Pane) UnsetHook(ctx context.Context, name string) error {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopePane, name, "", "-u")
}

// RunHook asks tmux to run one global hook directly. It intentionally performs
// no racy target preflight; stale targets and global execution context remain
// tmux-defined. Completed failures are secret-safe option errors; cancellation
// does not prove hook delivery or execution did not occur.
func (s GlobalSessionScope) RunHook(ctx context.Context, name string) error {
	return changeHook(ctx, s.server, []string{"-g"}, generatedOptionScopeSession, name, "", "-R")
}

// RunHook asks tmux to run one global window hook directly. It
// targets global window scope rather than a receiver, returns secret-safe option
// errors for completed failures, and cancellation does not prove hook delivery
// or execution did not occur.
func (s GlobalWindowScope) RunHook(ctx context.Context, name string) error {
	return changeHook(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		"",
		"-R",
	)
}

// RunHook asks tmux to run one session hook directly at this stable target. It
// intentionally performs no racy liveness preflight after validation. Completed
// failures are secret-safe option errors; cancellation does not prove execution did not occur.
func (s Session) RunHook(ctx context.Context, name string) error {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeSession, name, "", "-R")
}

// RunHook asks tmux to run one window hook directly at this exact target. The
// receiver's exact linked session context controls tmux format evaluation; no
// racy preflight is issued. Completed failures are secret-safe option errors;
// cancellation does not prove execution did not occur.
func (w Window) RunHook(ctx context.Context, name string) error {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopeWindow, name, "", "-R")
}

// RunHook asks tmux to run one pane hook directly at this exact target. The
// receiver's exact linked session context controls tmux format evaluation; no
// racy preflight is issued. Completed failures are secret-safe option errors;
// cancellation does not prove execution did not occur.
func (p Pane) RunHook(ctx context.Context, name string) error {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return err
	}
	return changeHook(ctx, server, scope, generatedOptionScopePane, name, "", "-R")
}

// SetHooks applies indexed global session-scope hook commands in ascending
// order. With ClearExisting it confirms clearing before applying sparse Values.
// It stops at the first failure without rollback; the returned result reports
// only confirmed partial progress and owns AppliedIndices. Cancellation may
// follow accepted commands and cannot disprove their delivery.
func (s GlobalSessionScope) SetHooks(
	ctx context.Context,
	name string,
	values SparseArray[string],
	options SetHooksOptions,
) (SetHooksResult, error) {
	return setHooks(ctx, s.server, []string{"-g"}, generatedOptionScopeSession, name, values, options)
}

// SetHooks applies indexed global window-hook commands in ascending
// order. With ClearExisting it confirms clearing before applying sparse Values,
// stops at the first failure without rollback, and reports confirmed progress.
// Cancellation may follow accepted commands and cannot disprove their delivery.
func (s GlobalWindowScope) SetHooks(
	ctx context.Context,
	name string,
	values SparseArray[string],
	options SetHooksOptions,
) (SetHooksResult, error) {
	return setHooks(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		values,
		options,
	)
}

// SetHooks applies indexed session hook commands in ascending order at this
// handle's stable session target. With ClearExisting it confirms clearing
// first, stops at the first failure without rollback, and reports confirmed
// partial progress. Cancellation may follow accepted commands and cannot
// disprove their delivery.
func (s Session) SetHooks(
	ctx context.Context,
	name string,
	values SparseArray[string],
	options SetHooksOptions,
) (SetHooksResult, error) {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return SetHooksResult{}, err
	}
	return setHooks(ctx, server, scope, generatedOptionScopeSession, name, values, options)
}

// SetHooks applies indexed window hook commands in ascending order at this
// handle's exact window target. With ClearExisting it confirms clearing first,
// stops at the first failure without rollback, and reports confirmed progress.
// Cancellation may follow accepted commands and cannot disprove their delivery.
func (w Window) SetHooks(
	ctx context.Context,
	name string,
	values SparseArray[string],
	options SetHooksOptions,
) (SetHooksResult, error) {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return SetHooksResult{}, err
	}
	return setHooks(ctx, server, scope, generatedOptionScopeWindow, name, values, options)
}

// SetHooks applies indexed pane hook commands in ascending order at this
// handle's exact pane target. With ClearExisting it confirms clearing first,
// stops at the first failure without rollback, and reports confirmed progress.
// Cancellation may follow accepted commands and cannot disprove their delivery.
func (p Pane) SetHooks(
	ctx context.Context,
	name string,
	values SparseArray[string],
	options SetHooksOptions,
) (SetHooksResult, error) {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return SetHooksResult{}, err
	}
	return setHooks(ctx, server, scope, generatedOptionScopePane, name, values, options)
}

func changeHook(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	command string,
	flag string,
) error {
	if err := validateServerCommandArgument(
		"set-hook", "Name", name, true,
	); err != nil {
		return err
	}
	if err := validateServerCommandArgument(
		"set-hook", "Command", command, true,
	); err != nil {
		return err
	}
	if err := preflightGeneratedMutation(
		ctx,
		server,
		"set-hook",
		name,
		generatedHookDefinitions[:],
		nil,
		generatedScope,
		false,
	); err != nil {
		return err
	}
	return runHookMutation(ctx, server, scope, name, command, flag)
}

func runHookMutation(
	ctx context.Context,
	server Server,
	scope []string,
	name string,
	command string,
	flag string,
) error {
	arguments := make([]string, 0, len(scope)+5)
	arguments = append(arguments, "set-hook")
	arguments = append(arguments, scope...)
	if flag != "" {
		arguments = append(arguments, flag)
	}
	arguments = append(arguments, "--", name)
	if flag != "-u" && flag != "-R" {
		arguments = append(arguments, command)
	}
	return runOptionMutation(ctx, server, arguments, name, false)
}

func setHooks(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	values SparseArray[string],
	options SetHooksOptions,
) (SetHooksResult, error) {
	entries := values.Entries()
	slices.SortFunc(entries, func(left, right SparseEntry[string]) int {
		switch {
		case left.Index < right.Index:
			return -1
		case left.Index > right.Index:
			return 1
		default:
			return 0
		}
	})
	if err := validateServerCommandArgument(
		"set-hook", "Name", name, true,
	); err != nil {
		return SetHooksResult{}, err
	}
	for _, entry := range entries {
		if entry.Index < 0 || entry.Index > math.MaxInt32 {
			return SetHooksResult{}, fmt.Errorf("%w: %d", ErrInvalidSparseIndex, entry.Index)
		}
		if err := validateServerCommandArgument(
			"set-hook", "Command", entry.Value, true,
		); err != nil {
			return SetHooksResult{}, err
		}
	}
	if !validUnindexedOptionBase(name) {
		return SetHooksResult{}, newLocalInvalidOptionError("set-hook", name)
	}
	if err := preflightGeneratedMutation(
		ctx,
		server,
		"set-hook",
		name,
		generatedHookDefinitions[:],
		nil,
		generatedScope,
		true,
	); err != nil {
		return SetHooksResult{}, err
	}
	result := SetHooksResult{AppliedIndices: make([]int, 0, len(entries))}
	if options.ClearExisting {
		if err := runHookMutation(ctx, server, scope, name, "", "-u"); err != nil {
			return result, err
		}
		result.Cleared = true
	}
	for _, entry := range entries {
		indexed := name + "[" + strconv.Itoa(entry.Index) + "]"
		if err := runHookMutation(ctx, server, scope, indexed, entry.Value, ""); err != nil {
			return result, err
		}
		result.AppliedIndices = append(result.AppliedIndices, entry.Index)
	}
	return result, nil
}

func validUnindexedOptionBase(name string) bool {
	if name == "" {
		return false
	}
	start := 0
	custom := name[0] == '@'
	if custom {
		if len(name) == 1 {
			return false
		}
		start = 1
	} else if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := start; index < len(name); index++ {
		value := name[index]
		if value == '-' {
			if index == start || previousHyphen || index == len(name)-1 {
				return false
			}
			previousHyphen = true
			continue
		}
		previousHyphen = false
		if value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || custom && value == '_' {
			continue
		}
		return false
	}
	return true
}
