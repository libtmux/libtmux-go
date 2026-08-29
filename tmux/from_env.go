package tmux

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

const (
	tmuxEnvironmentVariable     = "TMUX"
	tmuxPaneEnvironmentVariable = "TMUX_PANE"
)

// ErrNotInsideTmux identifies missing or malformed tmux discovery variables.
var ErrNotInsideTmux = errors.New("tmux: not inside tmux")

// FromEnvError reports which tmux discovery variable was missing or malformed.
// It never retains or prints the variable's value.
type FromEnvError struct {
	// Variable is the discovery variable name, never its value.
	Variable string
	// Reason describes why the variable cannot identify a tmux resource.
	Reason string
}

// Error implements error.
func (e *FromEnvError) Error() string {
	return fmt.Sprintf("%v: $%s is %s", ErrNotInsideTmux, e.Variable, e.Reason)
}

// Unwrap makes FromEnvError compatible with ErrNotInsideTmux.
func (e *FromEnvError) Unwrap() error { return ErrNotInsideTmux }

// NewServerFromEnv returns a server configured from TMUX without starting tmux.
// It snapshots the process environment once. A nonnil environment supplies
// discovery values and overrides that snapshot for later tmux processes; an
// empty map therefore has no TMUX discovery value. Names follow the platform's
// environment rules and are case-insensitive on Windows.
func NewServerFromEnv(environment map[string]string) (Server, error) {
	discovery, effective, err := snapshotDiscoveryEnvironment(environment, os.Environ)
	if err != nil {
		return Server{}, err
	}
	return newServerFromEnvironmentSnapshot(discovery, effective)
}

func newServerFromEnvironmentSnapshot(
	discovery map[string]string,
	effective []string,
) (Server, error) {
	socketPath, err := socketPathFromEnvironment(discovery)
	if err != nil {
		return Server{}, err
	}
	dependencies := defaultServerDependencies()
	dependencies.environ = func() []string { return slices.Clone(effective) }
	return newServer(ServerOptions{
		SocketPath: socketPath,
	}, dependencies)
}

// PaneFromEnv returns the pane identified by TMUX and TMUX_PANE. Nil reads the
// process environment; a nonnil empty map does not. Environment names are
// case-insensitive on Windows. It resolves the live hierarchy later through
// identity-checked tmux queries.
func PaneFromEnv(ctx context.Context, environment map[string]string) (Pane, error) {
	pane, _, _, err := hierarchyFromEnvironment(ctx, environment)
	return pane, err
}

// WindowFromEnv returns the window containing the pane identified by the
// environment. Nil reads the process environment; a nonnil empty map does not.
func WindowFromEnv(ctx context.Context, environment map[string]string) (Window, error) {
	_, window, _, err := hierarchyFromEnvironment(ctx, environment)
	return window, err
}

// SessionFromEnv returns the canonical session containing the environment's
// pane. Nil reads the process environment; a nonnil empty map does not.
func SessionFromEnv(ctx context.Context, environment map[string]string) (Session, error) {
	_, _, session, err := hierarchyFromEnvironment(ctx, environment)
	return session, err
}

func hierarchyFromEnvironment(
	ctx context.Context,
	environment map[string]string,
) (Pane, Window, Session, error) {
	return hierarchyFromEnvironmentWithProcess(ctx, environment, os.Environ)
}

func hierarchyFromEnvironmentWithProcess(
	ctx context.Context,
	environment map[string]string,
	processEnvironment func() []string,
) (Pane, Window, Session, error) {
	discovery, effective, err := snapshotDiscoveryEnvironment(
		environment,
		processEnvironment,
	)
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	paneID, err := paneIDFromEnvironment(discovery)
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	server, err := newServerFromEnvironmentSnapshot(discovery, effective)
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	return discoverEnvironmentHierarchy(ctx, server, paneID)
}

func snapshotDiscoveryEnvironment(
	environment map[string]string,
	processEnvironment func() []string,
) (map[string]string, []string, error) {
	parent := slices.Clone(processEnvironment())
	if environment == nil {
		return environmentFromEntries(parent), parent, nil
	}
	overrides, err := processEnvironmentEntries(environment)
	if err != nil {
		return nil, nil, err
	}
	discovery, err := canonicalDiscoveryEnvironment(environment, processEnvironmentKey)
	if err != nil {
		return nil, nil, err
	}
	effective := parent
	effective = append(effective, overrides...)
	return discovery, effective, nil
}

