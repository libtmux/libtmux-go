package tmux

import (
	"bytes"
	"context"
	"errors"
)

// Options returns a freshly decoded, caller-owned view of known server options,
// including inherited values. A read or transport failure is returned rather
// than answered with zero values; context errors propagate.
// Each returned accessor names the setter that writes it, so
// [ServerOptionValues.BufferLimit] pairs with [Server.SetBufferLimit].
func (s Server) Options(ctx context.Context) (ServerOptionValues, error) {
	values, err := readTypedOptionValues(
		ctx,
		s,
		[]string{"-s"},
		generatedOptionDefinitions[:],
		generatedOptionScopeServer,
		false,
	)
	if err != nil {
		return ServerOptionValues{}, err
	}
	return newServerOptionValues(values), nil
}

// Options returns a freshly decoded, caller-owned view of known global session
// options, including defaults. A read failure is returned rather than answered
// with zero values; context errors propagate.
// Each returned accessor names the setter that writes it, so
// [SessionOptionValues.Status] pairs with [GlobalSessionScope.SetStatus].
func (s GlobalSessionScope) Options(ctx context.Context) (SessionOptionValues, error) {
	values, err := readTypedOptionValues(
		ctx,
		s.server,
		[]string{"-g"},
		generatedOptionDefinitions[:],
		generatedOptionScopeSession,
		false,
	)
	if err != nil {
		return SessionOptionValues{}, err
	}
	return newSessionOptionValues(values), nil
}

// Options returns a freshly decoded, caller-owned view of known global window
// options, including defaults. A read failure is returned rather than answered
// with zero values; context errors propagate.
// Each returned accessor names the setter that writes it, so
// [WindowOptionValues.MainPaneWidth] pairs with
// [GlobalWindowScope.SetMainPaneWidth].
func (s GlobalWindowScope) Options(ctx context.Context) (WindowOptionValues, error) {
	values, err := readTypedOptionValues(
		ctx,
		s.server,
		[]string{"-g", "-w"},
		generatedOptionDefinitions[:],
		generatedOptionScopeWindow,
		false,
	)
	if err != nil {
		return WindowOptionValues{}, err
	}
	return newWindowOptionValues(values), nil
}

// Options returns a freshly decoded, caller-owned view of known options at this
// stable session target, including inherited values. A read failure is returned
// rather than answered with zero values; context errors propagate.
// Each returned accessor names the setter that writes it, so
// [SessionOptionValues.Mouse] pairs with [Session.SetMouse].
func (s Session) Options(ctx context.Context) (SessionOptionValues, error) {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return SessionOptionValues{}, err
	}
	values, err := readTypedOptionValues(
		ctx,
		server,
		scope,
		generatedOptionDefinitions[:],
		generatedOptionScopeSession,
		false,
	)
	if err != nil {
		return SessionOptionValues{}, err
	}
	return newSessionOptionValues(values), nil
}

// Options returns a freshly decoded, caller-owned view of known options at this
// exact window target, including inherited values. The receiver's exact linked
// session context controls tmux format evaluation. A read failure is returned
// rather than answered with zero values.
// Each returned accessor names the setter that writes it, so
// [WindowOptionValues.MainPaneWidth] pairs with [Window.SetMainPaneWidth].
func (w Window) Options(ctx context.Context) (WindowOptionValues, error) {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return WindowOptionValues{}, err
	}
	values, err := readTypedOptionValues(
		ctx,
		server,
		scope,
		generatedOptionDefinitions[:],
		generatedOptionScopeWindow,
		false,
	)
	if err != nil {
		return WindowOptionValues{}, err
	}
	return newWindowOptionValues(values), nil
}

// Options returns a freshly decoded, caller-owned view of known options at this
// exact pane target, including inherited values. The receiver's exact linked
// session context controls tmux format evaluation. A read failure is returned
// rather than answered with zero values.
// Each returned accessor names the setter that writes it, so
// [PaneOptionValues.WindowStyle] pairs with [Pane.SetWindowStyle].
func (p Pane) Options(ctx context.Context) (PaneOptionValues, error) {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return PaneOptionValues{}, err
	}
	values, err := readTypedOptionValues(
		ctx,
		server,
		scope,
		generatedOptionDefinitions[:],
		generatedOptionScopePane,
		false,
	)
	if err != nil {
		return PaneOptionValues{}, err
	}
	return newPaneOptionValues(values), nil
}

