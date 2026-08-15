package tmuxtest_test

import (
	"context"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestPTYProcessPublicSurfaceCompiles(_ *testing.T) {
	start := tmuxtest.StartPTYProcess
	requirePTYCloseContextSignature((*tmuxtest.PTYProcess).CloseContext)
	requirePTYWriteSignature((*tmuxtest.PTYProcess).Write)

	_ = start
	_ = (*tmuxtest.PTYProcess).Close
	_ = (*tmuxtest.PTYProcess).Done
	_ = (*tmuxtest.PTYProcess).Output
	_ = (*tmuxtest.PTYProcess).Wait
	_ = (*tmuxtest.PTYProcess).Write
}

func requirePTYWriteSignature(
	_ func(*tmuxtest.PTYProcess, context.Context, []byte) (int, error),
) {
}

func requirePTYCloseContextSignature(
	_ func(*tmuxtest.PTYProcess, context.Context) error,
) {
}