func canonicalDiscoveryEnvironment(
	environment map[string]string,
	canonical func(string) string,
) (map[string]string, error) {
	normalized := make(map[string]string, len(environment))
	for _, name := range slices.Sorted(maps.Keys(environment)) {
		key := canonical(name)
		if _, exists := normalized[key]; exists {
			return nil, fmt.Errorf(
				"%w: environment contains duplicate names",
				ErrInvalidServerOptions,
			)
		}
		normalized[key] = environment[name]
	}
	return normalized, nil
}

func processEnvironmentEntries(environment map[string]string) ([]string, error) {
	names := slices.Sorted(maps.Keys(environment))
	entries := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || strings.ContainsRune(name, '=') ||
			!processEnvironmentNULAllowed &&
				(strings.ContainsRune(name, '\x00') ||
					strings.ContainsRune(environment[name], '\x00')) {
			return nil, fmt.Errorf(
				"%w: environment contains an invalid entry",
				ErrInvalidServerOptions,
			)
		}
		entries = append(entries, name+"="+environment[name])
	}
	return entries, nil
}

func discoverEnvironmentHierarchy(
	ctx context.Context,
	server Server,
	paneID PaneID,
) (Pane, Window, Session, error) {
	values, version, identity, err := server.livePaneFromEnvironment(ctx, paneID.String())
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	// One pane's row projects into all three kinds, so each is listed: the
	// pane's window and session are known, and are the only ones there are.
	snapshot, err := newSnapshotWithIdentity(server, version, snapshotRecords{
		sessions: []formatValues{values},
		windows:  []formatValues{values},
		panes:    []formatValues{values},
	}, listedSessions|listedWindows|listedPanes, &identity)
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	pane, err := snapshot.PaneByID(paneID)
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	window, ok := pane.Window()
	if !ok {
		return Pane{}, Window{}, Session{}, newSnapshotDecodeError(
			"pane",
			1,
			"window_id",
			pane.windowID.String(),
			"containing window was not materialized",
		)
	}
	session, ok := pane.Session()
	if !ok {
		return Pane{}, Window{}, Session{}, newSnapshotDecodeError(
			"pane",
			1,
			"session_id",
			pane.sessionID.String(),
			"containing session was not materialized",
		)
	}
	return pane, window, session, nil
}

func socketPathFromEnvironment(environment map[string]string) (string, error) {
	value, ok := environmentValue(environment, tmuxEnvironmentVariable)
	if !ok || value == "" {
		return "", fromEnvError(tmuxEnvironmentVariable, "unset or empty")
	}
	lastComma := strings.LastIndexByte(value, ',')
	if lastComma < 0 {
		return "", fromEnvError(tmuxEnvironmentVariable, "not a socket, pid, session triple")
	}
	secondComma := strings.LastIndexByte(value[:lastComma], ',')
	if secondComma < 1 {
		return "", fromEnvError(tmuxEnvironmentVariable, "not a socket, pid, session triple")
	}
	return value[:secondComma], nil
}

func paneIDFromEnvironment(environment map[string]string) (PaneID, error) {
	value, ok := environmentValue(environment, tmuxPaneEnvironmentVariable)
	if !ok || value == "" {
		return "", fromEnvError(tmuxPaneEnvironmentVariable, "unset or empty")
	}
	if value[0] != '%' {
		return "", fromEnvError(
			tmuxPaneEnvironmentVariable,
			"not a pane id (missing percent sigil)",
		)
	}
	return PaneID(value), nil
}

func environmentFromEntries(entries []string) map[string]string {
	environment := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			environment[processEnvironmentKey(name)] = value
		}
	}
	return environment
}

func environmentValue(environment map[string]string, name string) (string, bool) {
	if environment == nil {
		return os.LookupEnv(name)
	}
	value, ok := environment[processEnvironmentKey(name)]
	return value, ok
}

func fromEnvError(variable, reason string) *FromEnvError {
	return &FromEnvError{Variable: variable, Reason: reason}
}
