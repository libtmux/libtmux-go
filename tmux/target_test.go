package tmux

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// A relation accessor such as Session.ActiveWindow or Session.Windows
// returns ok false rather than a Window carrying no id, and NewSession's
// result carries no relations at all - so a caller who skips the ok check
// reaches a zero-value target on the very next call. The error for that
// mistake must name which kind of object was empty - session, window, or
// pane - not just "tmux: object target is required".
func TestMissingTargetErrorNamesTheObjectKind(t *testing.T) {
	t.Parallel()

	for _, object := range []string{"session", "window", "pane", "client"} {
		err := validateStableTarget(object, "")
		if !errors.Is(err, ErrMissingTarget) {
			t.Fatalf("validateStableTarget(%q, \"\") error = %v, want ErrMissingTarget", object, err)
		}
		if !strings.Contains(err.Error(), object) {
			t.Fatalf("validateStableTarget(%q, \"\") error = %q, want it to name %q", object, err, object)
		}
		var missing *MissingTargetError
		if !errors.As(err, &missing) || missing.Object != object {
			t.Fatalf("validateStableTarget(%q, \"\") = %#v, want *MissingTargetError{Object: %q}", object, err, object)
		}
	}
}

// The hint a zero value's error gives names resolvers that exist, so a
// renamed method cannot leave the message pointing at nothing.
func TestMissingTargetErrorNamesRealResolvers(t *testing.T) {
	t.Parallel()

	types := map[string]reflect.Type{
		"Session": reflect.TypeFor[Session](),
		"Window":  reflect.TypeFor[Window](),
		"Pane":    reflect.TypeFor[Pane](),
	}
	for object, resolvers := range missingTargetResolvers {
		message := (&MissingTargetError{Object: object}).Error()
		if !strings.Contains(message, resolvers) {
			t.Fatalf("MissingTargetError{%q}.Error() = %q, want it to name %s", object, message, resolvers)
		}
		for resolver := range strings.SplitSeq(resolvers, " or ") {
			receiver, method, _ := strings.Cut(resolver, ".")
			if _, ok := types[receiver].MethodByName(method); !ok {
				t.Fatalf("%s hint names %s, which does not exist", object, resolver)
			}
		}
	}
}

// GO2-7: a zero-value Window - the shape a relation accessor's discarded ok
// or Server.NewSession's created-value-carries-no-relations leaves behind -
// reports itself as a missing window, not a missing session. Checking
// sessionID before windowID named the wrong kind and pointed a caller at
// Window.ResolveSession, which also needs the windowID this handle lacks and
// so reproduces the identical error.
func TestMissingTargetErrorForAZeroValueWindowNamesWindow(t *testing.T) {
	t.Parallel()

	_, err := validateWindowView(Window{})
	var missing *MissingTargetError
	if !errors.As(err, &missing) || missing.Object != "window" {
		t.Fatalf("validateWindowView(Window{}) = %#v, want *MissingTargetError{Object: \"window\"}", err)
	}
	if !strings.Contains(err.Error(), "Session.ResolveActiveWindow") {
		t.Fatalf("validateWindowView(Window{}) error = %q, want it to name Session.ResolveActiveWindow", err)
	}
}

// A present but malformed target keeps its existing, already-informative
// TargetError; only the empty case changes.
func TestInvalidTargetStillNamesObjectAndValue(t *testing.T) {
	t.Parallel()

	err := validateStableTarget("window", "not-a-window")
	if !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("validateStableTarget() error = %v, want ErrInvalidTarget", err)
	}
	var target *TargetError
	if !errors.As(err, &target) || target.Object != "window" || target.Target != "not-a-window" {
		t.Fatalf("validateStableTarget() = %#v, want TargetError{window, not-a-window}", err)
	}
}
