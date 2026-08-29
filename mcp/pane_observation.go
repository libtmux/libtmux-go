package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/libtmux/libtmux-go/tmux"
)

var errPaneObservationLost = errors.New(
	"pane output could not be preserved from the observation boundary",
)

type paneNotificationSource interface {
	NextNotification(context.Context) (tmux.ControlNotification, error)
}

func paneObservationError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", errPaneObservationLost, err)
}
