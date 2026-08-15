package tmux_test

import (
	"context"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

type openControlSignature func(tmux.Server, context.Context, tmux.Session) (*tmux.ControlClient, error)

type controlCommandSignature func(*tmux.ControlClient, context.Context, ...string) (tmux.ControlCommandResult, error)

type nextNotificationSignature func(*tmux.ControlClient, context.Context) (tmux.ControlNotification, error)

type reconnectControlSignature func(*tmux.ControlClient, context.Context) (*tmux.ControlClient, error)

type controlClientNameSignature func(*tmux.ControlClient) tmux.ClientName

type controlServerSignature func(*tmux.ControlClient) tmux.Server

type controlSessionSignature func(*tmux.ControlClient) tmux.Session

type controlContextErrorSignature func(*tmux.ControlClient, context.Context) error

type controlErrorSignature func(*tmux.ControlClient) error

func TestControlClientPublicSurfaceCompiles(_ *testing.T) {
	var client *tmux.ControlClient
	var result tmux.ControlCommandResult

	_ = result.Command
	_ = result.RawStdout
	_ = result.Timestamp
	_ = result.Number
	_ = result.Flags
	_ = result.Failed
	_ = tmux.ErrControlClosed
	_ = tmux.ErrControlProtocol
	var _ openControlSignature = tmux.Server.OpenControl
	var _ controlCommandSignature = (*tmux.ControlClient).Cmd
	var _ nextNotificationSignature = (*tmux.ControlClient).NextNotification
	var _ reconnectControlSignature = (*tmux.ControlClient).Reconnect
	var _ controlClientNameSignature = (*tmux.ControlClient).ClientName
	var _ controlServerSignature = (*tmux.ControlClient).Server
	var _ controlSessionSignature = (*tmux.ControlClient).Session
	var _ controlContextErrorSignature = (*tmux.ControlClient).Wait
	var _ controlContextErrorSignature = (*tmux.ControlClient).CloseContext
	var _ controlErrorSignature = (*tmux.ControlClient).Close

	_ = client
}
