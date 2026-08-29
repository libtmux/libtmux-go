package tmux

// Window operations a [Plan] can record. Each mirrors the method that runs the
// same tmux command immediately and takes the same request type; see
// [Plan.SplitPane] for how a recorded operation is written.

// NewWindow records a window created in the session or beside the window that
// target names, and returns a [Ref] to it.
//
// SelectExisting is rejected: it asks tmux for an existing window's
// expanded name before creating anything, and a plan records without reading.
func (p *Plan) NewWindow(target Ref, request NewWindowRequest) Ref {
	request = captureNewWindowRequest(request)
	return p.add(Op{
		name:    "new-window",
		target:  target,
		creates: true,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if request.SelectExisting {
				return nil, invalidLifecycleRequest(
					"SelectExisting reads an existing window's name before creating, " +
						"which a plan cannot do; run Session.NewWindow instead",
				)
			}
			return newWindowArguments(resolved, request)
		},
	})
}

// KillWindow records the destruction of the window target names.
func (p *Plan) KillWindow(target Ref) {
	p.add(Op{
		name:   "kill-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("kill-window", resolved)
		},
	})
}

// KillOtherWindows records the destruction of every window in the session
// holding the window target names, except that window.
func (p *Plan) KillOtherWindows(target Ref) {
	p.add(Op{
		name:   "kill-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("kill-window", resolved, "-a")
		},
	})
}

// RenameWindow records a new name for the window target names.
func (p *Plan) RenameWindow(target Ref, name string) {
	p.add(Op{
		name:   "rename-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if err := validateServerCommandArgument(
				"rename-window", "Name", name, true,
			); err != nil {
				return nil, err
			}
			return targetedArguments("rename-window", resolved, name)
		},
	})
}

// SelectWindow records the window target names becoming its session's current
// window.
func (p *Plan) SelectWindow(target Ref) {
	p.add(Op{
		name:   "select-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("select-window", resolved)
		},
	})
}

// SelectLayout records a layout applied to the window target names.
func (p *Plan) SelectLayout(target Ref, request SelectLayoutRequest) {
	p.add(Op{
		name:   "select-layout",
		target: target,
		build: func(resolved, _ string, render planRenderContext) ([]string, error) {
			return selectLayoutArguments(resolved, request, render.version)
		},
	})
}

// NextLayout records the next preset layout applied to the window target names.
func (p *Plan) NextLayout(target Ref) {
	p.add(Op{
		name:   "next-layout",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("next-layout", resolved)
		},
	})
}

// PreviousLayout records the previous preset layout applied to the window
// target names.
func (p *Plan) PreviousLayout(target Ref) {
	p.add(Op{
		name:   "previous-layout",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("previous-layout", resolved)
		},
	})
}

// ResizeWindow records a resize of the window target names.
func (p *Plan) ResizeWindow(target Ref, request ResizeWindowRequest) {
	p.add(Op{
		name:   "resize-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return resizeWindowArguments(resolved, request)
		},
	})
}

// RotateWindow records the panes of the window target names being rotated
// through their positions.
func (p *Plan) RotateWindow(target Ref, request RotateWindowRequest) {
	p.add(Op{
		name:   "rotate-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return rotateWindowArguments(resolved, request)
		},
	})
}

// RespawnWindow records the window target names being restarted with a new
// command.
func (p *Plan) RespawnWindow(target Ref, request RespawnRequest) {
	p.add(Op{
		name:   "respawn-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return respawnArguments("respawn-window", resolved, request)
		},
	})
}

// LastPane records the window target names returning to its previously active
// pane.
func (p *Plan) LastPane(target Ref) {
	p.add(Op{
		name:   "last-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("last-pane", resolved)
		},
	})
}

// LinkWindow records the window source names being linked into the session
// target names.
//
// target supplies the destination session so it may refer to a session an
// earlier step creates. TargetIndex still selects the position within it.
func (p *Plan) LinkWindow(target, source Ref, request LinkWindowRequest) {
	p.add(Op{
		name:   "link-window",
		target: target,
		source: source,
		build: func(resolved, from string, _ planRenderContext) ([]string, error) {
			return linkWindowArguments(resolved, from, request)
		},
	})
}

// UnlinkWindow records the window target names being removed from the session
// it is linked into.
func (p *Plan) UnlinkWindow(target Ref, request UnlinkWindowRequest) {
	p.add(Op{
		name:   "unlink-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			arguments := []string{"unlink-window"}
			if request.KillIfLast {
				arguments = append(arguments, "-k")
			}
			return append(arguments, "-t", resolved), nil
		},
	})
}

// MoveWindow records the window source names being moved into the session
// target names.
//
// target supplies the destination session. TargetIndex selects its position.
func (p *Plan) MoveWindow(target, source Ref, request MoveWindowRequest) {
	p.add(Op{
		name:   "move-window",
		target: target,
		source: source,
		build: func(resolved, from string, _ planRenderContext) ([]string, error) {
			return moveWindowArguments(resolved, from, request)
		},
	})
}

// SwapWindow records the windows target and source name exchanging places.
// Detach leaves each session's current window unchanged.
func (p *Plan) SwapWindow(target, source Ref, detach bool) {
	p.add(Op{
		name:   "swap-window",
		target: target,
		source: source,
		build: func(resolved, from string, _ planRenderContext) ([]string, error) {
			arguments, err := targetedArguments("swap-window", resolved, "-s", from)
			if err != nil {
				return nil, err
			}
			if detach {
				arguments = append(arguments, "-d")
			}
			return arguments, nil
		},
	})
}
