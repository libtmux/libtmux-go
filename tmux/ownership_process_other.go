//go:build !unix

package tmux

import (
	"context"
	"errors"
)

func waitOwnedProcess(context.Context, int) error {
	return errors.New("tmux: observing owned daemon termination requires Unix")
}
