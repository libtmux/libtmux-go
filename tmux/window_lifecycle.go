package tmux

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
)

// NewWindowRequest configures window creation on tmux 3.2a or later. Its zero
// value uses inherited defaults; placement behavior depends on the receiver.
// [Session.NewWindow] uses tmux's next free index. [Window.NewWindow] instead
// targets the receiver's occupied index and normally returns a command error;
// KillExisting may replace it. [Window.NewWindow] rejects Index. SelectExisting
// may run name-expansion probes before creation.
//
// Pointer fields are explicit when nonnil. Methods copy them and Environment
// before I/O; concurrent mutation during the copy is unsafe.
type NewWindowRequest struct {
	// Name is omitted when nil. A nonnil empty string remains an explicit
	// -n operand. The value is copied before tmux is called.
	Name *string
	// Attach lets the created or selected winlink become current in the target
	// session; false preserves its current window.
	Attach bool
	// Index selects an explicit nonnegative winlink index for
	// Session.NewWindow; nil uses the next free index.
	Index *int
	// StartDirectory expands ~ and ~/... for the current user. Named-user
	// forms such as ~other are rejected; empty inherits tmux's default.
	StartDirectory string
	// Command starts the window with this shell command; empty uses tmux's
	// default command.
	Command string
	// Environment is emitted in lexically sorted key order. The map is not
	// retained; nil and an empty map both add no entries.
	Environment map[string]string
	// Direction places the new winlink relative to the target. Its zero value
	// uses tmux's next free index with Session.NewWindow when Index is nil, but
	// keeps Window.NewWindow on the receiver's occupied target. Use
	// NewWindowDirectionAfter or NewWindowDirectionBefore for non-destructive
	// relative creation through Window.NewWindow.
	Direction NewWindowDirection
	// KillExisting asks tmux to destroy a window occupying the target index.
	KillExisting bool
	// SelectExisting asks tmux to select an existing expanded-name match instead
	// of creating another window. It requires Name.
	SelectExisting bool
}

// NewWindowDirection selects placement relative to the target winlink on tmux
// 3.2a or later. Its zero value lets [Session.NewWindow] with nil Index use the
// next free index, while [Window.NewWindow] keeps the receiver's exact occupied
// target.
type NewWindowDirection uint8

// Supported new-window placements.
const (
	// NewWindowDirectionDefault uses the next free index with Session.NewWindow
	// when Index is nil; Window.NewWindow instead keeps the receiver's exact
	// occupied target.
	NewWindowDirectionDefault NewWindowDirection = iota
	// NewWindowDirectionAfter places the new winlink after the target.
	NewWindowDirectionAfter
	// NewWindowDirectionBefore places the new winlink before the target.
	NewWindowDirectionBefore
)

// NewWindow creates a winlink in the receiver session. Attach selects it as
// that session's current window; it is not a guarantee about clients attached
// to other sessions. The returned [Window] is freshly materialized in the
// receiver's exact session context, including when its stable [WindowID] is
// linked elsewhere.
//
// SelectExisting no-output recovery is available only when Index is nil. It
// expands Name with tmux's version-specific rules and requires exactly one
// matching window name in the receiver session. An explicit Index has no such
// recovery. If tmux reports a WindowID before a transport or refresh failure,
// the partial result contains that ID and the receiver SessionID with Index -1.
// Other failures return zero; creation is not rolled back.
func (s Session) NewWindow(ctx context.Context, request NewWindowRequest) (Window, error) {
	request = captureNewWindowRequest(request)
	target := s.sessionID.String()
	if request.Index != nil {
		target += ":" + strconv.Itoa(*request.Index)
	}
	return newWindow(ctx, s.server, s.sessionID, target, request, request.Index == nil)
}

// NewWindow creates a window relative to the receiver's exact winlink. Set
// Direction to [NewWindowDirectionAfter] or [NewWindowDirectionBefore] for
// non-destructive relative creation. With the zero Direction, tmux targets the
// receiver's occupied index and normally returns a command error;
// KillExisting can destroy and replace that target. Attach changes the current
// window only in that target session. Index is rejected before execution
// because the receiver already supplies the target position. SelectExisting
// has no no-output recovery on this exact-target form; tmux must print the
// created WindowID.
//
// If tmux reports an ID before a transport or refresh failure, the partial result
// contains it and the receiver SessionID with Index -1. Other failures return
// zero; creation is not rolled back.
func (w Window) NewWindow(ctx context.Context, request NewWindowRequest) (Window, error) {
	request = captureNewWindowRequest(request)
	if request.Index != nil {
		return Window{}, invalidLifecycleRequest("Index cannot be used with Window.NewWindow")
	}
	target, err := exactWindowTarget(w)
	if err != nil {
		return Window{}, err
	}
	return newWindow(ctx, w.server, w.sessionID, target, request, false)
}

