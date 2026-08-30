package tmux

// Session and server operations a [Plan] can record. Each mirrors the method
// that runs the same tmux command immediately; see [Plan.SplitPane] for how a
// recorded operation is written.

// NewSession records a detached session and returns a [Ref] to it.
//
// KillExisting is rejected: [Server.NewSession] probes for the named session
// and removes it before creating, and a plan records without reading. Record
// [Plan.KillSession] before this instead, which says the same thing in the
// order it happens.
func (p *Plan) NewSession(request NewSessionRequest) Ref {
	request = captureNewSessionRequest(request)
	return p.add(Op{
		name:      "new-session",
		untargets: true,
		creates:   true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			if request.KillExisting {
				return nil, invalidLifecycleRequest(
					"KillExisting probes for an existing session before creating, " +
						"which a plan cannot do; record KillSession before this instead",
				)
			}
			return newSessionArguments(request)
		},
	})
}

// KillSession records the destruction of the session target names.
func (p *Plan) KillSession(target Ref) {
	p.add(Op{
		name:   "kill-session",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("kill-session", resolved)
		},
	})
}

// RenameSession records a new name for the session target names.
func (p *Plan) RenameSession(target Ref, name string) {
	p.add(Op{
		name:   "rename-session",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if err := validateLifecycleSessionName("name", name); err != nil {
				return nil, err
			}
			return targetedArguments("rename-session", resolved, name)
		},
	})
}

// NextWindow records the session target names moving to its next window.
func (p *Plan) NextWindow(target Ref) {
	p.add(Op{
		name:   "next-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("next-window", resolved)
		},
	})
}

// PreviousWindow records the session target names moving to its previous
// window.
func (p *Plan) PreviousWindow(target Ref) {
	p.add(Op{
		name:   "previous-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("previous-window", resolved)
		},
	})
}

// LastWindow records the session target names returning to its previously
// current window.
func (p *Plan) LastWindow(target Ref) {
	p.add(Op{
		name:   "last-window",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("last-window", resolved)
		},
	})
}

// LockSession records every client attached to the session target names being
// locked.
func (p *Plan) LockSession(target Ref) {
	p.add(Op{
		name:   "lock-session",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			return targetedArguments("lock-session", resolved)
		},
	})
}

// DetachClients records every client attached to the session target names being
// detached.
func (p *Plan) DetachClients(target Ref) {
	p.add(Op{
		name:   "detach-client",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			// detach-client names its session with -s rather than -t, so this
			// is untargetedArguments with the selector spelled out; it still
			// validates every argument the way the targeted form does.
			return untargetedArguments("detach-client", "-s", resolved)
		},
	})
}

// SetEnvironment records a variable set in the session target names, or in the
// server when target is the zero [Ref].
func (p *Plan) SetEnvironment(target Ref, name, value string) {
	p.add(Op{
		name:   "set-environment",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if err := validateEnvironmentName(name); err != nil {
				return nil, err
			}
			if err := validateEnvironmentValue(value); err != nil {
				return nil, err
			}
			return targetedArguments("set-environment", resolved, name, value)
		},
	})
}

// UnsetEnvironment records a variable removed from the session target names, or
// from the server when target is the zero [Ref].
func (p *Plan) UnsetEnvironment(target Ref, name string) {
	p.add(Op{
		name:   "set-environment",
		target: target,
		build: func(resolved, _ string, _ planRenderContext) ([]string, error) {
			if err := validateEnvironmentName(name); err != nil {
				return nil, err
			}
			return targetedArguments("set-environment", resolved, "-u", name)
		},
	})
}

// SourceFile records a tmux configuration file being loaded.
func (p *Plan) SourceFile(request SourceFileRequest) {
	p.add(Op{
		name:      "source-file",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			return sourceFileArguments(request)
		},
	})
}

// KillServer records the tmux server being shut down.
// Nothing recorded after it can run because the server will be gone.
func (p *Plan) KillServer() {
	p.add(Op{
		name:      "kill-server",
		untargets: true,
		build: func(_, _ string, _ planRenderContext) ([]string, error) {
			return untargetedArguments("kill-server")
		},
	})
}
