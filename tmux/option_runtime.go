package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
)

func encodeTypedOptionBool(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func encodeTypedOptionInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}

// SetArrayResult reports confirmed progress from a typed sparse-array
// replacement. AppliedIndices is caller-owned and is non-nil whenever mutation
// was attempted.
type SetArrayResult struct {
	// Replaced reports whether tmux confirmed the base replacement.
	Replaced bool
	// AppliedIndices lists confirmed indexed writes in ascending order.
	AppliedIndices []int
}

func setTypedOption(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	value string,
	choice bool,
) error {
	if err := validateServerCommandArgument("set-option", "Name", name, true); err != nil {
		return err
	}
	if err := validateServerCommandArgument("set-option", "Value", value, true); err != nil {
		return err
	}
	if choice {
		definition := generatedOptionDefinitionByName(name)
		if definition == nil || !generatedChoiceValueValid(*definition, value) {
			return &OptionValueError{Name: name}
		}
		if generatedChoiceDomainVariesByVersion(*definition, generatedScope) {
			version, err := server.Version(ctx)
			if err != nil {
				return err
			}
			variant := generatedActiveOptionVariant(*definition, version)
			if variant != nil && variant.scopes&generatedScope != 0 &&
				!generatedVariantChoiceValid(*variant, value) {
				return &OptionValueError{Name: name}
			}
		}
	}
	return changeOption(
		ctx, server, scope, generatedScope, name, value, SetOptionOptions{}, false,
	)
}

func setTypedOptionArray(
	ctx context.Context,
	server Server,
	scope []string,
	generatedScope generatedOptionScope,
	name string,
	value SparseArray[string],
) (SetArrayResult, error) {
	entries := value.Entries()
	slices.SortFunc(entries, func(left, right SparseEntry[string]) int {
		switch {
		case left.Index < right.Index:
			return -1
		case left.Index > right.Index:
			return 1
		default:
			return 0
		}
	})
	if err := validateServerCommandArgument("set-option", "Name", name, true); err != nil {
		return SetArrayResult{}, err
	}
	for index, entry := range entries {
		if entry.Index < 0 || entry.Index > math.MaxInt32 {
			return SetArrayResult{}, fmt.Errorf("%w: %d", ErrInvalidSparseIndex, entry.Index)
		}
		if index != 0 && entries[index-1].Index == entry.Index {
			return SetArrayResult{}, fmt.Errorf("%w: %d", ErrDuplicateSparseIndex, entry.Index)
		}
		if err := validateServerCommandArgument(
			"set-option", "Value", entry.Value, true,
		); err != nil {
			return SetArrayResult{}, err
		}
	}
	if err := preflightGeneratedMutation(
		ctx,
		server,
		"set-option",
		name,
		generatedOptionDefinitions[:],
		generatedOptionAliases[:],
		generatedScope,
		true,
	); err != nil {
		return SetArrayResult{}, err
	}

	result := SetArrayResult{AppliedIndices: make([]int, 0, len(entries))}
	if err := runTypedArrayOptionMutation(ctx, server, scope, name, ""); err != nil {
		return result, err
	}
	result.Replaced = true
	for _, entry := range entries {
		indexedName := name + "[" + strconv.Itoa(entry.Index) + "]"
		if err := runTypedArrayOptionMutation(ctx, server, scope, indexedName, entry.Value); err != nil {
			return result, err
		}
		result.AppliedIndices = append(result.AppliedIndices, entry.Index)
	}
	return result, nil
}

func runTypedArrayOptionMutation(
	ctx context.Context,
	server Server,
	scope []string,
	name string,
	value string,
) error {
	arguments := make([]string, 0, len(scope)+4)
	arguments = append(arguments, "set-option")
	arguments = append(arguments, scope...)
	arguments = append(arguments, "--", name, value)
	return runOptionMutation(ctx, server, arguments, name, false)
}

func generatedOptionDefinitionByName(name string) *generatedOptionDefinition {
	for index := range generatedOptionDefinitions {
		if generatedOptionDefinitions[index].name == name {
			return &generatedOptionDefinitions[index]
		}
	}
	return nil
}

func generatedChoiceValueValid(definition generatedOptionDefinition, value string) bool {
	for _, variant := range definition.variants {
		if generatedVariantChoiceValid(variant, value) {
			return true
		}
	}
	return false
}

func generatedVariantChoiceValid(variant generatedOptionVariant, value string) bool {
	if variant.kind == generatedOptionKindFlag {
		return value == "off" || value == "on"
	}
	return slices.Contains(variant.choices, value)
}

func generatedChoiceDomainVariesByVersion(
	definition generatedOptionDefinition,
	scope generatedOptionScope,
) bool {
	var baseline []string
	for _, variant := range definition.variants {
		if variant.scopes&scope == 0 {
			continue
		}
		domain := variant.choices
		if variant.kind == generatedOptionKindFlag {
			domain = []string{"off", "on"}
		}
		if baseline == nil {
			baseline = domain
			continue
		}
		if !slices.Equal(baseline, domain) {
			return true
		}
	}
	return false
}

func generatedActiveOptionVariant(
	definition generatedOptionDefinition,
	version Version,
) *generatedOptionVariant {
	var active *generatedOptionVariant
	for index := range definition.variants {
		if !version.AtLeast(definition.variants[index].minimum) {
			break
		}
		active = &definition.variants[index]
	}
	return active
}

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

// GlobalSessionScope is an immutable handle for global session options and
// hooks. Its zero value uses the zero [Server] and tmux's default connection.
type GlobalSessionScope struct {
	server Server
}

// GlobalWindowScope is an immutable handle for global window options and
// hooks. Its zero value uses the zero [Server] and tmux's default connection.
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
	var transport *versionTransportError
	if errors.As(err, &transport) {
		return true, err
	}
	var query *VersionQueryError
	if errors.As(err, &query) {
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
