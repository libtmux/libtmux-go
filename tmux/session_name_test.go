package tmux

import (
	"errors"
	"testing"
)

// libtmux:parity libtmux.common.session_check_name
// libtmux:parity libtmux.common.session_check_name#parameter-branch:session_name:6c28c15e0739
// libtmux:parity libtmux.common.session_check_name#parameter-branch:session_name:e9c28dc69cef
// libtmux:parity libtmux.common.session_check_name#parameter-branch:session_name:fa76a5a47a6b
// libtmux:parity libtmux.exc.BadSessionName
// libtmux:parity libtmux.exc.BadSessionName.__init__
// libtmux:parity libtmux.exc.BadSessionName.__init__#parameter-branch:session_name:ab485de610f3
func TestValidateSessionNameMatchesTmuxDelimiterRules(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"work", "with spaces", "-literal", "semi;"} {
		if err := ValidateSessionName(name); err != nil {
			t.Fatalf("ValidateSessionName(%q) error = %v", name, err)
		}
	}
	for _, name := range []string{"", "bad.name", "bad:name", "bad.name:both"} {
		if err := ValidateSessionName(name); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("ValidateSessionName(%q) error = %v, want ErrInvalidRequest", name, err)
		}
	}
}

// tmux before 3.7 stores control bytes in names visibility-encoded; tmux 3.7
// rejects them. Validation rejects them across the supported range.
func TestValidateSessionNameRefusesWhatOlderTmuxWouldMangle(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"work", "with spaces", "日本語", "café", "-literal"} {
		if err := ValidateSessionName(name); err != nil {
			t.Errorf("ValidateSessionName(%q) error = %v, want a usable name", name, err)
		}
	}
	for name, why := range map[string]string{
		"a\abell":     "bell",
		"a\tb":        "tab",
		"a\nb":        "newline",
		"a\x7fb":      "delete",
		"a\x1b[0mb":   "escape",
		"bad\xffutf8": "malformed utf-8",
	} {
		if err := ValidateSessionName(name); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: ValidateSessionName(%q) error = %v, want ErrInvalidRequest",
				why, name, err)
		}
	}
}
