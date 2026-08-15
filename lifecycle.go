package tmux

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrInvalidRequest identifies lifecycle options rejected before execution.
	ErrInvalidRequest = errors.New("tmux: invalid lifecycle request")
	// ErrSessionExists identifies a named session that was not replaced.
	ErrSessionExists = errors.New("tmux: session already exists")
	// ErrInvalidCommandOutput identifies malformed identity output from tmux.
	ErrInvalidCommandOutput = errors.New("tmux: invalid lifecycle command output")
)

// HasSessionRequest selects exact or tmux-pattern session matching on tmux
// 3.2a or later. Its zero value is invalid because Target is required.
// Pattern is checked after local target validation and before execution; the
// request contains no retained caller-owned storage.
type HasSessionRequest struct {
	// Target is the required session name or tmux session pattern.
	Target string
	// Pattern lets tmux interpret Target as a pattern; false requires an exact
	// session name.
	Pattern bool
}

// SessionKillRequest configures kill-session on tmux 3.2a or later. Its zero
// value destroys the receiver session. AllExcept destroys other sessions
// instead, while ClearAlerts is non-destructive. AllExcept, ClearAlerts, and
// Group are mutually exclusive because tmux applies a hidden precedence when
// more than one mode is supplied; the package rejects those combinations
// before execution. The request contains no retained caller-owned storage.
// Group's compatibility behavior is documented on that field.
type SessionKillRequest struct {
	// AllExcept terminates every other session and detaches their clients while
	// preserving the receiver session.
	AllExcept bool
	// ClearAlerts clears bell, activity, and silence alerts in every window linked
	// to the receiver without destroying a session or detaching its clients.
	ClearAlerts bool
	// Group terminates every session in the receiver's group and detaches their
	// clients on tmux 3.7 or newer. Older versions warn synchronously and omit
	// the unsupported flag, so only the receiver is terminated.
	Group bool
}

// KillWindowRequest selects a window for [Session.KillWindow] on tmux 3.2a or
// later. A zero request kills the receiver session's current window, and Index
// is scoped to that session. Target is unrestricted tmux target syntax and may
// select a window in another session. Target and Index are mutually exclusive
// and are checked before execution. Pointer values are read during the call
// and are not retained; callers must not mutate them concurrently. A nil
// pointer omits its selector, while a nonnil pointer is explicit even when
// Target points to an empty string.
type KillWindowRequest struct {
	// Target is passed through as unrestricted tmux target syntax when nonnil.
	Target *string
	// Index selects a winlink index within the receiver session when nonnil.
	Index *int
}

// NewSessionRequest configures detached session creation on tmux 3.2a or
// later. Its zero value creates an automatically named detached session with
// tmux defaults. Zero Width and Height omit those flags; nonzero values must
// be between 1 and 65535. KillExisting requires Name. Local
// validation completes before tmux is mutated, except that a named request
// probes for an existing session and KillExisting may remove it before later
// creation fails.
//
// [Server.NewSession] copies Environment before validation and the existence
// probe, then retains none of the caller's request storage. The caller may
// mutate the map after the copy completes, but mutation during the copy is not
// race-safe. Foreground attach requires a stdio/control
// transport and is not represented by this blocking subprocess API. tmux
// consumes -D, -X, and -f only while attaching a client, including
// new-session -A, so this always-detached request does not expose them.
type NewSessionRequest struct {
	// Name selects the new session name; empty lets tmux generate one.
	Name string
	// KillExisting removes an existing session named Name before creation.
	KillExisting bool
	// StartDirectory expands ~ and ~/... for the current user. Named-user
	// forms such as ~other are rejected; empty inherits tmux's default.
	StartDirectory string
	// WindowName names the initial window; empty lets tmux choose.
	WindowName string
	// Width sets the detached session width; zero lets tmux choose.
	Width int
	// Height sets the detached session height; zero lets tmux choose.
	Height int
	// Environment is emitted in lexically sorted key order. The map is not
	// retained; nil and an empty map both add no entries.
	Environment map[string]string
	// Command starts the initial pane with this shell command; empty uses tmux's
	// default command.
	Command string
}

