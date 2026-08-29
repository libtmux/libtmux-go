package tmux

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

type waitDelayRunner struct{}

func (waitDelayRunner) Run(
	context.Context,
	tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	return tmuxcmd.Result{ExitCode: -1}, exec.ErrWaitDelay
}

func TestTruncatedReadIsNotReportedAsAnEmptyServer(t *testing.T) {
	panes, err := serverWithRunner(waitDelayRunner{}).SearchPanes(
		context.Background(),
		nil,
	)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("SearchPanes() error = %v, want exec.ErrWaitDelay", err)
	}
	if panes != nil {
		t.Fatalf("SearchPanes() panes = %#v beside transport failure, want nil", panes)
	}
}
