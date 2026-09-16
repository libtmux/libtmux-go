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

// MissingTargetError reports an operation attempted on a zero-value model
// identity. It matches [ErrMissingTarget] through errors.Is; callers can
// recover Object with errors.As.
//
// A relation accessor such as [Session.ActiveWindow] or [Session.Windows]
// returns ok false rather than a value with no id, and a creation call such
// as [Server.NewSession] carries no relations at all - so using a discarded
// or freshly created value's zero-value fields reaches this error on the
// first operation. Resolve live state with a Resolve* method or a fresh
// [Server.Snapshot] instead.
type MissingTargetError struct {
	// Object names the tmux object kind with no id: "session", "window",
	// "pane", or "client".
	Object string
}

// Error implements error.
func (e *MissingTargetError) Error() string {
	message := fmt.Sprintf("%v: %s has no id", ErrMissingTarget, e.Object)
	if resolvers := missingTargetResolvers[e.Object]; resolvers != "" {
		message += "; a relation accessor whose ok was false returns a zero " +
			e.Object + ", and a created value carries no relations: use " + resolvers
	}
	return message
}

// missingTargetResolvers names the live lookups that return each object kind,
// the fix for a zero value reached through a discarded relation result.
var missingTargetResolvers = map[string]string{
	"session": "Window.ResolveSession or Pane.ResolveSession",
	"window":  "Session.ResolveActiveWindow or Pane.ResolveWindow",
	"pane":    "Session.ResolveActivePane or Window.ResolveActivePane",
}

// Unwrap makes MissingTargetError compatible with ErrMissingTarget.
func (e *MissingTargetError) Unwrap() error { return ErrMissingTarget }

func validateTypedTarget(subcommand, field, object, target string) error {
	if err := validateServerCommandArgument(subcommand, field, target, true); err != nil {
		return err
	}
	return validateStableTarget(object, target)
}

func validateStableTarget(object, target string) error {
	if target == "" {
		return &MissingTargetError{Object: object}
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
