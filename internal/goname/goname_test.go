package goname_test

import (
	"testing"

	"github.com/tmux-python/libtmux/golang/internal/goname"
)

func TestExportedAppliesTheGoNamingConvention(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name string
		want string
	}{
		{name: "pane_active", want: "PaneActive"},
		{name: "session", want: "Session"},

		{name: "pane_id", want: "PaneID"},
		{name: "get_by_id", want: "GetByID"},
		{name: "pane_pid", want: "PanePID"},
		{name: "pane_tty", want: "PaneTTY"},
		{name: "client_uid", want: "ClientUID"},
		{name: "client_utf8", want: "ClientUTF8"},
		{name: "pane_bg", want: "PaneBG"},
		{name: "pane_fg", want: "PaneFG"},
		{name: "copy_cursor_word", want: "CopyCursorWord"},

		{name: "client_readonly", want: "ClientReadOnly"},
		{name: "client_termname", want: "ClientTermName"},
		{name: "client_termfeatures", want: "ClientTermFeatures"},
		{name: "client_termtype", want: "ClientTermType"},

		{name: "callback_url", want: "CallbackURL"},
		{name: "api_token", want: "APIToken"},
		{name: "http_proxy", want: "HTTPProxy"},

		{name: "", want: ""},
		{name: "_", want: ""},
		{name: "_leading", want: "Leading"},
		{name: "trailing_", want: "Trailing"},
		{name: "double__underscore", want: "DoubleUnderscore"},
		{name: "Already_Cased", want: "AlreadyCased"},
	} {
		if got := goname.Exported(testCase.name); got != testCase.want {
			t.Errorf("Exported(%q) = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// TestExportedRejectsMixedCaseInitialisms pins the reason this package exists:
// a name crossing from tmux or Python must not reach Go as Id or Utf8, which
// Go programmers read as a style error.
func TestExportedRejectsMixedCaseInitialisms(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"pane_id", "get_by_id", "client_utf8", "pane_tty"} {
		got := goname.Exported(name)
		for _, rejected := range []string{"Id", "Utf8", "Tty"} {
			if len(got) >= len(rejected) && got[len(got)-len(rejected):] == rejected {
				t.Errorf("Exported(%q) = %q, which ends in the mixed-case %q", name, got, rejected)
			}
		}
	}
}
