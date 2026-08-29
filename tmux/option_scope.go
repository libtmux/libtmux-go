package tmux

// GlobalSessionScope is an immutable handle for global session options and
// hooks. Its zero value is invalid because it contains a zero [Server].
type GlobalSessionScope struct {
	server Server
}

// GlobalWindowScope is an immutable handle for global window options and
// hooks. Its zero value is invalid because it contains a zero [Server].
type GlobalWindowScope struct {
	server Server
}

// GlobalSessionScope returns a handle that preserves s and its error policy.
func (s Server) GlobalSessionScope() GlobalSessionScope {
	return GlobalSessionScope{server: s}
}

// GlobalWindowScope returns a handle that preserves s and its error policy.
func (s Server) GlobalWindowScope() GlobalWindowScope {
	return GlobalWindowScope{server: s}
}

func sessionOptionRuntimeScope(session Session) (Server, []string, error) {
	target := session.sessionID.String()
	if err := validateServerCommandArgument(
		"show-options", "Target", target, true,
	); err != nil {
		return Server{}, nil, err
	}
	if err := validateTypedTarget("show-options", "Target", "session", target); err != nil {
		return Server{}, nil, err
	}
	return session.server, []string{"-t", target}, nil
}

func windowOptionRuntimeScope(window Window) (Server, []string, error) {
	target, err := exactWindowTarget(window)
	if err != nil {
		return Server{}, nil, err
	}
	return window.server, []string{"-t", target, "-w"}, nil
}

func paneOptionRuntimeScope(pane Pane) (Server, []string, error) {
	target, err := exactPaneTarget(pane)
	if err != nil {
		return Server{}, nil, err
	}
	return pane.server, []string{"-t", target, "-p"}, nil
}
