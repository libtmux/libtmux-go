package tmuxtest_test

import (
	"context"
	"testing"

	tmux "github.com/libtmux/libtmux-go"
	"github.com/libtmux/libtmux-go/tmuxtest"
)

// libtmux:parity libtmux._internal.control_mode.ControlMode
// libtmux:parity libtmux._internal.control_mode.ControlMode.__enter__
// libtmux:parity libtmux._internal.control_mode.ControlMode.__exit__
// libtmux:parity libtmux._internal.control_mode.ControlMode.__init__
// libtmux:parity libtmux.pytest_plugin.TestServer
// libtmux:parity libtmux.pytest_plugin.USING_ZSH
// libtmux:parity libtmux.pytest_plugin.control_mode
func TestServerOptionsPublicSurfaceCompiles(_ *testing.T) {
	var options tmuxtest.ServerOptions
	requireNewServerSignature(tmuxtest.NewServer)
	requireNewServerWithOptionsSignature(tmuxtest.NewServerWithOptions)
	requireNewControlModeSignature(tmuxtest.NewControlMode)
	requireControlCloseContextSignature((*tmuxtest.ControlMode).CloseContext)
	requireControlReadSignature((*tmuxtest.ControlMode).Read)

	_ = options
}

func requireControlReadSignature(
	_ func(*tmuxtest.ControlMode, context.Context, []byte) (int, error),
) {
}

func requireNewServerSignature(_ func(context.Context, testing.TB) tmux.Server) {}

func requireNewServerWithOptionsSignature(
	_ func(context.Context, testing.TB, tmuxtest.ServerOptions) tmux.Server,
) {
}

func requireNewControlModeSignature(
	_ func(context.Context, testing.TB, tmux.Server, tmux.Session) *tmuxtest.ControlMode,
) {
}

func requireControlCloseContextSignature(
	_ func(*tmuxtest.ControlMode, context.Context) error,
) {
}
