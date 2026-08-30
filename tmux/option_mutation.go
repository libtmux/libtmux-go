package tmux

import "context"

// SetOptionOptions controls set-option mutation flags. Its zero value sends no
// optional mutation flags.
type SetOptionOptions struct {
	// ExpandFormat expands tmux formats in the value before storing it.
	ExpandFormat bool
	// PreventOverwrite leaves an existing option unchanged.
	PreventOverwrite bool
	// Quiet suppresses tmux's missing-option diagnostic.
	Quiet bool
}

// UnsetOptionOptions controls set-option unset behavior. Its zero value unsets
// only the selected option.
type UnsetOptionOptions struct {
	// UnsetPanes unsets a window option on every pane in a Window receiver.
	UnsetPanes bool
	// Quiet suppresses tmux's missing-option diagnostic.
	Quiet bool
}

// SetOption stores a server-scope option without refreshing existing models.
// It may validate a known option against the live version. Completed failures
// return secret-safe option errors; cancellation does not prove the mutation
// was not accepted by tmux.
func (s Server) SetOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	return changeOption(
		ctx,
		s,
		[]string{"-s"},
		generatedOptionScopeServer,
		name,
		value,
		options,
		false,
	)
}

// SetOption stores a global session option without refreshing
// existing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the mutation.
func (s GlobalSessionScope) SetOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	return changeOption(
		ctx,
		s.server,
		[]string{"-g"},
		generatedOptionScopeSession,
		name,
		value,
		options,
		false,
	)
}

// SetOption stores a global window option without refreshing
// existing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the mutation.
func (s GlobalWindowScope) SetOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	return changeOption(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		value,
		options,
		false,
	)
}

// SetOption stores a session option at this stable session target without
// refreshing existing models. Completed failures are secret-safe option errors;
// cancellation does not prove tmux did not accept the mutation.
func (s Session) SetOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return err
	}
	return changeOption(ctx, server, scope, generatedOptionScopeSession, name, value, options, false)
}

// SetOption stores a window option at this exact window target without
// refreshing existing models. Completed failures are secret-safe option errors;
// cancellation does not prove tmux did not accept the mutation.
func (w Window) SetOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return err
	}
	return changeOption(ctx, server, scope, generatedOptionScopeWindow, name, value, options, false)
}

// SetOption stores a pane option at this exact pane target without refreshing
// existing models. Completed failures are secret-safe option errors; cancellation
// does not prove tmux did not accept the mutation.
func (p Pane) SetOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return err
	}
	return changeOption(ctx, server, scope, generatedOptionScopePane, name, value, options, false)
}

// AppendOption appends to a server-scope option without refreshing existing
// models. Completed failures are secret-safe option errors; cancellation does
// not prove the append was not accepted.
func (s Server) AppendOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	return changeOption(
		ctx,
		s,
		[]string{"-s"},
		generatedOptionScopeServer,
		name,
		value,
		options,
		true,
	)
}

// AppendOption appends to a global session option without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove the append was not accepted.
func (s GlobalSessionScope) AppendOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	return changeOption(
		ctx,
		s.server,
		[]string{"-g"},
		generatedOptionScopeSession,
		name,
		value,
		options,
		true,
	)
}

// AppendOption appends to a global window option without refreshing
// models. Completed failures are secret-safe option errors; cancellation does
// not prove the append was not accepted.
func (s GlobalWindowScope) AppendOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	return changeOption(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		value,
		options,
		true,
	)
}

// AppendOption appends to a session option at this stable session target without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove the append was not accepted.
func (s Session) AppendOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return err
	}
	return changeOption(ctx, server, scope, generatedOptionScopeSession, name, value, options, true)
}

// AppendOption appends to a window option at this exact window target without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove the append was not accepted.
func (w Window) AppendOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return err
	}
	return changeOption(ctx, server, scope, generatedOptionScopeWindow, name, value, options, true)
}

// AppendOption appends to a pane option at this exact pane target without
// refreshing models. Completed failures are secret-safe option errors; cancellation
// does not prove the append was not accepted.
func (p Pane) AppendOption(
	ctx context.Context,
	name string,
	value string,
	options SetOptionOptions,
) error {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return err
	}
	return changeOption(ctx, server, scope, generatedOptionScopePane, name, value, options, true)
}

// UnsetOption unsets a server-scope option without refreshing existing models.
// UnsetPanes is invalid at server scope. Completed failures are secret-safe
// option errors; cancellation does not prove the unset was not accepted.
func (s Server) UnsetOption(
	ctx context.Context,
	name string,
	options UnsetOptionOptions,
) error {
	if options.UnsetPanes {
		return newLocalInvalidOptionError("set-option", name)
	}
	return unsetOption(ctx, s, []string{"-s"}, generatedOptionScopeServer, name, options, false)
}

// UnsetOption unsets a global session option without refreshing
// models. UnsetPanes is invalid at this scope; cancellation does not prove the
// unset was not accepted.
func (s GlobalSessionScope) UnsetOption(
	ctx context.Context,
	name string,
	options UnsetOptionOptions,
) error {
	if options.UnsetPanes {
		return newLocalInvalidOptionError("set-option", name)
	}
	return unsetOption(
		ctx,
		s.server,
		[]string{"-g"},
		generatedOptionScopeSession,
		name,
		options,
		false,
	)
}