// newWindowArguments renders one new-window argument vector. It performs no
// I/O, so a [Plan] can render a window it has not created.
func newWindowArguments(
	target string,
	request NewWindowRequest,
) ([]string, error) {
	name := ""
	if request.Name != nil {
		name = *request.Name
	}
	if err := validateServerCommandArguments(
		"new-window",
		serverCommandArgument{field: "Name", value: name},
		serverCommandArgument{field: "StartDirectory", value: request.StartDirectory},
		serverCommandArgument{field: "Command", value: request.Command},
		serverCommandArgument{field: "Target", value: target},
	); err != nil {
		return nil, err
	}
	if request.Index != nil && *request.Index < 0 {
		return nil, invalidLifecycleRequest("Index must be nonnegative")
	}
	if request.Direction > NewWindowDirectionBefore {
		return nil, invalidLifecycleRequest("unsupported new window direction")
	}
	if request.SelectExisting && request.Name == nil {
		return nil, invalidLifecycleRequest("SelectExisting requires Name")
	}
	environment, err := lifecycleEnvironmentArguments(request.Environment)
	if err != nil {
		return nil, err
	}
	startDirectory, err := expandLifecycleDirectory(request.StartDirectory)
	if err != nil {
		return nil, err
	}

	arguments := []string{"new-window", "-t", target}
	if !request.Attach {
		arguments = append(arguments, "-d")
	}
	arguments = append(arguments, "-P")
	if startDirectory != "" {
		arguments = append(arguments, "-c"+startDirectory)
	}
	arguments = append(arguments, "-F#{window_id}")
	if request.Name != nil {
		arguments = append(arguments, "-n", name)
	}
	arguments = append(arguments, environment...)
	switch request.Direction {
	case NewWindowDirectionDefault:
	case NewWindowDirectionAfter:
		arguments = append(arguments, "-a")
	case NewWindowDirectionBefore:
		arguments = append(arguments, "-b")
	}
	if request.KillExisting {
		arguments = append(arguments, "-k")
	}
	if request.SelectExisting {
		arguments = append(arguments, "-S")
	}
	if request.Command != "" {
		arguments = append(arguments, request.Command)
	}
	return arguments, nil
}

func newWindow(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	target string,
	request NewWindowRequest,
	selectExistingEligible bool,
) (Window, error) {
	name := ""
	if request.Name != nil {
		name = *request.Name
	}
	if err := validateTypedTarget(
		"new-window", "Target", "session", sessionID.String(),
	); err != nil {
		return Window{}, err
	}
	// Rendered before the name probe below, so a request tmux would refuse is
	// refused without reading anything first.
	arguments, err := newWindowArguments(target, request)
	if err != nil {
		return Window{}, err
	}
	selectionName := ""
	if request.SelectExisting && selectExistingEligible {
		selectionName, err = newWindowSelectionName(ctx, server, target, name)
		if err != nil {
			return Window{}, err
		}
	}
	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		if identity, identityErr := lifecycleStableIdentity("window", result.Stdout); identityErr == nil {
			return Window{
				server: server, sessionID: sessionID,
				windowID: WindowID(identity), windowIndex: -1,
			}, err
		}
		return Window{}, err
	}
	result, err = requireRedactedLifecycleSuccess("new-window", result, nil)
	if err != nil {
		return Window{}, err
	}
	if len(result.Stdout) == 0 && request.SelectExisting && selectExistingEligible {
		return selectedExistingWindow(ctx, server, sessionID, selectionName)
	}
	identity, err := lifecycleStableIdentity("window", result.Stdout)
	if err != nil {
		return Window{}, err
	}
	predicted := Window{
		server: server, sessionID: sessionID,
		windowID: WindowID(identity), windowIndex: -1,
	}
	window, err := refreshCreatedWindow(ctx, server, sessionID, predicted.windowID)
	if err != nil {
		return predicted, err
	}
	return window, nil
}

