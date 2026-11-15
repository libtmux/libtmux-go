package tmux_test

import (
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestPackageExportsCompile(t *testing.T) {
	t.Parallel()

	var server tmux.Server
	var session tmux.Session
	var window tmux.Window
	var pane tmux.Pane
	var client tmux.Client
	_ = []any{server, session, window, pane, client}
}