// UnsetOption unsets a global window option without refreshing
// models. UnsetPanes is invalid at this scope; cancellation does not prove the
// unset was not accepted.
func (s GlobalWindowScope) UnsetOption(
	ctx context.Context,
	name string,
	options UnsetOptionOptions,
) error {
	if options.UnsetPanes {
		return newLocalInvalidOptionError("set-option", name)
	}
	return unsetOption(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionScopeWindow,
		name,
		options,
		false,
	)
}

// UnsetOption unsets a session option at this stable session target without
// refreshing models. UnsetPanes is invalid at this scope; cancellation does
// not prove the unset was not accepted.
func (s Session) UnsetOption(
	ctx context.Context,
	name string,
	options UnsetOptionOptions,
) error {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return err
	}
	if options.UnsetPanes {
		return newLocalInvalidOptionError("set-option", name)
	}
	return unsetOption(ctx, server, scope, generatedOptionScopeSession, name, options, false)
}

// UnsetOption unsets a window option at this exact window target, or its pane
// copies with UnsetPanes, without refreshing models. Cancellation does not
// prove the unset was not accepted.
func (w Window) UnsetOption(
	ctx context.Context,
	name string,
	options UnsetOptionOptions,
) error {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return err
	}
	return unsetOption(
		ctx,
		server,
		scope,
		generatedOptionScopeWindow,
		name,
		options,
		options.UnsetPanes,
	)
}

// UnsetOption unsets a pane option at this exact pane target without refreshing
// models. UnsetPanes is invalid at this scope; cancellation does not prove the
// unset was not accepted.
func (p Pane) UnsetOption(
	ctx context.Context,
	name string,
	options UnsetOptionOptions,
) error {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return err
	}
	if options.UnsetPanes {
		return newLocalInvalidOptionError("set-option", name)
	}
	return unsetOption(ctx, server, scope, generatedOptionScopePane, name, options, false)
}

func changeOption(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	value string,
	options SetOptionOptions,
	appendValue bool,
) error {
	if err := validateServerCommandArgument(
		"set-option", "Name", name, true,
	); err != nil {
		return err
	}
	if err := validateServerCommandArgument(
		"set-option", "Value", value, true,
	); err != nil {
		return err
	}
	if err := preflightGeneratedMutation(
		ctx,
		server,
		"set-option",
		name,
		generatedOptionDefinitions[:],
		generatedOptionAliases[:],
		generatedScope,
		false,
	); err != nil {
		return err
	}
	arguments := make([]string, 0, len(scope)+8)
	arguments = append(arguments, "set-option")
	arguments = append(arguments, scope...)
	if appendValue {
		arguments = append(arguments, "-a")
	}
	if options.ExpandFormat {
		arguments = append(arguments, "-F")
	}
	if options.PreventOverwrite {
		arguments = append(arguments, "-o")
	}
	if options.Quiet {
		arguments = append(arguments, "-q")
	}
	arguments = append(arguments, "--", name, value)
	return runOptionMutation(ctx, server, arguments, name, options.Quiet)
}

func unsetOption(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	options UnsetOptionOptions,
	unsetPanes bool,
) error {
	if err := validateServerCommandArgument(
		"set-option", "Name", name, true,
	); err != nil {
		return err
	}
	if err := preflightGeneratedMutation(
		ctx,
		server,
		"set-option",
		name,
		generatedOptionDefinitions[:],
		generatedOptionAliases[:],
		generatedScope,
		false,
	); err != nil {
		return err
	}
	arguments := make([]string, 0, len(scope)+5)
	arguments = append(arguments, "set-option")
	arguments = append(arguments, scope...)
	if unsetPanes {
		arguments = append(arguments, "-U")
	} else {
		arguments = append(arguments, "-u")
	}
	if options.Quiet {
		arguments = append(arguments, "-q")
	}
	arguments = append(arguments, "--", name)
	return runOptionMutation(ctx, server, arguments, name, options.Quiet)
}

func runOptionMutation(
	ctx context.Context,
	server Server,
	arguments []string,
	name string,
	quiet bool,
) error {
	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		return err
	}
	if quiet && len(result.Stdout) == 0 && len(result.Stderr) == 0 {
		return nil
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return newOptionError(arguments[0], name, result)
	}
	return nil
}

func preflightGeneratedMutation(
	ctx context.Context,
	server Server,
	subcommand string,
	name string,
	definitions []generatedOptionDefinition,
	aliases []generatedOptionAlias,
	scope generatedOptionScope,
	requireKnown bool,
) error {
	key := parseDecodedOptionKey(name)
	base := name
	if key.base != "" {
		base = key.base
	}
	for _, alias := range aliases {
		if alias.name == base {
			base = alias.canonical
			break
		}
	}

	var definition *generatedOptionDefinition
	for index := range definitions {
		if definitions[index].name == base {
			definition = &definitions[index]
			break
		}
	}
	if definition == nil {
		if requireKnown {
			return newLocalInvalidOptionError(subcommand, name)
		}
		return nil
	}
	if !generatedDefinitionSupportsScope(*definition, scope) {
		return newLocalInvalidOptionError(subcommand, name)
	}
	if !generatedDefinitionScopeVariesByVersion(*definition, scope) {
		return nil
	}

	version, err := server.Version(ctx)
	if err != nil {
		return err
	}
	if !generatedDefinitionSupportsVersionScope(*definition, version, scope) {
		return newLocalInvalidOptionError(subcommand, name)
	}
	return nil
}
