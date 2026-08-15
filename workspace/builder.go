package workspace

import (
	"context"
	"fmt"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// Build creates the workspace on server and returns the created session.
//
// Build is not atomic. tmux has no transaction, so a failure partway through
// leaves the windows and panes created so far in place; the returned session
// identifies them so a caller can inspect or kill what exists. Build uses
// strict errors regardless of server's setting, because a workspace that half
// exists is never the caller's intent.
//
// Build runs over a control connection, which carries a tmux command without
// starting a process for it and takes most of the cost out of a workspace. The
// connection is a tmux client for the length of the call: it appears in
// list-clients, counts toward session_attached, and fires a client-attached
// hook. Hand Build a server carrying [tmux.Server.SubprocessEngine] to decline
// it, which is what a tmux configuration that reacts to attachment wants.
//
// A pane's commands are its workspace, window, and pane shell_command_before
// entries in that order, then its own shell_command entries. sleep_before and
// sleep_after pause Build rather than the pane, matching tmuxp: the delay
// exists to let a previous command settle before the next is typed.
func Build(ctx context.Context, server tmux.Server, workspace Workspace) (tmux.Session, error) {
	if err := workspace.Validate(); err != nil {
		return tmux.Session{}, err
	}
	server = server.WithStrictErrors()

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

	// The rest of the build runs over a control connection, which carries a
	// tmux command without starting a process for it. A workspace is dozens of
	// commands, so this is most of the build's cost.
	//
	// A handle that already carries an engine is left alone, because its owner
	// has chosen a transport and the choice includes declining this one.
	//
	// The pool closes with this call while the session returned from it does
	// not. That is safe because a closed pool stops carrying commands rather
	// than failing them: the returned session goes back to starting a process
	// per command, exactly as it would have without any of this.
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

	// global_options are applied after the session exists because tmux has no
	// global scope until a server runs, so the first window cannot inherit
	// them; every later window can.
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

// buildWindow creates one window and its panes. The first window of a
// workspace already exists, created with the session, so it is resolved rather
// than created.
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
		// tmux creates the first window with the session and new-session takes
		// no index, so honouring one means moving the window afterwards. Every
		// later window carries its index into the call that creates it.
		// tmux refuses to move a window to the index it already occupies, and a
		// file naming the server's base-index for its first window asks for
		// exactly that. Asking is not a mistake, so the move is skipped rather
		// than the build failed.
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

// buildPanes splits the window until it holds one pane per description, then
// runs each pane's commands. Splitting completes before any command runs so a
// layout change cannot interleave output between panes.
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
		// Each pane splits the one before it rather than the window, so the
		// panes end up in the order the file lists them. Splitting the window
		// targets whichever pane tmux considers current, which puts every new
		// pane next to the first and reverses everything after it.
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

// applyPaneDefaults fills each command's unset enter and sleep settings from
// the pane's, so a pane can state once what every one of its commands does.
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

// paneSuppressHistory resolves the nearest suppress_history setting, pane over
// window over workspace.
func paneSuppressHistory(workspace Workspace, window Window, pane Pane) bool {
	if pane.SuppressHistory != nil {
		return bool(*pane.SuppressHistory)
	}
	if window.SuppressHistory != nil {
		return bool(*window.SuppressHistory)
	}
	return bool(workspace.SuppressHistory)
}

// sleep waits for the requested delay, or returns early when ctx ends.
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

// firstWindowDirectory is what the session's own window starts in when neither
// it nor its first pane names a directory.
func firstWindowDirectory(workspace Workspace, first Window) string {
	if first.StartDirectory != "" {
		return first.StartDirectory
	}
	return workspace.StartDirectory
}

// windowAlreadyAt reports whether the window occupies index. A window whose
// index cannot be read is treated as elsewhere, so the move is attempted and
// tmux decides.
func windowAlreadyAt(window tmux.Window, index int) bool {
	current, ok := window.Formats().WindowIndex()
	return ok && current == index
}

// windowDirectory is the directory a window's first pane starts in.
//
// tmux creates that pane with the window, so it has no creation call of its
// own to carry a directory and its start_directory would otherwise be read and
// dropped. A pane's own setting is the more specific one, so it wins over the
// window's, which is how it works for every later pane.
func windowDirectory(described Window, fallback string) string {
	if len(described.Panes) > 0 && described.Panes[0].StartDirectory != "" {
		return described.Panes[0].StartDirectory
	}
	return fallback
}

// windowCommand is the process a window's first pane runs.
//
// tmux creates that pane with the window, so unlike every later pane it is
// never split into existence and has no creation call of its own to carry a
// command. A window_shell is therefore the only way to give it one, and a
// first pane's own shell would otherwise be read and dropped. Validate rejects
// a workspace that sets both, so the fallback here can never pick between two
// commands a file actually asked for.
func windowCommand(described Window) string {
	if described.Shell != "" {
		return described.Shell
	}
	if len(described.Panes) > 0 {
		return described.Panes[0].Shell
	}
	return ""
}

// setSessionOption applies one of tmuxp's options to the session.
//
// tmux resolves a bare "set-option -t" against whichever of its option tables
// declares the name, so a tmuxp file puts a window option such as
// main-pane-height beside session options and tmux sorts them out, targeting
// the session's current window for the window-table ones. The tmux module's
// scopes are typed instead and reject a name their own table does not declare,
// so reproducing tmux's dispatch means asking the window as well.
//
// The session-scope error is the one returned when neither table declares the
// name, because it names the option the file actually got wrong.
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

// setGlobalOption applies one of tmuxp's global_options at tmux's global scope.
//
// tmux resolves a bare "set-option -g" against whichever of its three option
// tables declares the name, so a tmuxp file puts session, window, and server
// options in one global_options mapping and tmux sorts them out. The tmux
// module's scopes are typed instead: each one accepts only the names its own
// table declares, and rejects the rest before a command is built. Reproducing
// tmux's dispatch therefore means asking the scopes in turn.
//
// Only a name no table declares reaches the end, so its session-scope error is
// the one returned: it names the option the file actually got wrong, rather
// than reporting it against whichever table happened to be tried last.
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
