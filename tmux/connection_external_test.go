package tmux_test

import (
	"context"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

type openSessionControlSignature func(
	tmux.Session,
	context.Context,
	tmux.ConnectionOptions,
) (*tmux.Connection, error)

type newSessionConnectionSignature func(
	tmux.Server,
	context.Context,
	tmux.NewSessionRequest,
	tmux.ConnectionOptions,
) (tmux.Session, *tmux.Connection, error)

type connectionServerSignature func(*tmux.Connection) tmux.Server

type connectionSessionSignature func(*tmux.Connection) tmux.Session

type connectionLanesSignature func(*tmux.Connection) int

type closeConnectionContextSignature func(*tmux.Connection, context.Context) error

type closeConnectionSignature func(*tmux.Connection) error

func TestConnectionPublicSurfaceCompiles(_ *testing.T) {
	var _ openSessionControlSignature = tmux.Session.OpenControl
	var _ newSessionConnectionSignature = tmux.Server.NewSessionConnection
	var _ connectionServerSignature = (*tmux.Connection).Server
	var _ connectionSessionSignature = (*tmux.Connection).Session
	var _ connectionLanesSignature = (*tmux.Connection).Lanes
	var _ closeConnectionContextSignature = (*tmux.Connection).CloseContext
	var _ closeConnectionSignature = (*tmux.Connection).Close
	_ = tmux.ErrConnectionRequiresProcess
}
