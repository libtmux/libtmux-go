package tmux

// Pane operations a [Plan] can record. Each mirrors the method that runs the
// same tmux command immediately, takes the same request type, and returns a
// [Ref] where the method returns a record. A plan is therefore written the way
// the same work is written without one, and the two share the rendering, so a
// flag cannot mean one thing when it runs and another when it is planned.

// SplitPane records a split of the window or pane target names, and returns a
// [Ref] to the pane it will create.
//
// It mirrors [Window.SplitPane] and [Pane.Split] and takes the same request.
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
		marks:        request.Attach,
		needsVersion: splitPaneRequiresVersion(request),
		build: func(resolved, _ string, version Version) ([]string, error) {
			arguments, warnings, err := splitPaneArguments(resolved, request, version)
			if err != nil {
				return nil, err
			}
			if err := p.reportUnsupported(warnings); err != nil {
				return nil, err
			}
			return arguments, nil
		},
	})
}

// SendKeys records keys sent to the pane target names.
//
// It mirrors [Pane.SendKeys] and takes the same request. Sending keys produces
// no output and creates nothing, so it shares a dispatch with its neighbours;
// see [Plan.Explain].
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
		build: func(resolved, _ string, version Version) ([]string, error) {
			arguments, _, err := sendKeysArguments(resolved, request, version)
			return arguments, err
		},
	})
	if sendKeysNeedsEnter(request) {
		p.add(Op{
			name:   "send-keys",
			target: target,
			build: func(resolved, _ string, _ Version) ([]string, error) {
				return enterArguments(resolved)
			},
		})
	}
}

// DisplayMessage records a format expanded against the object target names, and
// returns its output in the step's [OpResult]. The zero [Ref] expands it against
// no object, the way [Plan.Cmd] takes one.
//
// It mirrors [Pane.DisplayMessage]. Reading output is what stops an operation
// sharing a dispatch: tmux merges a command list into one stdout with no
// boundary, so this one is sent on its own. [Plan.Explain] reports that.
func (p *Plan) DisplayMessage(target Ref, format string) {
	p.add(Op{
		name:      "display-message",
		target:    target,
		captures:  true,
		untargets: target == Ref{},
		build: func(resolved, _ string, _ Version) ([]string, error) {
			if resolved == "" {
				return untargetedArguments("display-message", "-p", format)
			}
			return targetedArguments("display-message", resolved, "-p", format)
		},
	})
}

// KillPane records the destruction of the pane target names.
//
// It mirrors [Pane.Kill].
func (p *Plan) KillPane(target Ref) {
	p.add(Op{
		name:   "kill-pane",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return targetedArguments("kill-pane", resolved)
		},
	})
}

// KillOtherPanes records the destruction of every pane in the window holding
// the pane target names, except that pane.
//
// It mirrors [Pane.KillOthers].
func (p *Plan) KillOtherPanes(target Ref) {
	p.add(Op{
		name:   "kill-pane",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return targetedArguments("kill-pane", resolved, "-a")
		},
	})
}

// SelectPane records a selection or marking of the pane target names.
//
// It mirrors [Pane.Select] and takes the same request.
func (p *Plan) SelectPane(target Ref, request PaneSelectRequest) {
	p.add(Op{
		name:   "select-pane",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return paneSelectArguments(resolved, request)
		},
	})
}

// ResizePane records a resize of the pane target names.
//
// It mirrors [Pane.Resize] and takes the same request.
func (p *Plan) ResizePane(target Ref, request ResizePaneRequest) {
	p.add(Op{
		name:   "resize-pane",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return resizePaneArguments(resolved, request)
		},
	})
}

// SetPaneTitle records a title for the pane target names.
//
// It mirrors [Pane.SetTitle].
func (p *Plan) SetPaneTitle(target Ref, title string) {
	p.add(Op{
		name:   "select-pane",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return targetedArguments("select-pane", resolved, "-T", title)
		},
	})
}

// ClearHistory records the clearing of the scrollback of the pane target names.
//
// It mirrors [Pane.ClearHistory] and takes the same request.
func (p *Plan) ClearHistory(target Ref, request ClearHistoryRequest) {
	p.add(Op{
		name:         "clear-history",
		target:       target,
		needsVersion: request.ResetHyperlinks,
		build: func(resolved, _ string, version Version) ([]string, error) {
			arguments, _, err := clearHistoryArguments(resolved, request, version)
			return arguments, err
		},
	})
}

// SendPrefix records tmux's prefix key sent to the pane target names.
//
// It mirrors [Pane.SendPrefix].
func (p *Plan) SendPrefix(target Ref, key PrefixKey) {
	p.add(Op{
		name:   "send-prefix",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return sendPrefixArguments(resolved, key)
		},
	})
}

// PipePane records the pane target names having its output piped to a shell
// command.
//
// It mirrors [Pane.Pipe] and takes the same request.
func (p *Plan) PipePane(target Ref, request PipePaneRequest) {
	p.add(Op{
		name:   "pipe-pane",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return pipePaneArguments(resolved, request)
		},
	})
}

// RespawnPane records the pane target names being restarted with a new command.
//
// It mirrors [Pane.Respawn] and takes the same request.
func (p *Plan) RespawnPane(target Ref, request RespawnRequest) {
	p.add(Op{
		name:   "respawn-pane",
		target: target,
		build: func(resolved, _ string, _ Version) ([]string, error) {
			return respawnArguments("respawn-pane", resolved, request)
		},
	})
}

// SwapPane records the panes target and source name exchanging places.
//
// It mirrors [Pane.Swap], but names both panes rather than taking that method's
// request: the request selects the other pane as a materialized [Pane], and a
// plan needs to be able to name one an earlier step will create. Detach leaves
// the active-pane selection alone, and KeepZoom preserves a zoomed window.
func (p *Plan) SwapPane(target, source Ref, detach, keepZoom bool) {
	p.add(Op{
		name:   "swap-pane",
		target: target,
		source: source,
		build: func(resolved, from string, _ Version) ([]string, error) {
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
// It mirrors [Pane.Join], but names both panes for the reason [Plan.SwapPane]
// does. Horizontal splits left and right rather than above and below, and
// Detach leaves the active-pane selection alone.
func (p *Plan) JoinPane(target, source Ref, horizontal, detach bool) {
	p.add(Op{
		name:   "join-pane",
		target: target,
		source: source,
		build: func(resolved, from string, _ Version) ([]string, error) {
			return joinPaneArguments("join-pane", resolved, from, horizontal, detach)
		},
	})
}

// MovePane records the pane source names being moved beside the pane target
// names without joining it to that pane's layout.
//
// It mirrors [Pane.Move], and names both panes for the reason [Plan.SwapPane]
// does.
func (p *Plan) MovePane(target, source Ref, horizontal, detach bool) {
	p.add(Op{
		name:   "move-pane",
		target: target,
		source: source,
		build: func(resolved, from string, _ Version) ([]string, error) {
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
