package workspace

import (
	"context"
	"fmt"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// Build creates the workspace on server and returns the created session.
//
// Build is not atomic. On failure, it returns the session containing resources
// already created. Command failures are returned rather than normalized.
//
// When server has no engine, Build uses a temporary control connection. It
// appears as a tmux client and may fire attachment hooks; pass a server carrying
// [tmux.Server.SubprocessEngine] to avoid it.
//
// Each pane receives workspace, window, and pane CommandsBefore, then its own
// Commands. Sleep values pause Build rather than the pane.
func Build(ctx context.Context, server tmux.Server, workspace Workspace) (tmux.Session, error) {
	if err := workspace.Validate(); err != nil {
		return tmux.Session{}, err
	}

	first := workspace.Windows[0]
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name:           workspace.SessionName,
		StartDirectory: windowDirectory(first, firstWindowDirectory(workspace, first)),
		WindowName:     first.Name,
		Command:        windowCommand(first),
	})
	if err != nil {
		return session, fmt.Errorf("create session %q: %w", workspace.SessionName, err)
	}

	// Preserve a caller-selected engine. When Build opens the pool, it closes
	// with this call while the returned session keeps its pooled engine. After
	// close, EngineFallbackAllow permits subprocess fallback;
	// EngineFallbackReject returns a tmux.EngineFallbackError instead.
	if server.Engine() == nil {
		connected, live, pool, poolErr := server.OpenControlPool(
			ctx, session, tmux.ControlPoolRequest{},
		)
		if poolErr == nil {
			defer func() { _ = pool.Close() }()
			server = connected
			session = live
		}
	}

	// The initial window is created with the session before these values are set;
	// later windows can inherit them at creation.
	for name, value := range workspace.GlobalOptions {
		if err := setGlobalOption(ctx, server, name, value); err != nil {
			return session, fmt.Errorf("set global option %q: %w", name, err)
		}
	}

	for name, value := range workspace.Environment {
		if err := session.SetEnvironment(
			ctx, name, value, tmux.SetEnvironmentOptions{},
		); err != nil {
			return session, fmt.Errorf("set environment %q: %w", name, err)
		}
	}
	for name, value := range workspace.Options {
		if err := setSessionOption(ctx, session, name, value); err != nil {
			return session, fmt.Errorf("set session option %q: %w", name, err)
		}
	}

	var focusWindow *tmux.Window
	for index, described := range workspace.Windows {
		window, err := buildWindow(ctx, session, workspace, described, index)
		if err != nil {
			return session, err
		}
		if bool(described.Focus) {
			focused := window
			focusWindow = &focused
		}
	}

	if focusWindow != nil {
		if _, err := focusWindow.Select(ctx); err != nil {
			return session, fmt.Errorf("focus window: %w", err)
		}
	}
	return session, nil
}

// buildWindow resolves the session's initial window or creates a later one,
// then builds its panes.
func buildWindow(
	ctx context.Context,
	session tmux.Session,
	workspace Workspace,
	described Window,
	position int,
) (tmux.Window, error) {
	directory := described.StartDirectory
	if directory == "" {
		directory = workspace.StartDirectory
	}

	var window tmux.Window
	var err error
	if position == 0 {
		window, err = session.ResolveActiveWindow(ctx)
		if err != nil {
			return window, fmt.Errorf("resolve initial window: %w", err)
		}
		// new-session cannot choose the initial winlink index, so move it
		// afterward unless it already occupies the requested index.
		if described.Index != nil && !windowAlreadyAt(window, *described.Index) {
			window, err = window.Move(ctx, tmux.MoveWindowRequest{
				TargetIndex: described.Index,
				NoSelect:    true,
			})
			if err != nil {
				return window, fmt.Errorf(
					"move initial window to index %d: %w", *described.Index, err)
			}
		}
	} else {
		request := tmux.NewWindowRequest{
			StartDirectory: windowDirectory(described, directory),
			Command:        windowCommand(described),
			Index:          described.Index,
		}
		if described.Name != "" {
			name := described.Name
			request.Name = &name
		}
		window, err = session.NewWindow(ctx, request)
		if err != nil {
			return window, fmt.Errorf("create window %q: %w", described.Name, err)
		}
	}

	for name, value := range described.Environment {
		if err := session.SetEnvironment(
			ctx, name, value, tmux.SetEnvironmentOptions{},
		); err != nil {
			return window, fmt.Errorf("set window environment %q: %w", name, err)
		}
	}
	for name, value := range described.Options {
		if err := window.SetOption(ctx, name, value, tmux.SetOptionOptions{}); err != nil {
			return window, fmt.Errorf("set window option %q: %w", name, err)
		}
	}

	panes, err := buildPanes(ctx, session, window, workspace, described, directory)
	if err != nil {
		return window, err
	}

	for name, value := range described.OptionsAfter {
		if err := window.SetOption(ctx, name, value, tmux.SetOptionOptions{}); err != nil {
			return window, fmt.Errorf("set window option %q after panes: %w", name, err)
		}
	}

	if described.Layout != "" {
		if err := window.SelectLayout(ctx, tmux.SelectLayoutRequest{
			Layout: described.Layout,
		}); err != nil {
			return window, fmt.Errorf("apply layout %q: %w", described.Layout, err)
		}
	}
	for index, pane := range panes {
		if index < len(described.Panes) && bool(described.Panes[index].Focus) {
			if _, err := pane.Select(ctx, tmux.PaneSelectRequest{}); err != nil {
				return window, fmt.Errorf("focus pane %d: %w", index, err)
			}
		}
	}
	return window, nil
}

