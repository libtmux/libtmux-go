package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidEnvironmentName identifies a name tmux cannot store.
	ErrInvalidEnvironmentName = errors.New("tmux: invalid environment variable name")
	// ErrInvalidEnvironmentValue identifies a value the line-oriented read API cannot round-trip.
	ErrInvalidEnvironmentValue = errors.New("tmux: invalid environment variable value")
	// ErrMalformedEnvironment identifies output that is not a tmux environment entry.
	ErrMalformedEnvironment = errors.New("tmux: malformed environment output")
)

// SetEnvironmentOptions controls tmux's value expansion and visibility flags.
// Its zero value stores Value literally and exposes it to child processes.
type SetEnvironmentOptions struct {
	// ExpandFormat expands tmux format expressions in Value before storing it.
	ExpandFormat bool
	// Hidden keeps the value available to tmux but out of child environments.
	Hidden bool
}

// EnvironmentValue is one tmux environment value or removal marker. Its zero
// value is an ordinary empty value, not a removal marker.
type EnvironmentValue struct {
	// Value is the exact value after tmux format expansion, if any.
	Value string
	// Removed reports that tmux will remove the variable from new processes.
	Removed bool
}

// EnvironmentNameError reports a name that tmux cannot store.
type EnvironmentNameError struct {
	// Name is the invalid environment variable name.
	Name string
}

// Error implements error.
func (e *EnvironmentNameError) Error() string {
	return fmt.Sprintf("%v %q", ErrInvalidEnvironmentName, e.Name)
}

// Unwrap makes EnvironmentNameError compatible with ErrInvalidEnvironmentName.
func (e *EnvironmentNameError) Unwrap() error { return ErrInvalidEnvironmentName }

// EnvironmentValueError reports a value that cannot round-trip through show-environment.
// It intentionally does not retain or print the environment value.
type EnvironmentValueError struct{}

// Error implements error without disclosing the environment value.
func (*EnvironmentValueError) Error() string {
	return ErrInvalidEnvironmentValue.Error() + ": contains a line or NUL delimiter"
}

// Unwrap makes EnvironmentValueError compatible with ErrInvalidEnvironmentValue.
func (*EnvironmentValueError) Unwrap() error { return ErrInvalidEnvironmentValue }

// EnvironmentDecodeError reports one malformed show-environment record.
// It never retains the record because environment output may contain secrets.
type EnvironmentDecodeError struct {
	// Record is the zero-based malformed output record number.
	Record int
	// Reason describes malformed framing without retaining the record value.
	Reason string
}

// Error implements error.
func (e *EnvironmentDecodeError) Error() string {
	return fmt.Sprintf("%v: record %d: %s", ErrMalformedEnvironment, e.Record, e.Reason)
}

// Unwrap makes EnvironmentDecodeError compatible with ErrMalformedEnvironment.
func (e *EnvironmentDecodeError) Unwrap() error { return ErrMalformedEnvironment }

// SetEnvironment stores a value in the server's global tmux environment.
// ExpandFormat and Hidden select tmux flags; cancellation does not prove the
// value was not stored.
func (s Server) SetEnvironment(
	ctx context.Context,
	name string,
	value string,
	options SetEnvironmentOptions,
) error {
	return setEnvironment(ctx, s, []string{"-g"}, name, value, options)
}

// UnsetEnvironment deletes a value from the server's global tmux environment.
// Cancellation does not prove deletion did not occur.
func (s Server) UnsetEnvironment(ctx context.Context, name string) error {
	return changeEnvironmentState(ctx, s, []string{"-g"}, "-u", name)
}

// RemoveEnvironment marks a variable for removal from new process environments.
func (s Server) RemoveEnvironment(ctx context.Context, name string) error {
	return changeEnvironmentState(ctx, s, []string{"-g"}, "-r", name)
}

// ShowEnvironment returns an owned server-global non-hidden tmux environment.
// Completed command failures return an empty map unless strict errors are enabled.
// Externally injected multiline entries return ErrMalformedEnvironment because
// tmux's multi-entry output does not frame continuation lines. Decode errors
// are compatible with [ErrMalformedEnvironment] and return no partial map.
func (s Server) ShowEnvironment(ctx context.Context) (map[string]EnvironmentValue, error) {
	return showEnvironment(ctx, s, []string{"-g"})
}

// GetEnvironment returns one non-hidden global environment entry.
// A missing or hidden variable reports ok false.
func (s Server) GetEnvironment(
	ctx context.Context,
	name string,
) (EnvironmentValue, bool, error) {
	return getEnvironment(ctx, s, []string{"-g"}, name)
}

// SetEnvironment stores a value in this session's tmux environment, targeted
// by its stable SessionID.
func (s Session) SetEnvironment(
	ctx context.Context,
	name string,
	value string,
	options SetEnvironmentOptions,
) error {
	server, scope, err := s.environmentScope()
	if err != nil {
		return err
	}
	return setEnvironment(ctx, server, scope, name, value, options)
}

// UnsetEnvironment deletes a value from this session's tmux environment,
// targeted by its stable SessionID.
func (s Session) UnsetEnvironment(ctx context.Context, name string) error {
	server, scope, err := s.environmentScope()
	if err != nil {
		return err
	}
	return changeEnvironmentState(ctx, server, scope, "-u", name)
}

// RemoveEnvironment marks a variable for removal from new process environments
// for this stable session target.
func (s Session) RemoveEnvironment(ctx context.Context, name string) error {
	server, scope, err := s.environmentScope()
	if err != nil {
		return err
	}
	return changeEnvironmentState(ctx, server, scope, "-r", name)
}