// newSessionArguments renders one new-session argument vector. It performs no
// tmux I/O, so a [Plan] can render a session it has not created. The existence
// probe KillExisting needs is the caller's, not this.
func newSessionArguments(request NewSessionRequest) ([]string, error) {
	if request.Width < 0 || request.Width > 65535 {
		return nil, invalidLifecycleRequest("Width must be between 1 and 65535")
	}
	if request.Height < 0 || request.Height > 65535 {
		return nil, invalidLifecycleRequest("Height must be between 1 and 65535")
	}
	if request.Name != "" {
		if err := validateLifecycleSessionName("name", request.Name); err != nil {
			return nil, err
		}
	}
	environment, err := lifecycleEnvironmentArguments(request.Environment)
	if err != nil {
		return nil, err
	}
	startDirectory, err := expandLifecycleDirectory(request.StartDirectory)
	if err != nil {
		return nil, err
	}

	arguments := []string{"new-session", "-P", "-F#{session_id}"}
	if request.Name != "" {
		arguments = append(arguments, "-s"+request.Name)
	}
	arguments = append(arguments, "-d")
	if startDirectory != "" {
		arguments = append(arguments, "-c", startDirectory)
	}
	if request.WindowName != "" {
		arguments = append(arguments, "-n", request.WindowName)
	}
	if request.Width != 0 {
		arguments = append(arguments, "-x", strconv.Itoa(request.Width))
	}
	if request.Height != 0 {
		arguments = append(arguments, "-y", strconv.Itoa(request.Height))
	}
	arguments = append(arguments, environment...)
	if request.Command != "" {
		arguments = append(arguments, request.Command)
	}
	return arguments, nil
}

// NewWindowRequest configures window creation on tmux 3.2a or later. Its zero
// value uses inherited defaults; placement behavior depends on the receiver.
// [Session.NewWindow] uses tmux's next free index. [Window.NewWindow] instead
// targets the receiver's occupied index and normally returns a command error;
// with KillExisting, tmux destroys and replaces that target if it is still
// occupied. Nil pointer fields omit their options; nonnil pointers are
// explicit, including an empty Name. Window.NewWindow rejects Index because
// its receiver already supplies the exact winlink. Other invalid values are
// rejected before creation; SelectExisting's name-expansion probes can run
// before the create command.
//
// [Session.NewWindow] and [Window.NewWindow] copy Name, Index, and Environment
// before validation or later probes and retain none of that storage. Mutation
// after the copy completes cannot affect the call, but mutation during the
// copy is not race-safe.
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

// PaneDirection selects where a tiled pane is created or moved on tmux 3.2a
// or later. Its zero value places the pane below the target.
type PaneDirection uint8

// Supported pane directions.
const (
	// PaneDirectionBelow places the pane below the target.
	PaneDirectionBelow PaneDirection = iota
	// PaneDirectionAbove places the pane above the target.
	PaneDirectionAbove
	// PaneDirectionRight places the pane to the right of the target.
	PaneDirectionRight
	// PaneDirectionLeft places the pane to the left of the target.
	PaneDirectionLeft
)

// SplitPaneRequest configures tiled pane creation on tmux 3.2a or later. Its
// zero value creates a detached pane below the exact target with tmux's
// default size and command. Nil pointer fields omit their options; nonnil
// pointers are explicit, including empty style or message strings. Size and
// Percentage are mutually exclusive. Invalid values are rejected before tmux
// is mutated.
//
// [Window.SplitPane] and [Pane.Split] copy every pointer and Environment
// before validation or a compatibility probe and retain none of that storage.
// Mutation after the copy completes cannot affect the call, but mutation
// during the copy is not race-safe. Empty and the style, border, message, and
// Keep group require tmux 3.7. On older supported versions each requested
// group synchronously reaches [WarningHandler] and is omitted. Empty and
// Command are checked for mutual exclusion after that probe, so an unsupported
// Empty can be omitted while Command still runs.
type SplitPaneRequest struct {
	// Attach lets the created pane become active in the exact target winlink;
	// false preserves its active pane.
	Attach bool
	// Direction selects the side of the target; zero means below.
	Direction PaneDirection
	// Size selects a nonnegative absolute pane size; nil omits it.
	Size *int
	// Percentage selects a size from 0 through 100; nil omits it. It is sent
	// as tmux's "-l N%", which every supported version accepts; the older -p
	// spelling is rejected by tmux 3.4.
	Percentage *int
	// StartDirectory expands ~ and ~/... for the current user. Named-user
	// forms such as ~other are rejected; empty inherits tmux's default.
	StartDirectory string
	// Command starts the pane with this shell command; empty uses tmux's default.
	Command string
	// FullWindow lets the new pane span the full window size.
	FullWindow bool
	// Zoom preserves the window's zoomed state after the split.
	Zoom bool
	// Environment is emitted in lexically sorted key order and is not retained;
	// nil and an empty map both add no entries.
	Environment map[string]string
	// Empty requests an empty pane on tmux 3.7 or later.
	Empty bool
	// Style sets the pane style on tmux 3.7 or later; nil omits it.
	Style *string
	// ActiveBorderStyle sets the active border style on tmux 3.7 or later; nil
	// omits it.
	ActiveBorderStyle *string
	// InactiveBorderStyle sets the inactive border style on tmux 3.7 or later;
	// nil omits it.
	InactiveBorderStyle *string
	// Message sets the pane message on tmux 3.7 or later; nil omits it.
	Message *string
	// Keep preserves the pane after its command exits on tmux 3.7 or later.
	Keep bool
}