// RawOption returns one exact server-scope option value. A successful string is
// caller-owned; ok reports presence. Targeted reads do not use list leniency,
// and completed failures return a secret-safe option error.
// An unindexed empty array is indistinguishable from an empty scalar; use
// Options for typed array presence.
func (s Server) RawOption(ctx context.Context, name string) (string, bool, error) {
	return rawOption(ctx, s, []string{"-s"}, name, false)
}

// RawOption returns one exact global session-option value. A
// successful string is caller-owned, and ok reports presence.
// An unindexed empty array is indistinguishable from an empty scalar; use
// Options for typed array presence.
func (s GlobalSessionScope) RawOption(
	ctx context.Context,
	name string,
) (string, bool, error) {
	return rawOption(ctx, s.server, []string{"-g"}, name, false)
}

// RawOption returns one exact global window-option value. A
// successful string is caller-owned, and ok reports presence.
// An unindexed empty array is indistinguishable from an empty scalar; use
// Options for typed array presence.
func (s GlobalWindowScope) RawOption(
	ctx context.Context,
	name string,
) (string, bool, error) {
	return rawOption(ctx, s.server, []string{"-g", "-w"}, name, false)
}

// RawOption returns one exact session option value at this stable session
// target. A successful string is caller-owned; ok reports presence, and a
// completed failure is returned rather than normalized.
// An unindexed empty array is indistinguishable from an empty scalar; use
// Options for typed array presence.
func (s Session) RawOption(ctx context.Context, name string) (string, bool, error) {
	server, scope, err := sessionOptionRuntimeScope(s)
	if err != nil {
		return "", false, err
	}
	return rawOption(ctx, server, scope, name, false)
}

// RawOption returns one exact window option value at this exact window target.
// A successful string is caller-owned; the receiver's exact linked session
// context controls tmux format evaluation, and a completed failure is returned.
func (w Window) RawOption(ctx context.Context, name string) (string, bool, error) {
	server, scope, err := windowOptionRuntimeScope(w)
	if err != nil {
		return "", false, err
	}
	return rawOption(ctx, server, scope, name, false)
}

// RawOption returns one exact pane option value at this exact pane target. A
// successful string is caller-owned; the receiver's exact linked session
// context controls tmux format evaluation, and a completed failure is returned.
func (p Pane) RawOption(ctx context.Context, name string) (string, bool, error) {
	server, scope, err := paneOptionRuntimeScope(p)
	if err != nil {
		return "", false, err
	}
	return rawOption(ctx, server, scope, name, false)
}

func readTypedOptionValues(
	ctx context.Context,
	server Server,
	scope []string,
	definitions []generatedOptionDefinition,
	generatedScope generatedOptionScope,
	hooks bool,
) ([]decodedOptionValue, error) {
	arguments := make([]string, 0, len(scope)+3)
	arguments = append(arguments, "show-options")
	arguments = append(arguments, scope...)
	arguments = append(arguments, "-A")
	if hooks {
		arguments = append(arguments, "-H")
	}
	result, raw, err := server.literalCmdWithRaw(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return nil, newOptionError("show-options", "", result)
	}
	var version *Version
	if optionOutputRequiresVersion(raw, definitions, generatedScope) {
		current, versionErr := server.Version(ctx)
		if versionErr != nil {
			if isContextOperationError(ctx, versionErr) {
				return nil, versionErr
			}
			_, classified := classifyOptionVersionBoundaryError(versionErr)
			return nil, classified
		}
		version = &current
	}
	return decodeOptionOutput(raw, definitions, generatedScope, hooks, version)
}

func classifyOptionVersionBoundaryError(err error) (bool, error) {
	if _, ok := errors.AsType[*versionTransportError](err); ok {
		return true, err
	}
	if query, ok := errors.AsType[*VersionQueryError](err); ok {
		if query.failedCommand() {
			return true, err
		}
		return false, &optionVersionBoundaryError{kind: ErrVersionQuery}
	}
	if errors.Is(err, ErrInvalidVersion) {
		return false, &optionVersionBoundaryError{kind: ErrInvalidVersion}
	}
	return false, err
}