func newWindowSelectionName(
	ctx context.Context,
	server Server,
	target string,
	name string,
) (string, error) {
	version, err := server.Version(ctx)
	if err != nil {
		return "", err
	}
	version34 := Version{raw: "3.4", major: 3, minor: 4}
	version37 := Version{raw: "3.7", major: 3, minor: 7}
	if !version.AtLeast(version34) {
		return name, nil
	}
	name, err = expandNewWindowSelectionName(ctx, server, target, name)
	if err != nil {
		return "", err
	}
	if !version.AtLeast(version37) {
		return name, nil
	}
	// tmux 3.7 added clean_name between two format expansions. For valid
	// window names its observable transformation is escaping backslashes.
	name = strings.ReplaceAll(name, `\`, `\\`)
	return expandNewWindowSelectionName(ctx, server, target, name)
}

func expandNewWindowSelectionName(
	ctx context.Context,
	server Server,
	target string,
	name string,
) (string, error) {
	result, err := server.literalCmd(ctx, "display-message", "-p", "-t", target, "-F", name)
	result, err = requireRedactedLifecycleSuccess("display-message", result, err)
	if err != nil {
		return "", err
	}
	if len(result.Stdout) != 1 {
		return "", fmt.Errorf(
			"%w: window name expansion printed %d lines",
			ErrInvalidCommandOutput,
			len(result.Stdout),
		)
	}
	return result.Stdout[0], nil
}

func selectedExistingWindow(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	name string,
) (Window, error) {
	windows, err := (Session{server: server, sessionID: sessionID}).SearchWindows(ctx, nil)
	if err != nil {
		return Window{}, err
	}
	matches := make([]Window, 0, 1)
	for _, window := range windows {
		windowName, ok := window.Name()
		if ok && windowName == name {
			matches = append(matches, window)
		}
	}
	if len(matches) != 1 {
		return Window{}, fmt.Errorf(
			"%w: selected window lookup returned %d matches",
			ErrInvalidCommandOutput,
			len(matches),
		)
	}
	return matches[0], nil
}

// Rename changes the stable window through the receiver's exact winlink and
// returns a canonical freshly materialized [Window]. The returned winlink may
// therefore use another session when the WindowID is linked. If the command
// succeeds but refresh fails, Rename returns the receiver with that error. A
// transport or context error can be delivery-ambiguous; no rollback is
// attempted.
func (w Window) Rename(ctx context.Context, name string) (Window, error) {
	if err := validateServerCommandArguments(
		"rename-window",
		serverCommandArgument{field: "Target", value: w.windowID.String()},
		serverCommandArgument{field: "Name", value: name},
	); err != nil {
		return Window{}, err
	}
	target, err := exactWindowTarget(w)
	if err != nil {
		return Window{}, err
	}
	result, err := w.server.literalCmd(ctx, "rename-window", "-t", target, name)
	if _, err = requireLifecycleSuccess("rename-window", result, err); err != nil {
		return Window{}, err
	}
	refreshed, err := w.Refresh(ctx)
	if err != nil {
		return w, err
	}
	return refreshed, nil
}

// Select makes the receiver's exact winlink current in its session. It does
// not promise focus for clients attached to other sessions. Select returns a
// canonical freshly materialized [Window], which may use another linked
// session for the same WindowID. If the command succeeds but refresh fails, it
// returns the receiver with that error. A transport or context error can be
// delivery-ambiguous; no rollback is attempted.
func (w Window) Select(ctx context.Context) (Window, error) {
	target, err := exactWindowTarget(w)
	if err != nil {
		return Window{}, err
	}
	result, err := w.server.literalCmd(ctx, "select-window", "-t", target)
	if _, err = requireLifecycleSuccess("select-window", result, err); err != nil {
		return Window{}, err
	}
	refreshed, err := w.Refresh(ctx)
	if err != nil {
		return w, err
	}
	return refreshed, nil
}

func captureNewWindowRequest(request NewWindowRequest) NewWindowRequest {
	request.Name = copyOptionalString(request.Name)
	request.Index = copyOptionalInt(request.Index)
	request.Environment = maps.Clone(request.Environment)
	return request
}
