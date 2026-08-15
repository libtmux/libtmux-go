package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// NewServerFromEnv returns a server configured from TMUX without executing tmux.
// A nil environment reads the process environment; a non-nil empty map does not.
func NewServerFromEnv(environment map[string]string) (Server, error) {
	socketPath, err := socketPathFromEnvironment(environment)
	if err != nil {
		return Server{}, err
	}
	return NewServer(ServerOptions{SocketPath: socketPath}), nil
}

// PaneFromEnv returns the pane identified by TMUX and TMUX_PANE. Nil reads the
// process environment; a nonnil empty map does not. It resolves the live
// hierarchy later through identity-checked tmux queries.
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
	if environment == nil {
		environment = environmentFromEntries(processEnvironment())
	}
	server, err := NewServerFromEnv(environment)
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	paneID, err := paneIDFromEnvironment(environment)
	if err != nil {
		return Pane{}, Window{}, Session{}, err
	}
	return discoverEnvironmentHierarchy(ctx, server, paneID)
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
	snapshot, err := newSnapshotWithIdentity(server, version, snapshotRecords{
		sessions: []formatValues{values},
		windows:  []formatValues{values},
		panes:    []formatValues{values},
	}, &identity)
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
			environment[name] = value
		}
	}
	return environment
}

func environmentValue(environment map[string]string, name string) (string, bool) {
	if environment == nil {
		return os.LookupEnv(name)
	}
	value, ok := environment[name]
	return value, ok
}

func fromEnvError(variable, reason string) *FromEnvError {
	return &FromEnvError{Variable: variable, Reason: reason}
}