// HasSession reports whether the configured tmux server has a matching
// session without changing it. Pattern false performs an exact match; Pattern
// true preserves tmux's session-pattern semantics. Completed nonzero exits are
// predicate misses. Local validation errors match
// [ErrInvalidServerCommandRequest] or [ErrInvalidRequest]; transport and
// context failures remain errors.
func (s Server) HasSession(ctx context.Context, request HasSessionRequest) (bool, error) {
	if err := validateServerCommandArgument(
		"has-session", "Target", request.Target, true,
	); err != nil {
		return false, err
	}
	if err := validateLifecycleSessionName("target", request.Target); err != nil {
		return false, err
	}
	target := request.Target
	if !request.Pattern {
		target = "=" + target
	}
	result, err := s.literalCmd(ctx, "has-session", "-t", target)
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

// NewSession creates a detached session, then returns a newly materialized
// live [Session]. It never changes client focus. KillExisting may destroy the
// old named session before a later creation failure; no rollback is
// attempted. A transport or context error can be delivery-ambiguous.
//
// The returned Session is a session-only point lookup, so its [Session.Windows]
// and [Session.Panes] relations are empty even though tmux creates an initial
// window and pane. Use [Session.ResolveActiveWindow], [Session.ResolveActivePane],
// or [Server.Snapshot] when those related records are needed.
//
// When tmux prints a valid stable identity before a transport failure, or
// creation succeeds but the live lookup fails, NewSession returns a partial
// Session containing its creating [Server] and [SessionID] with the error.
// Other failures return a zero Session. See [ErrSessionExists],
// [ErrInvalidCommandOutput], [ErrInvalidRequest], and [CommandError].
func (s Server) NewSession(ctx context.Context, request NewSessionRequest) (Session, error) {
	request = captureNewSessionRequest(request)
	if err := validateServerCommandArguments(
		"new-session",
		serverCommandArgument{field: "Name", value: request.Name},
		serverCommandArgument{field: "StartDirectory", value: request.StartDirectory},
		serverCommandArgument{field: "WindowName", value: request.WindowName},
		serverCommandArgument{field: "Command", value: request.Command},
	); err != nil {
		return Session{}, err
	}
	if request.KillExisting && request.Name == "" {
		return Session{}, invalidLifecycleRequest("KillExisting requires Name")
	}
	if request.Width < 0 || request.Width > 65535 {
		return Session{}, invalidLifecycleRequest("Width must be between 1 and 65535")
	}
	if request.Height < 0 || request.Height > 65535 {
		return Session{}, invalidLifecycleRequest("Height must be between 1 and 65535")
	}
	effective := newSessionCommandServer(s)
	if request.Name != "" {
		if err := validateLifecycleSessionName("name", request.Name); err != nil {
			return Session{}, err
		}
		exists, err := effective.HasSession(ctx, HasSessionRequest{Target: request.Name})
		if err != nil {
			return Session{}, err
		}
		if exists && !request.KillExisting {
			return Session{}, fmt.Errorf("%w: %q", ErrSessionExists, request.Name)
		}
		if exists {
			result, err := effective.literalCmd(ctx, "kill-session", "-t", request.Name)
			if _, err = requireRedactedLifecycleSuccess("kill-session", result, err); err != nil {
				return Session{}, err
			}
		}
	}

	arguments, err := newSessionArguments(request)
	if err != nil {
		return Session{}, err
	}
	result, err := effective.literalCmd(ctx, arguments...)
	if err != nil {
		if identity, identityErr := lifecycleStableIdentity("session", result.Stdout); identityErr == nil {
			return Session{server: effective, sessionID: SessionID(identity)}, err
		}
		return Session{}, err
	}
	result, err = requireRedactedLifecycleSuccess("new-session", result, nil)
	if err != nil {
		return Session{}, err
	}
	identity, err := lifecycleStableIdentity("session", result.Stdout)
	if err != nil {
		return Session{}, err
	}
	session, err := effective.Session(ctx, SessionID(identity))
	if err != nil {
		return Session{server: effective, sessionID: SessionID(identity)}, err
	}
	return session, nil
}

// NewWindow creates a winlink in the receiver session. Attach selects it as
// that session's current window; it is not a guarantee about clients attached
// to other sessions. The returned [Window] is freshly materialized in the
// receiver's exact session context, including when its stable [WindowID] is
// linked elsewhere.
//
// SelectExisting no-output recovery is available only when Index is nil. It
// expands Name with tmux's version-specific rules and requires exactly one
// matching window name in the receiver session. An explicit Index has no such
// recovery. A transport or context error can be delivery-ambiguous and no
// rollback is attempted. If tmux printed a valid WindowID before that error,
// or exact refresh fails after creation, NewWindow returns a partial Window
// containing the receiver SessionID and new WindowID with an Index of -1;
// other failures return a zero Window. See [ErrInvalidCommandOutput] and
// [CommandError].
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
// The returned [Window] is freshly materialized in the receiver SessionID.
// A transport or context error can be delivery-ambiguous and no rollback is
// attempted. If tmux printed a valid identity before that error, or exact
// refresh fails after creation, NewWindow returns the receiver SessionID and
// new WindowID as a partial Window with an Index of -1; other failures return
// a zero Window.
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

// SplitPane creates a tiled pane in the receiver's exact winlink. Attach makes
// the new pane active in that session and winlink; it is not a global
// client-focus guarantee. The returned [Pane] is freshly materialized in the
// receiver SessionID and WindowID rather than by canonical ID-only refresh.
//
// A transport or context error can be delivery-ambiguous and no rollback is
// attempted. If tmux printed a valid [PaneID] before that error, or exact
// refresh fails after creation, SplitPane returns a partial Pane containing
// the receiver SessionID and WindowID and the new PaneID. Other failures return
// a zero Pane. See [SplitPaneRequest], [WarningHandler],
// [ErrInvalidCommandOutput], and [CommandError].
func (w Window) SplitPane(ctx context.Context, request SplitPaneRequest) (Pane, error) {
	request = captureSplitPaneRequest(request)
	target, err := exactWindowTarget(w)
	if err != nil {
		return Pane{}, err
	}
	return splitPane(ctx, w.server, w.sessionID, w.windowID, w.windowIndex, target, request)
}

// Split creates a tiled pane relative to the receiver's exact linked-pane
// view. Attach makes the new pane active in that session and winlink; it is not
// a global client-focus guarantee. The returned [Pane] is freshly materialized
// in the receiver SessionID and WindowID.
//
// A transport or context error can be delivery-ambiguous and no rollback is
// attempted. If tmux printed a valid [PaneID] before that error, or exact
// refresh fails after creation, Split returns a partial Pane containing the
// receiver SessionID and WindowID and the new PaneID. Other failures return a
// zero Pane. See [SplitPaneRequest], [WarningHandler], and
// [ErrInvalidCommandOutput].
func (p Pane) Split(ctx context.Context, request SplitPaneRequest) (Pane, error) {
	request = captureSplitPaneRequest(request)
	target, err := exactPaneTarget(p)
	if err != nil {
		return Pane{}, err
	}
	return splitPane(ctx, p.server, p.sessionID, p.windowID, p.windowIndex, target, request)
}

func splitPane(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	windowID WindowID,
	windowIndex int,
	target string,
	request SplitPaneRequest,
) (Pane, error) {
	var version Version
	if splitPaneRequiresVersion(request) {
		probed, err := server.Version(ctx)
		if err != nil {
			return Pane{}, err
		}
		version = probed
	}
	arguments, warnings, err := splitPaneArguments(target, request, version)
	if err != nil {
		return Pane{}, err
	}
	for _, warning := range warnings {
		server.warn(warning)
	}
	result, err := server.literalCmd(ctx, arguments...)
	return splitPaneResult(ctx, server, sessionID, windowID, windowIndex, result, err)
}

// splitPaneRequiresVersion reports whether a request asks for a flag this
// package gates on the tmux version, and so cannot be rendered without probing
// it. Nearly every split asks for none, which is why the probe is conditional:
// it costs a tmux process of its own the first time.
func splitPaneRequiresVersion(request SplitPaneRequest) bool {
	return request.Empty || splitPaneStylingRequested(request)
}

// splitPaneArguments renders a split's argument vector and the warnings that
// rendering it produced. It performs no I/O, so a [Plan] can render a split it
// has not run and a caller can read what would be sent.
//
// version is consulted only for the flags [splitPaneRequiresVersion] reports,
// so a caller that skipped the probe may pass the zero Version.
func splitPaneArguments(
	target string,
	request SplitPaneRequest,
	version Version,
) ([]string, []Warning, error) {
	if err := validateServerCommandArguments(
		"split-window",
		serverCommandArgument{field: "StartDirectory", value: request.StartDirectory},
		serverCommandArgument{field: "Command", value: request.Command},
		serverCommandArgument{field: "Target", value: target},
	); err != nil {
		return nil, nil, err
	}
	if err := validateSplitPaneRequest(request); err != nil {
		return nil, nil, err
	}
	environment, err := lifecycleEnvironmentArguments(request.Environment)
	if err != nil {
		return nil, nil, err
	}
	startDirectory, err := expandLifecycleDirectory(request.StartDirectory)
	if err != nil {
		return nil, nil, err
	}
	var warnings []Warning
	useEmpty, useStyling := request.Empty, splitPaneStylingRequested(request)
	if useEmpty || useStyling {
		required := Version{raw: "3.7", major: 3, minor: 7}
		if !version.AtLeast(required) {
			if useEmpty {
				warnings = append(warnings, newUnsupportedFeatureWarning(
					"split-window", "empty", version, required,
				))
				useEmpty = false
			}
			if useStyling {
				warnings = append(warnings, newUnsupportedFeatureWarning(
					"split-window", "style/border/message/keep", version, required,
				))
				useStyling = false
			}
		}
	}
	if useEmpty && request.Command != "" {
		return nil, warnings, invalidLifecycleRequest(
			"Empty and Command are mutually exclusive",
		)
	}

	arguments := []string{"split-window", "-t", target}
	switch request.Direction {
	case PaneDirectionBelow:
		arguments = append(arguments, "-v")
	case PaneDirectionAbove:
		arguments = append(arguments, "-v", "-b")
	case PaneDirectionRight:
		arguments = append(arguments, "-h")
	case PaneDirectionLeft:
		arguments = append(arguments, "-h", "-b")
	}
	if request.Size != nil {
		arguments = append(arguments, "-l"+strconv.Itoa(*request.Size))
	}
	if request.Percentage != nil {
		// "-l N%" rather than "-p N". tmux deprecated -p in 3.1 when -l
		// learned percentages, and 3.4 stopped accepting it outright: a
		// percentage split there fails with "size missing". The percentage
		// form of -l works on every version this supports, 3.2a through 3.7b,
		// so there is nothing to gate on.
		arguments = append(arguments, "-l"+strconv.Itoa(*request.Percentage)+"%")
	}
	if request.FullWindow {
		arguments = append(arguments, "-f")
	}
	if request.Zoom {
		arguments = append(arguments, "-Z")
	}
	arguments = append(arguments, "-P", "-F#{pane_id}")
	if startDirectory != "" {
		arguments = append(arguments, "-c"+startDirectory)
	}
	if !request.Attach {
		arguments = append(arguments, "-d")
	}
	arguments = append(arguments, environment...)
	if useEmpty {
		arguments = append(arguments, "-E")
	}
	if useStyling {
		for _, option := range []struct {
			flag  string
			value *string
		}{
			{flag: "-s", value: request.Style},
			{flag: "-S", value: request.ActiveBorderStyle},
			{flag: "-R", value: request.InactiveBorderStyle},
			{flag: "-m", value: request.Message},
		} {
			if option.value != nil {
				arguments = append(arguments, option.flag, *option.value)
			}
		}
		if request.Keep {
			arguments = append(arguments, "-k")
		}
	}
	if request.Command != "" {
		arguments = append(arguments, request.Command)
	}
	return arguments, warnings, nil
}

// splitPaneResult materializes the pane a split produced.
func splitPaneResult(
	ctx context.Context,
	server Server,
	sessionID SessionID,
	windowID WindowID,
	windowIndex int,
	result CommandResult,
	err error,
) (Pane, error) {
	if err != nil {
		if identity, identityErr := lifecycleStableIdentity("pane", result.Stdout); identityErr == nil {
			return Pane{
				server: server, sessionID: sessionID,
				windowID: windowID, windowIndex: windowIndex, paneID: PaneID(identity),
			}, err
		}
		return Pane{}, err
	}
	result, err = requireRedactedLifecycleSuccess("split-window", result, nil)
	if err != nil {
		return Pane{}, err
	}
	identity, err := lifecycleStableIdentity("pane", result.Stdout)
	if err != nil {
		return Pane{}, err
	}
	predicted := Pane{
		server: server, sessionID: sessionID,
		windowID: windowID, windowIndex: windowIndex, paneID: PaneID(identity),
	}
	pane, err := refreshExactPane(ctx, predicted)
	if err != nil {
		return predicted, err
	}
	return pane, nil
}

// Rename changes the receiver session's name and returns a canonical freshly
// materialized [Session]. If the command succeeds but refresh fails, it
// returns the receiver with that error. A transport or context error can be
// delivery-ambiguous; no rollback is attempted. Invalid names are rejected
// before execution and match [ErrInvalidRequest].
func (s Session) Rename(ctx context.Context, name string) (Session, error) {
	if err := validateServerCommandArguments(
		"rename-session",
		serverCommandArgument{field: "Target", value: s.sessionID.String()},
		serverCommandArgument{field: "Name", value: name},
	); err != nil {
		return Session{}, err
	}
	if err := validateLifecycleSessionName("name", name); err != nil {
		return Session{}, err
	}
	result, err := s.literalCmd(ctx, "rename-session", name)
	if _, err = requireLifecycleSuccess("rename-session", result, err); err != nil {
		return Session{}, err
	}
	refreshed, err := s.Refresh(ctx)
	if err != nil {
		return s, err
	}
	return refreshed, nil
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

// Kill terminates the configured tmux server and all of its sessions, windows,
// panes, and clients. After a completed failed kill, a liveness probe makes
// repeated calls harmless when no daemon answers. Transport or context errors
// can be delivery-ambiguous; no rollback is attempted.
//
// tmux leaves the socket file in place, so cleanup that waits for the path to
// disappear does not finish. Use [Server.IsAlive] to observe the daemon, and
// remove a socket path this process chose itself.
func (s Server) Kill(ctx context.Context) error {
	result, err := s.literalCmd(ctx, "kill-server")
	if err != nil {
		return err
	}
	if result.ExitCode == 0 && len(result.Stderr) == 0 {
		return nil
	}
	commandErr := newCommandError("kill-server", result)
	alive, probeErr := s.IsAlive(ctx)
	if probeErr != nil {
		return errors.Join(
			commandErr,
			fmt.Errorf("verify server stopped: %w", probeErr),
		)
	}
	if !alive {
		return nil
	}
	return commandErr
}

// KillSession terminates the session selected by tmux target syntax. Pattern
// and prefix matching are deliberately left to tmux. Completed nonzero exits
// without stderr are ignored by this operation's source contract. A transport
// or context error can be delivery-ambiguous; the void result cannot carry
// partial identity and no rollback is attempted.
func (s Server) KillSession(ctx context.Context, target string) error {
	if err := validateServerCommandArgument(
		"kill-session", "Target", target, true,
	); err != nil {
		return err
	}
	result, err := s.literalCmd(ctx, "kill-session", "-t", target)
	return requireServerCommandNoStderr("kill-session", result, err)
}

// Kill destroys the receiver session, removes its winlinks, and detaches every
// client attached to it. A window with no remaining links and all of that
// window's panes are also destroyed. The materialized receiver is not
// refreshed and no longer represents a live session. A completed command is
// treated as an error only when tmux writes stderr, which returns a
// [CommandError]; a nonzero exit without stderr is ignored. A transport or
// context error can be delivery-ambiguous; the void result cannot carry
// partial identity and no rollback is attempted.
// Kill is equivalent to [Session.KillWith] with a zero [SessionKillRequest].
func (s Session) Kill(ctx context.Context) error {
	return s.KillWith(ctx, SessionKillRequest{})
}

// KillWith applies one kill-session mode without refreshing the materialized
// receiver. The zero request destroys the receiver; AllExcept destroys every
// other session while preserving the receiver and its current window;
// ClearAlerts only clears alerts in windows linked to the receiver and leaves
// sessions, winlinks, panes, client attachments, and current-window selections
// unchanged. Destroying a session detaches its clients and removes its
// winlinks; windows left without links and their panes are also destroyed.
// Group destroys the receiver's group on tmux 3.7 or later. On older versions,
// Group synchronously reaches [WarningHandler], is omitted, and the receiver
// alone is destroyed.
//
// AllExcept, ClearAlerts, and Group are rejected as mutually exclusive before
// execution. A completed command is treated as an error only when tmux writes
// stderr, which returns a [CommandError]; a nonzero exit without stderr is
// ignored. A transport or context error can be delivery-ambiguous; the void
// result cannot carry partial identity and no rollback is attempted.
func (s Session) KillWith(ctx context.Context, request SessionKillRequest) error {
	modes := 0
	if request.AllExcept {
		modes++
	}
	if request.ClearAlerts {
		modes++
	}
	if request.Group {
		modes++
	}
	if modes > 1 {
		return invalidLifecycleRequest(
			"AllExcept, ClearAlerts, and Group are mutually exclusive",
		)
	}
	if err := validateTypedTarget(
		"kill-session", "Target", "session", s.sessionID.String(),
	); err != nil {
		return err
	}
	arguments := []string{"kill-session"}
	if request.AllExcept {
		arguments = append(arguments, "-a")
	}
	if request.ClearAlerts {
		arguments = append(arguments, "-C")
	}
	if request.Group {
		required := Version{raw: "3.7", major: 3, minor: 7}
		current, err := s.server.Version(ctx)
		if err != nil {
			return err
		}
		if current.AtLeast(required) {
			arguments = append(arguments, "-g")
		} else {
			s.server.warn(newUnsupportedFeatureWarning(
				"kill-session", "group", current, required,
			))
		}
	}
	result, err := s.literalCmd(ctx, arguments...)
	return requireServerCommandNoStderr("kill-session", result, err)
}

// KillWindow destroys one stable window. When Target and Index are nil it
// selects the receiver session's current window, and Index selects a winlink
// in that session. A nonnil Target is forwarded as unrestricted tmux target
// syntax and may select a window in another session. The selected stable
// window is removed from every session and its panes are destroyed. Affected
// sessions preserve their current selection unless this window was current,
// in which case they select another. A session left without windows is
// destroyed and its clients are detached.
//
// Target and Index are rejected together before execution. The materialized
// receiver is not refreshed. A completed command is treated as an error only
// when tmux writes stderr, which returns a [CommandError]; a nonzero exit
// without stderr is ignored. A transport or context error can be
// delivery-ambiguous; the void result cannot carry partial identity and no
// rollback is attempted.
func (s Session) KillWindow(ctx context.Context, request KillWindowRequest) error {
	if request.Target != nil && request.Index != nil {
		return invalidLifecycleRequest("Target and Index are mutually exclusive")
	}

	var target string
	if request.Target != nil {
		target = *request.Target
		if err := validateServerCommandArgument(
			"kill-window", "Target", target, true,
		); err != nil {
			return err
		}
	} else {
		if err := validateTypedTarget(
			"kill-window", "Target", "session", s.sessionID.String(),
		); err != nil {
			return err
		}
		target = s.sessionID.String()
		if request.Index != nil {
			target += ":" + strconv.Itoa(*request.Index)
		}
	}

	result, err := s.server.literalCmd(ctx, "kill-window", "-t", target)
	return requireServerCommandNoStderr("kill-window", result, err)
}

// Kill destroys the stable window selected by the receiver's WindowID,
// removes all of its winlinks, and destroys its panes. Affected sessions
// preserve their current selection unless this window was current, in which
// case they select another. A session left without windows is destroyed and
// its clients are detached. The materialized receiver is not refreshed and no
// longer represents a live window. A completed command is treated as an error
// only when tmux writes stderr, which returns a [CommandError]; a nonzero exit
// without stderr is ignored. A transport or context error can be
// delivery-ambiguous; the void result cannot carry partial identity and no
// rollback is attempted.
func (w Window) Kill(ctx context.Context) error {
	result, err := w.literalCmd(ctx, "kill-window")
	return requireServerCommandNoStderr("kill-window", result, err)
}

// KillOthers destroys every other stable window selected through the
// receiver's exact session and leaves the receiver as that session's current
// and only window. Links in other sessions are not selected merely because
// they share the receiver's [WindowID], but every selected stable window is
// removed from all sessions. Other affected sessions preserve their current
// selection unless a destroyed window was current, in which case they select
// another. A session left without windows is destroyed and detaches its
// clients. The receiver is not refreshed. A completed command is treated as
// an error only when tmux writes stderr, which returns a [CommandError]; a
// nonzero exit without stderr is ignored. A transport or context error can be
// delivery-ambiguous; the void result cannot carry partial identity and no
// rollback is attempted.
func (w Window) KillOthers(ctx context.Context) error {
	target, err := exactWindowTarget(w)
	if err != nil {
		return err
	}
	result, err := w.server.literalCmd(ctx, "kill-window", "-t", target, "-a")
	return requireServerCommandNoStderr("kill-window", result, err)
}

// Kill destroys the receiver pane through its exact linked-pane target. If
// panes remain and the receiver was active, tmux selects another active pane.
// If this was the last pane, tmux also destroys the window and removes all of
// its winlinks. Affected sessions preserve their current selection unless the
// destroyed window was current, in which case they select another. A session
// left without windows is destroyed and detaches its clients. The receiver is
// not refreshed. A completed command is treated as an error only when tmux
// writes stderr, which returns a [CommandError]; a nonzero exit without stderr
// is ignored. A transport or context error can be delivery-ambiguous; the void
// result cannot carry partial identity and no rollback is attempted.
func (p Pane) Kill(ctx context.Context) error {
	result, err := p.literalCmd(ctx, "kill-pane")
	return requireServerCommandNoStderr("kill-pane", result, err)
}

// KillOthers destroys every other pane in the receiver's stable window and
// leaves the receiver as its sole active pane. It does not select the exact
// winlink as its session's current window or promise client focus, and it does
// not destroy a window or session. The receiver is not refreshed. A completed
// command is treated as an error only when tmux writes stderr, which returns a
// [CommandError]; a nonzero exit without stderr is ignored. A transport or
// context error can be delivery-ambiguous; the void result cannot carry
// partial identity and no rollback is attempted.
func (p Pane) KillOthers(ctx context.Context) error {
	result, err := p.literalCmd(ctx, "kill-pane", "-a")
	return requireServerCommandNoStderr("kill-pane", result, err)
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

func validateLifecycleSessionName(field, name string) error {
	if name == "" {
		return invalidLifecycleRequest(field + " must not be empty")
	}
	if strings.ContainsAny(name, ".:") {
		return invalidLifecycleRequest(field + " must not contain periods or colons")
	}
	return nil
}

// ValidateSessionName reports whether name can be used as a tmux session
// name. Empty names and the target delimiters '.' and ':' are rejected.
func ValidateSessionName(name string) error {
	return validateLifecycleSessionName("name", name)
}

func validateSplitPaneRequest(request SplitPaneRequest) error {
	if request.Direction > PaneDirectionLeft {
		return invalidLifecycleRequest("unsupported pane direction")
	}
	if request.Size != nil && request.Percentage != nil {
		return invalidLifecycleRequest("Size and Percentage are mutually exclusive")
	}
	if request.Size != nil && *request.Size < 0 {
		return invalidLifecycleRequest("Size must be nonnegative")
	}
	if request.Percentage != nil && (*request.Percentage < 0 || *request.Percentage > 100) {
		return invalidLifecycleRequest("Percentage must be between 0 and 100")
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "Style", value: request.Style},
		{name: "ActiveBorderStyle", value: request.ActiveBorderStyle},
		{name: "InactiveBorderStyle", value: request.InactiveBorderStyle},
		{name: "Message", value: request.Message},
	} {
		if field.value != nil {
			if err := validateServerCommandArgument(
				"split-window", field.name, *field.value, true,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func captureNewSessionRequest(request NewSessionRequest) NewSessionRequest {
	request.Environment = maps.Clone(request.Environment)
	return request
}

func captureNewWindowRequest(request NewWindowRequest) NewWindowRequest {
	request.Name = copyOptionalString(request.Name)
	request.Index = copyOptionalInt(request.Index)
	request.Environment = maps.Clone(request.Environment)
	return request
}

func captureSplitPaneRequest(request SplitPaneRequest) SplitPaneRequest {
	request.Size = copyOptionalInt(request.Size)
	request.Percentage = copyOptionalInt(request.Percentage)
	request.Style = copyOptionalString(request.Style)
	request.ActiveBorderStyle = copyOptionalString(request.ActiveBorderStyle)
	request.InactiveBorderStyle = copyOptionalString(request.InactiveBorderStyle)
	request.Message = copyOptionalString(request.Message)
	request.Environment = maps.Clone(request.Environment)
	return request
}

func copyOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func splitPaneStylingRequested(request SplitPaneRequest) bool {
	return request.Style != nil ||
		request.ActiveBorderStyle != nil ||
		request.InactiveBorderStyle != nil ||
		request.Message != nil ||
		request.Keep
}

func lifecycleEnvironmentArguments(environment map[string]string) ([]string, error) {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	arguments := make([]string, 0, len(keys))
	for _, key := range keys {
		if err := validateEnvironmentName(key); err != nil {
			return nil, err
		}
		if err := validateEnvironmentValue(environment[key]); err != nil {
			return nil, err
		}
		arguments = append(arguments, "-e"+key+"="+environment[key])
	}
	return arguments, nil
}

func invalidLifecycleRequest(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, detail)
}

func expandLifecycleDirectory(path string) (string, error) {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path, nil
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return "", invalidLifecycleRequest("StartDirectory does not support named-user expansion")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: expand StartDirectory: %w", ErrInvalidRequest, err)
	}
	if home == "" {
		return "", invalidLifecycleRequest("current user home directory is empty")
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func newSessionCommandServer(server Server) Server {
	state := server.connectionState()
	options := state.options
	environment := options.ProcessEnvironment
	if environment == nil {
		environment = os.Environ()
	} else {
		environment = slices.Clone(environment)
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name != "TMUX" {
			filtered = append(filtered, entry)
		}
	}
	options.ProcessEnvironment = filtered
	return Server{
		state: &serverState{
			options: options,
			runner:  state.runner,
		},
		engine:       server.engine,
		strictErrors: server.strictErrors,
	}
}

func requireLifecycleSuccess(
	subcommand string,
	result CommandResult,
	err error,
) (CommandResult, error) {
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return result, newCommandError(subcommand, result)
	}
	return result, nil
}

func requireRedactedLifecycleSuccess(
	subcommand string,
	result CommandResult,
	err error,
) (CommandResult, error) {
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return result, newRedactedCommandError(subcommand, result)
	}
	return result, nil
}

func lifecycleStableIdentity(object string, output []string) (string, error) {
	if len(output) != 1 {
		return "", fmt.Errorf(
			"%w: %s command printed %d identity lines",
			ErrInvalidCommandOutput,
			object,
			len(output),
		)
	}
	if err := validateStableTarget(object, output[0]); err != nil {
		return "", fmt.Errorf(
			"%w: %s command printed %q",
			ErrInvalidCommandOutput,
			object,
			output[0],
		)
	}
	return output[0], nil
}
