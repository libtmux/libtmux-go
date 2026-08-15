package tmux

import (
	"errors"
	"fmt"
)

// Target error sentinels classify malformed identities. [TargetError] matches
// ErrInvalidTarget through errors.Is.
var (
	// ErrMissingTarget identifies an operation on a zero-value model identity.
	ErrMissingTarget = errors.New("tmux: object target is required")
	// ErrInvalidTarget identifies a malformed stable tmux identifier. It is
	// matched by errors.Is for TargetError.
	ErrInvalidTarget = errors.New("tmux: invalid object target")
)

// TargetError reports a malformed stable identifier before command execution.
// It matches [ErrInvalidTarget] through errors.Is; callers can recover Object
// and Target with errors.As.
type TargetError struct {
	// Object names the tmux object kind whose target was validated.
	Object string
	// Target is the submitted target text.
	Target string
}

// Error implements error.
func (e *TargetError) Error() string {
	return fmt.Sprintf("%v: %s %q", ErrInvalidTarget, e.Object, e.Target)
}

// Unwrap makes TargetError compatible with ErrInvalidTarget.
func (e *TargetError) Unwrap() error { return ErrInvalidTarget }

func validateTypedTarget(subcommand, field, object, target string) error {
	if err := validateServerCommandArgument(subcommand, field, target, true); err != nil {
		return err
	}
	return validateStableTarget(object, target)
}

func validateStableTarget(object, target string) error {
	if target == "" {
		return ErrMissingTarget
	}
	var sigil byte
	switch object {
	case "session":
		sigil = '$'
	case "window":
		sigil = '@'
	case "pane":
		sigil = '%'
	case "client":
		return nil
	default:
		return &TargetError{Object: object, Target: target}
	}
	if len(target) < 2 || target[0] != sigil {
		return &TargetError{Object: object, Target: target}
	}
	for index := 1; index < len(target); index++ {
		if target[index] < '0' || target[index] > '9' {
			return &TargetError{Object: object, Target: target}
		}
	}
	return nil
}