// ShowEnvironment returns an owned session-scoped non-hidden tmux environment.
// Completed command failures return an empty map unless strict errors are enabled.
// Externally injected multiline entries return ErrMalformedEnvironment because
// tmux's multi-entry output does not frame continuation lines. Decode errors
// are compatible with [ErrMalformedEnvironment] and return no partial map.
func (s Session) ShowEnvironment(ctx context.Context) (map[string]EnvironmentValue, error) {
	server, scope, err := s.environmentScope()
	if err != nil {
		return nil, err
	}
	return showEnvironment(ctx, server, scope)
}

// GetEnvironment returns one non-hidden session environment entry for this
// stable session target.
// A missing or hidden variable reports ok false.
func (s Session) GetEnvironment(
	ctx context.Context,
	name string,
) (EnvironmentValue, bool, error) {
	server, scope, err := s.environmentScope()
	if err != nil {
		return EnvironmentValue{}, false, err
	}
	return getEnvironment(ctx, server, scope, name)
}

func (s Session) environmentScope() (Server, []string, error) {
	target := s.sessionID.String()
	if err := validateServerCommandArgument(
		"environment", "Target", target, true,
	); err != nil {
		return Server{}, nil, err
	}
	if err := validateTypedTarget("environment", "Target", "session", target); err != nil {
		return Server{}, nil, err
	}
	return s.server, []string{"-t", target}, nil
}

func setEnvironment(
	ctx context.Context,
	server Server,
	scope []string,
	name string,
	value string,
	options SetEnvironmentOptions,
) error {
	if err := validateEnvironmentName(name); err != nil {
		return err
	}
	if err := validateEnvironmentValue(value); err != nil {
		return err
	}
	arguments := make([]string, 0, 6+len(scope))
	arguments = append(arguments, "set-environment")
	arguments = append(arguments, scope...)
	if options.ExpandFormat {
		arguments = append(arguments, "-F")
	}
	if options.Hidden {
		arguments = append(arguments, "-h")
	}
	arguments = append(arguments, "--", name, value)
	return runEnvironmentMutation(ctx, server, arguments)
}

func changeEnvironmentState(
	ctx context.Context,
	server Server,
	scope []string,
	flag string,
	name string,
) error {
	if err := validateEnvironmentName(name); err != nil {
		return err
	}
	arguments := make([]string, 0, 4+len(scope))
	arguments = append(arguments, "set-environment")
	arguments = append(arguments, scope...)
	arguments = append(arguments, flag, "--", name)
	return runEnvironmentMutation(ctx, server, arguments)
}

func runEnvironmentMutation(ctx context.Context, server Server, arguments []string) error {
	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		return newRedactedCommandError(arguments[0], result)
	}
	return nil
}

func showEnvironment(
	ctx context.Context,
	server Server,
	scope []string,
) (map[string]EnvironmentValue, error) {
	arguments := make([]string, 0, 1+len(scope))
	arguments = append(arguments, "show-environment")
	arguments = append(arguments, scope...)
	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, newRedactedCommandError(arguments[0], result)
	}
	return parseEnvironmentLines(result.Stdout)
}

func getEnvironment(
	ctx context.Context,
	server Server,
	scope []string,
	name string,
) (EnvironmentValue, bool, error) {
	if err := validateEnvironmentName(name); err != nil {
		return EnvironmentValue{}, false, err
	}
	arguments := make([]string, 0, 2+len(scope))
	arguments = append(arguments, "show-environment")
	arguments = append(arguments, scope...)
	arguments = append(arguments, "--", name)
	result, err := server.literalCmd(ctx, arguments...)
	if err != nil {
		return EnvironmentValue{}, false, err
	}
	if result.ExitCode != 0 {
		if environmentVariableMissing(result, name) {
			return EnvironmentValue{}, false, nil
		}
		return EnvironmentValue{}, false, newRedactedCommandError(arguments[0], result)
	}
	values, err := parseEnvironmentLines(result.Stdout)
	if err != nil {
		return EnvironmentValue{}, false, err
	}
	if len(values) == 0 {
		return EnvironmentValue{}, false, nil
	}
	value, ok := values[name]
	if !ok || len(values) != 1 {
		return EnvironmentValue{}, false, &EnvironmentDecodeError{
			Record: 0,
			Reason: "entry does not match requested variable",
		}
	}
	return value, true, nil
}

func validateEnvironmentName(name string) error {
	if name == "" || strings.ContainsAny(name, "=\r\n\x00") {
		return &EnvironmentNameError{Name: name}
	}
	return nil
}

func validateEnvironmentValue(value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return &EnvironmentValueError{}
	}
	return nil
}

func parseEnvironmentLines(lines []string) (map[string]EnvironmentValue, error) {
	values := make(map[string]EnvironmentValue, len(lines))
	for record, line := range lines {
		name, value, hasValue := strings.Cut(line, "=")
		if hasValue {
			if validateEnvironmentName(name) != nil {
				return nil, &EnvironmentDecodeError{Record: record, Reason: "invalid variable name"}
			}
			values[name] = EnvironmentValue{Value: value}
			continue
		}
		name = strings.TrimPrefix(line, "-")
		if name == line || validateEnvironmentName(name) != nil {
			return nil, &EnvironmentDecodeError{
				Record: record,
				Reason: "expected NAME=value or -NAME",
			}
		}
		values[name] = EnvironmentValue{Removed: true}
	}
	return values, nil
}

func environmentVariableMissing(result CommandResult, name string) bool {
	return len(result.Stdout) == 0 &&
		len(result.Stderr) == 1 &&
		result.Stderr[0] == "unknown variable: "+name
}
