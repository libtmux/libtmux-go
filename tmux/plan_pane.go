package tmux

// Pane plan operations share request types and argument rendering with their
// immediate counterparts.

// SplitPane records a split of the window or pane target names, and returns a
// [Ref] to the pane it will create.
//
// The returned ref can target later steps before the pane exists:
//
//	plan := tmux.NewPlan()
//	pane := plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{})
//	plan.SendKeys(pane, tmux.SendKeysRequest{Command: tmux.Ptr("top")})
//	result, err := plan.Run(ctx, server)
func (p *Plan) SplitPane(target Ref, request SplitPaneRequest) Ref {
	request = captureSplitPaneRequest(request)
	return p.add(Op{
		name:         "split-window",
		target:       target,
		creates:      true,
		needsVersion: splitPaneRequiresVersion(request),
		build: func(resolved, _ string, render planRenderContext) ([]string, error) {
			arguments, warnings, err := splitPaneArguments(resolved, request, render.version)
			if err != nil {
				return nil, err
			}
			if err := render.reportUnsupported(warnings); err != nil {
				return nil, err
			}
			return arguments, nil
		},
	})
}

// SendKeys records keys sent to the pane target names.
//
// Sending keys produces no output and can share a dispatch; see [Plan.Explain].
//
// A request carrying a command records two steps, as [Pane.SendKeys] issues two
// tmux commands: the keys, and the Enter that submits them. Both are chainable,
// so they still travel together.
func (p *Plan) SendKeys(target Ref, request SendKeysRequest) {
	request = captureSendKeysRequest(request)
	p.add(Op{
		name:         "send-keys",
		target:       target,
		needsVersion: sendKeysRequiresVersion(request),
		build: func(resolved, _ string, render planRenderContext) ([]string, error) {
			arguments, _, err := sendKeysArguments(resolved, request, render.version)
			return arguments, err
		},
	})
	if sendKeysNeedsEnter(request) {
		p.add(Op{
			name:   "send-keys",
			target: target,
			build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
				return enterArguments(resolved)
			},
		})
	}
}

// DisplayMessage records a format expanded against the object target names, and
// returns its output in the step's [OpResult]. The zero [Ref] expands it against
// no object, the way [Plan.Cmd] takes one.
//
// It runs alone because command-list stdout has no operation boundaries.
func (p *Plan) DisplayMessage(target Ref, format string) {
	p.add(Op{
		name:      "display-message",
		target:    target,
		captures:  true,
		untargets: target == Ref{},
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if resolved == "" {
				return untargetedArguments("display-message", "-p", format)
			}
			return targetedArguments("display-message", resolved, "-p", format)
		},
	})
}

// KillPane records the destruction of the pane target names.
func (p *Plan) KillPane(target Ref) {
	p.add(Op{
		name:   "kill-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("kill-pane", resolved)
		},
	})
}

// KillOtherPanes records the destruction of every pane in the window holding
// the pane target names, except that pane.
func (p *Plan) KillOtherPanes(target Ref) {
	p.add(Op{
		name:   "kill-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("kill-pane", resolved, "-a")
		},
	})
}

// SelectPane records a selection or marking of the pane target names.
func (p *Plan) SelectPane(target Ref, request PaneSelectRequest) {
	p.add(Op{
		name:   "select-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return paneSelectArguments(resolved, request)
		},
	})
}

// ResizePane records a resize of the pane target names.
func (p *Plan) ResizePane(target Ref, request ResizePaneRequest) {
	p.add(Op{
		name:   "resize-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return resizePaneArguments(resolved, request)
		},
	})
}

// SetPaneTitle records a title for the pane target names.
func (p *Plan) SetPaneTitle(target Ref, title string) {
	p.add(Op{
		name:   "select-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("select-pane", resolved, "-T", title)
		},
	})
}

// ClearHistory records the clearing of the scrollback of the pane target names.
func (p *Plan) ClearHistory(target Ref, request ClearHistoryRequest) {
	p.add(Op{
		name:         "clear-history",
		target:       target,
		needsVersion: request.ResetHyperlinks,
		build: func(resolved, _ string, render planRenderContext) ([]string, error) {
			arguments, _, err := clearHistoryArguments(resolved, request, render.version)
			return arguments, err
		},
	})
}

// SendPrefix records tmux's prefix key sent to the pane target names.
func (p *Plan) SendPrefix(target Ref, key PrefixKey) {
	p.add(Op{
		name:   "send-prefix",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return sendPrefixArguments(resolved, key)
		},
	})
}

// PipePane records the pane target names having its output piped to a shell
// command.
func (p *Plan) PipePane(target Ref, request PipePaneRequest) {
	p.add(Op{
		name:   "pipe-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return pipePaneArguments(resolved, request)
		},
	})
}

// RespawnPane records the pane target names being restarted with a new command.
func (p *Plan) RespawnPane(target Ref, request RespawnRequest) {
	p.add(Op{
		name:   "respawn-pane",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return respawnArguments("respawn-pane", resolved, request)
		},
	})
}

// SwapPane records the panes target and source name exchanging places.
//
// Both panes are Refs so either may be created by an earlier step. Detach leaves
// active selection alone; KeepZoom preserves a zoomed window.
func (p *Plan) SwapPane(target, source Ref, detach, keepZoom bool) {
	p.add(Op{
		name:   "swap-pane",
		target: target,
		source: source,
		build: func(resolved, from string, _ planRenderContext) ([]string, error) {
			arguments, err := targetedArguments("swap-pane", resolved, "-s", from)
			if err != nil {
				return nil, err
			}
			if detach {
				arguments = append(arguments, "-d")
			}
			if keepZoom {
				arguments = append(arguments, "-Z")
			}
			return arguments, nil
		},
	})
}

// JoinPane records the pane source names being moved beside the pane target
// names, splitting that pane's space.
//
// Horizontal splits left and right; Detach leaves active selection alone.
func (p *Plan) JoinPane(target, source Ref, horizontal, detach bool) {
	p.add(Op{
		name:   "join-pane",
		target: target,
		source: source,
		build: func(resolved, from string, _ planRenderContext) ([]string, error) {
			return joinPaneArguments("join-pane", resolved, from, horizontal, detach)
		},
	})
}

// MovePane records the pane source names being moved beside the pane target
// names without joining it to that pane's layout.
func (p *Plan) MovePane(target, source Ref, horizontal, detach bool) {
	p.add(Op{
		name:   "move-pane",
		target: target,
		source: source,
		build: func(resolved, from string, _ planRenderContext) ([]string, error) {
			return joinPaneArguments("move-pane", resolved, from, horizontal, detach)
		},
	})
}

// joinPaneArguments renders one join-pane or move-pane argument vector.
func joinPaneArguments(
	command string,
	target string,
	source string,
	horizontal bool,
	detach bool,
) ([]string, error) {
	arguments, err := targetedArguments(command, target, "-s", source)
	if err != nil {
		return nil, err
	}
	if horizontal {
		arguments = append(arguments, "-h")
	} else {
		arguments = append(arguments, "-v")
	}
	if detach {
		arguments = append(arguments, "-d")
	}
	return arguments, nil
}
