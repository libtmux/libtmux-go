package tmux

// Server operations a [Plan] can record: the ones that act on the tmux server
// or a client rather than on a session, window, or pane. See [Plan.SplitPane]
// for how a recorded operation is written.

// StartServer records the tmux server being started if it is not running.
func (p *Plan) StartServer() {
	p.add(Op{
		name:      "start-server",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			return untargetedArguments("start-server")
		},
	})
}

// SetOption records a tmux option written on the object target names, or on the
// server when target is the zero [Ref] and Global is set.
//
// It takes the option's tmux name because typed accessors read values back.
func (p *Plan) SetOption(target Ref, request SetPlanOptionRequest) {
	p.add(Op{
		name:      "set-option",
		target:    target,
		untargets: request.Global && target == Ref{},
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return setOptionArguments(resolved, request)
		},
	})
}

// SetPlanOptionRequest names one tmux option write for [Plan.SetOption]. Its
// zero value is invalid, because Name is required.
type SetPlanOptionRequest struct {
	// Name is the tmux option name, such as "status" or a user option "@thing".
	Name string
	// Value is the value to write. It is omitted when Unset is set.
	Value string
	// Global writes the server or global scope rather than the target's own.
	Global bool
	// Window writes a window option rather than a session or server one.
	Window bool
	// Pane writes a pane option.
	Pane bool
	// Unset removes the option instead of writing it.
	Unset bool
	// Append adds to an existing value rather than replacing it.
	Append bool
}

// setOptionArguments renders one set-option argument vector.
func setOptionArguments(target string, request SetPlanOptionRequest) ([]string, error) {
	if request.Name == "" {
		return nil, invalidServerCommandRequest(
			"set-option", "Name", "", "must not be empty")
	}
	if request.Window && request.Pane {
		return nil, invalidServerCommandRequest(
			"set-option", "Scope", "", "Window and Pane are mutually exclusive")
	}
	arguments := []string{"set-option"}
	if request.Global {
		arguments = append(arguments, "-g")
	}
	if request.Window {
		arguments = append(arguments, "-w")
	}
	if request.Pane {
		arguments = append(arguments, "-p")
	}
	if request.Unset {
		arguments = append(arguments, "-u")
	}
	if request.Append {
		arguments = append(arguments, "-a")
	}
	if target != "" {
		arguments = append(arguments, "-t", target)
	}
	arguments = append(arguments, request.Name)
	if !request.Unset {
		arguments = append(arguments, request.Value)
	}
	for _, argument := range arguments {
		if err := validateServerCommandArgument(
			"set-option", "Arguments", argument, true,
		); err != nil {
			return nil, err
		}
	}
	return arguments, nil
}

// SetHook records a tmux hook written on the object target names.
func (p *Plan) SetHook(target Ref, name, command string, global bool) {
	p.add(Op{
		name:      "set-hook",
		target:    target,
		untargets: global && target == Ref{},
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if name == "" {
				return nil, invalidServerCommandRequest(
					"set-hook", "Name", "", "must not be empty")
			}
			arguments := []string{"set-hook"}
			if global {
				arguments = append(arguments, "-g")
			}
			if resolved != "" {
				arguments = append(arguments, "-t", resolved)
			}
			return append(arguments, name, command), nil
		},
	})
}

// SetBuffer records text stored in a tmux buffer.
func (p *Plan) SetBuffer(name, data string) {
	p.add(Op{
		name:      "set-buffer",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			if name == "" {
				return untargetedArguments("set-buffer", data)
			}
			return untargetedArguments("set-buffer", "-b", name, data)
		},
	})
}

// DeleteBuffer records a tmux buffer being removed.
// An empty name removes the most recently added buffer.
func (p *Plan) DeleteBuffer(name string) {
	p.add(Op{
		name:      "delete-buffer",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			if name == "" {
				return untargetedArguments("delete-buffer")
			}
			return untargetedArguments("delete-buffer", "-b", name)
		},
	})
}

// DetachClient records one client being detached.
// A client is named directly because plans do not create clients.
func (p *Plan) DetachClient(client ClientName) {
	p.add(Op{
		name:      "detach-client",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			return untargetedArguments("detach-client", "-t", client.String())
		},
	})
}

// SuspendClient records one client being suspended.
func (p *Plan) SuspendClient(client ClientName) {
	p.add(Op{
		name:      "suspend-client",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			return untargetedArguments("suspend-client", "-t", client.String())
		},
	})
}

// RefreshClient records one client being asked to redraw.
func (p *Plan) RefreshClient(client ClientName) {
	p.add(Op{
		name:      "refresh-client",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			return untargetedArguments("refresh-client", "-t", client.String())
		},
	})
}

// SwitchClient records one client being moved to the session target names.
// target may refer to a session an earlier step creates.
func (p *Plan) SwitchClient(target Ref, client ClientName) {
	p.add(Op{
		name:   "switch-client",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("switch-client", resolved, "-c", client.String())
		},
	})
}

// LockServer records every client attached to the server being locked.
func (p *Plan) LockServer() {
	p.add(Op{
		name:      "lock-server",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			return untargetedArguments("lock-server")
		},
	})
}
