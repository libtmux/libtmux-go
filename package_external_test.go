package tmux_test

import (
	"testing"

	"github.com/tmux-python/libtmux/golang"
)

// libtmux:parity libtmux#export:Client
// libtmux:parity libtmux#export:Pane
// libtmux:parity libtmux#export:Server
// libtmux:parity libtmux#export:Session
// libtmux:parity libtmux#export:Window
// libtmux:parity libtmux.__all__
func TestPackageExportsCompile(t *testing.T) {
	t.Parallel()

	var server tmux.Server
	var session tmux.Session
	var window tmux.Window
	var pane tmux.Pane
	var client tmux.Client
	_ = []any{server, session, window, pane, client}
}