func rawOption(
	ctx context.Context,
	server Server,
	scope []string,
	name string,
	hooks bool,
) (string, bool, error) {
	if err := validateServerCommandArgument(
		"show-options", "Name", name, true,
	); err != nil {
		return "", false, err
	}
	arguments := make([]string, 0, len(scope)+6)
	arguments = append(arguments, "show-options")
	arguments = append(arguments, scope...)
	if hooks {
		arguments = append(arguments, "-H")
	}
	arguments = append(arguments, "-q", "-v", "--", name)
	result, raw, err := server.literalCmdWithRaw(ctx, arguments...)
	if err != nil {
		return "", false, err
	}
	if len(raw) == 0 && len(result.Stderr) == 0 {
		return "", false, nil
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return "", false, newOptionError("show-options", name, result)
	}
	value, err := decodeTargetedOptionOutput(raw)
	if err != nil {
		return "", false, err
	}
	if len(raw) != 1 || raw[0] != '\n' {
		return value, true, nil
	}

	base, index, indexed := splitIndexedOptionName(name)
	if !indexed {
		return "", true, nil
	}
	if hooks {
		version, err := server.Version(ctx)
		if err != nil {
			return "", false, err
		}
		if !version.AtLeast(Version{major: 3, minor: 3}) {
			return "", false, nil
		}
	}
	present, err := queryIndexedOptionPresence(ctx, server, scope, name, base, index, hooks)
	if err != nil {
		return "", false, err
	}
	return "", present, nil
}

func queryIndexedOptionPresence(
	ctx context.Context,
	server Server,
	scope []string,
	name string,
	base string,
	index int,
	hooks bool,
) (bool, error) {
	arguments := make([]string, 0, len(scope)+5)
	arguments = append(arguments, "show-options")
	arguments = append(arguments, scope...)
	if hooks {
		arguments = append(arguments, "-H")
	}
	arguments = append(arguments, "-q", "--", base)
	result, raw, err := server.literalCmdWithRaw(ctx, arguments...)
	if err != nil {
		return false, err
	}
	if len(raw) == 0 && len(result.Stderr) == 0 {
		return false, nil
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return false, newOptionError("show-options", name, result)
	}
	return indexedOptionPresent(raw, base, index)
}

func indexedOptionPresent(raw []byte, base string, wanted int) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	if raw[len(raw)-1] != '\n' {
		return false, newBulkOptionDecodeError(
			bytes.Count(raw, []byte{'\n'}),
			"indexed base listing is missing its terminal line feed",
		)
	}
	last := -1
	present := false
	bare := false
	for record, line := range bytes.Split(raw[:len(raw)-1], []byte{'\n'}) {
		keyBytes, serialized, hasValue := bytes.Cut(line, []byte{' '})
		key := parseDecodedOptionKey(string(keyBytes))
		if !key.valid {
			return false, newBulkOptionDecodeError(record, "indexed base listing has a malformed key")
		}
		if key.base != base {
			return false, newBulkOptionDecodeError(record, "indexed base listing has an unexpected key")
		}
		if !key.indexed {
			if hasValue && len(serialized) != 0 {
				return false, newBulkOptionDecodeError(record, "indexed base listing has a nonempty bare value")
			}
			if last >= 0 {
				return false, newBulkOptionDecodeError(record, "indexed base listing mixes bare and indexed records")
			}
			bare = true
			continue
		}
		if bare {
			return false, newBulkOptionDecodeError(record, "indexed base listing mixes bare and indexed records")
		}
		if !hasValue {
			return false, newBulkOptionDecodeError(record, "indexed base listing value is missing")
		}
		if key.index <= last {
			return false, newBulkOptionDecodeError(record, "indexed base listing indices are not unique and ascending")
		}
		last = key.index
		if key.index == wanted {
			present = true
		}
	}
	return present, nil
}

func splitIndexedOptionName(name string) (string, int, bool) {
	key := parseDecodedOptionKey(name)
	if !key.valid || key.inherited || !key.indexed {
		return "", 0, false
	}
	return key.base, key.index, true
}

func isContextOperationError(ctx context.Context, err error) bool {
	return ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