// buildPanes creates all panes before running commands so layout changes cannot
// interleave their output.
func buildPanes(
	ctx context.Context,
	session tmux.Session,
	window tmux.Window,
	workspace Workspace,
	described Window,
	directory string,
) ([]tmux.Pane, error) {
	first, ok, err := window.ResolveActivePane(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve initial pane: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: window %q has no initial pane",
			ErrInvalidWorkspace, described.Name)
	}
	panes := []tmux.Pane{first}

	// tmuxp allows a window with no panes; it still has the one tmux created.
	for index := 1; index < len(described.Panes); index++ {
		paneDirectory := described.Panes[index].StartDirectory
		if paneDirectory == "" {
			paneDirectory = directory
		}
		// Split the preceding pane so tmux preserves document order.
		pane, err := panes[index-1].Split(ctx, tmux.SplitPaneRequest{
			Direction:      tmux.PaneDirectionBelow,
			StartDirectory: paneDirectory,
			Command:        described.Panes[index].Shell,
		})
		if err != nil {
			return nil, fmt.Errorf("split pane %d: %w", index, err)
		}
		panes = append(panes, pane)
	}

	for index, pane := range panes {
		if index >= len(described.Panes) {
			continue
		}
		describedPane := described.Panes[index]
		for name, value := range describedPane.Environment {
			if err := session.SetEnvironment(
				ctx, name, value, tmux.SetEnvironmentOptions{},
			); err != nil {
				return nil, fmt.Errorf("set pane environment %q: %w", name, err)
			}
		}
		suppress := paneSuppressHistory(workspace, described, describedPane)
		commands := make([]Command, 0, len(describedPane.Commands)+4)
		commands = append(commands, workspace.CommandsBefore...)
		commands = append(commands, described.CommandsBefore...)
		commands = append(commands, describedPane.CommandsBefore...)
		commands = append(commands, describedPane.Commands...)

		for _, command := range applyPaneDefaults(describedPane, commands) {
			if err := sleep(ctx, command.SleepBefore); err != nil {
				return nil, err
			}
			if command.Command != "" {
				text := command.Command
				if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
					Command:         &text,
					SuppressHistory: suppress,
					SkipEnter:       !command.sends(),
				}); err != nil {
					return nil, fmt.Errorf("run %q in pane %d: %w", command.Command, index, err)
				}
			}
			if err := sleep(ctx, command.SleepAfter); err != nil {
				return nil, err
			}
		}
	}
	return panes, nil
}

func applyPaneDefaults(pane Pane, commands []Command) []Command {
	resolved := make([]Command, 0, len(commands))
	for _, command := range commands {
		if command.Enter == nil && pane.Enter != nil {
			enter := *pane.Enter
			command.Enter = &enter
		}
		if command.SleepBefore == 0 && pane.SleepBefore > 0 {
			command.SleepBefore = pane.SleepBefore
		}
		if command.SleepAfter == 0 && pane.SleepAfter > 0 {
			command.SleepAfter = pane.SleepAfter
		}
		resolved = append(resolved, command)
	}
	return resolved
}

func paneSuppressHistory(workspace Workspace, window Window, pane Pane) bool {
	if pane.SuppressHistory != nil {
		return bool(*pane.SuppressHistory)
	}
	if window.SuppressHistory != nil {
		return bool(*window.SuppressHistory)
	}
	return bool(workspace.SuppressHistory)
}

func sleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func firstWindowDirectory(workspace Workspace, first Window) string {
	if first.StartDirectory != "" {
		return first.StartDirectory
	}
	return workspace.StartDirectory
}

// windowAlreadyAt lets tmux decide when the materialized index is unavailable.
func windowAlreadyAt(window tmux.Window, index int) bool {
	current, ok := window.Formats().WindowIndex()
	return ok && current == index
}

// The first pane is created with its window, so its directory must be passed
// to NewWindow. A pane-specific value wins.
func windowDirectory(described Window, fallback string) string {
	if len(described.Panes) > 0 && described.Panes[0].StartDirectory != "" {
		return described.Panes[0].StartDirectory
	}
	return fallback
}

// The first pane is created with its window, so its command must be passed to
// NewWindow. Validate rejects competing window and pane commands.
func windowCommand(described Window) string {
	if described.Shell != "" {
		return described.Shell
	}
	if len(described.Panes) > 0 {
		return described.Panes[0].Shell
	}
	return ""
}

// setSessionOption reproduces tmuxp's untyped dispatch across the session and
// window tables. It returns the session-scope error when neither accepts name.
func setSessionOption(ctx context.Context, session tmux.Session, name, value string) error {
	first := session.SetOption(ctx, name, value, tmux.SetOptionOptions{})
	if first == nil {
		return nil
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		return first
	}
	if err := window.SetOption(ctx, name, value, tmux.SetOptionOptions{}); err == nil {
		return nil
	}
	return first
}

// setGlobalOption reproduces tmuxp's untyped dispatch across the global
// session, window, and server tables. It returns the session-scope error when
// none accepts name.
func setGlobalOption(ctx context.Context, server tmux.Server, name, value string) error {
	first := server.GlobalSessionScope().SetOption(ctx, name, value, tmux.SetOptionOptions{})
	if first == nil {
		return nil
	}
	if err := server.GlobalWindowScope().SetOption(
		ctx, name, value, tmux.SetOptionOptions{},
	); err == nil {
		return nil
	}
	if err := server.SetOption(ctx, name, value, tmux.SetOptionOptions{}); err == nil {
		return nil
	}
	return first
}
