package tmux

import (
	"errors"
	"fmt"
	"strings"
)

// Option error sentinels classify [OptionError] through errors.Is.
var (
	// ErrOption identifies a failed high-level option or hook operation. It is
	// matched by errors.Is for OptionError.
	ErrOption = errors.New("tmux: option operation failed")
	// ErrUnknownOption identifies tmux's case-sensitive unknown-option diagnostic.
	ErrUnknownOption = fmt.Errorf("%w: unknown option", ErrOption)
	// ErrInvalidOption identifies tmux's case-sensitive invalid-option diagnostic.
	ErrInvalidOption = fmt.Errorf("%w: invalid option", ErrOption)
	// ErrInvalidOptionValue identifies a typed option value outside its active
	// tmux-version domain.
	ErrInvalidOptionValue = fmt.Errorf("%w: invalid option value", ErrOption)
	// ErrAmbiguousOption identifies tmux's case-sensitive ambiguous-option diagnostic.
	ErrAmbiguousOption = fmt.Errorf("%w: ambiguous option", ErrOption)
	// ErrOptionTarget identifies an option or hook operation whose target did
	// not resolve, which is the failure an unknown name is most easily mistaken
	// for. Classifying it discloses nothing that redaction protects: which kind
	// of failure occurred is not one of the values or hook commands an option
	// error withholds.
	ErrOptionTarget = fmt.Errorf("%w: target not found", ErrOption)
)

// OptionValueError reports a rejected typed option value without retaining or
// rendering that value. It matches [ErrInvalidOptionValue] and [ErrOption].
type OptionValueError struct {
	// Name is the safe, generated tmux option name.
	Name string
}

// Error implements error without disclosing the attempted value.
func (e *OptionValueError) Error() string {
	return fmt.Sprintf("%v: %s", ErrInvalidOptionValue, e.Name)
}

// Unwrap makes OptionValueError compatible with ErrInvalidOptionValue.
func (e *OptionValueError) Unwrap() error { return ErrInvalidOptionValue }

// OptionError reports a completed high-level option or hook failure. It matches
// [ErrOption] and a specific option sentinel through errors.Is; callers can
// recover its fields with errors.As. Library-created errors retain only
// Result.ExitCode. Error never renders command output because option values and
// hook commands may be secret; callers may construct exported values with other
// contents.
type OptionError struct {
	// Subcommand is the failed tmux option or hook subcommand.
	Subcommand string
	// Name is the option or hook name, when available.
	Name string
	// Result contains the library-created error's exit code and no diagnostics.
	Result CommandResult
	kind   error
	// unreachable is tmux's own message when its client failed before reaching
	// a server. It is private so that only this package can set it: OptionError
	// is exported, a caller may build one with any contents, and Error must
	// never render output it did not itself decide was disclosable.
	unreachable string
}

// Error implements error.
func (e *OptionError) Error() string {
	kind := e.kind
	if kind == nil {
		kind = ErrOption
	}
	operation := e.Subcommand
	if e.Name != "" {
		operation += " " + e.Name
	}
	// Only a pre-connection diagnosis this package recorded is rendered. Result
	// is never read here, because a caller may have built it.
	if e.unreachable != "" {
		return fmt.Sprintf("%v: %s exited %d: %s",
			kind, operation, e.Result.ExitCode, e.unreachable)
	}
	// A negative code means no tmux command ran, so reporting an exit status
	// invites a reader to look for one tmux never produced.
	if e.Result.ExitCode < 0 {
		return fmt.Sprintf("%v: %s was refused before tmux ran it", kind, operation)
	}
	return fmt.Sprintf("%v: %s exited %d", kind, operation, e.Result.ExitCode)
}

// Unwrap makes OptionError compatible with ErrOption and its specific kinds.
func (e *OptionError) Unwrap() error {
	if e.kind == nil {
		return ErrOption
	}
	return e.kind
}

func newOptionError(subcommand, name string, result CommandResult) *OptionError {
	kind := classifyOptionError(result.Stderr)
	// A client that never reached a server disclosed nothing, so its diagnosis
	// is safe to keep and is the only way a caller learns nothing answered.
	unreachable := ""
	if commandServerUnreachable(result.Stderr) {
		unreachable = strings.Join(result.Stderr, "\n")
	}
	return &OptionError{
		Subcommand:  subcommand,
		Name:        name,
		Result:      CommandResult{ExitCode: result.ExitCode},
		kind:        kind,
		unreachable: unreachable,
	}
}

func classifyOptionError(stderr []string) error {
	if len(stderr) == 0 {
		return ErrOption
	}
	if optionTargetNotFound(stderr) {
		return ErrOptionTarget
	}
	first := stderr[0]
	switch {
	case strings.Contains(first, "unknown option"):
		return ErrUnknownOption
	case strings.Contains(first, "invalid option"):
		return ErrInvalidOption
	case strings.Contains(first, "ambiguous option"):
		return ErrAmbiguousOption
	default:
		return ErrOption
	}
}

// optionTargetNotFound recognizes option-specific target errors. Option
// commands use "no such <object>" instead of the usual "can't find <object>".
func optionTargetNotFound(stderr []string) bool {
	for _, line := range stderr {
		for _, object := range [...]string{"session", "window", "pane"} {
			if strings.Contains(line, "no such "+object+": ") {
				return true
			}
		}
	}
	return false
}

func newLocalInvalidOptionError(subcommand, name string) *OptionError {
	return &OptionError{
		Subcommand: subcommand,
		Name:       name,
		Result:     CommandResult{ExitCode: -1},
		kind:       ErrInvalidOption,
	}
}
