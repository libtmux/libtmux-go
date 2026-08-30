package mcp

import (
	"context"

	"github.com/libtmux/libtmux-go/tmux"
)

type mcpDependencies struct {
	probeSessions        func(context.Context, tmux.Server) ([]tmux.Session, error)
	newSessionConnection func(context.Context, tmux.Server, tmux.NewSessionRequest) (tmux.Session, *tmux.Connection, error)
	waitForChannel       func(context.Context, tmux.Server, tmux.WaitForRequest) error
	newWindow            func(context.Context, tmux.Session, tmux.NewWindowRequest) (tmux.Window, error)
	refreshWindow        func(context.Context, tmux.Server, tmux.WindowID) (tmux.Window, error)
	probeSibling         func(context.Context, tmux.Server) (bool, int)
}

func defaultMCPDependencies() mcpDependencies {
	return mcpDependencies{
		probeSessions: func(ctx context.Context, server tmux.Server) ([]tmux.Session, error) {
			alive, err := server.IsAlive(ctx)
			if err != nil {
				return nil, err
			}
			if !alive {
				return nil, tmux.ErrNoServer
			}
			return server.Sessions(ctx)
		},
		newSessionConnection: func(
			ctx context.Context,
			server tmux.Server,
			request tmux.NewSessionRequest,
		) (tmux.Session, *tmux.Connection, error) {
			return server.NewSessionConnection(ctx, request, tmux.ConnectionOptions{})
		},
		waitForChannel: func(
			ctx context.Context,
			server tmux.Server,
			request tmux.WaitForRequest,
		) error {
			return server.WaitFor(ctx, request)
		},
		newWindow: func(
			ctx context.Context,
			session tmux.Session,
			request tmux.NewWindowRequest,
		) (tmux.Window, error) {
			return session.NewWindow(ctx, request)
		},
		refreshWindow: func(
			ctx context.Context,
			server tmux.Server,
			id tmux.WindowID,
		) (tmux.Window, error) {
			return server.Window(ctx, id)
		},
		probeSibling: probeSiblingServer,
	}
}

func probeSiblingServer(ctx context.Context, server tmux.Server) (bool, int) {
	alive, err := server.IsAlive(ctx)
	if err != nil || !alive {
		return false, 0
	}
	sessions, err := server.Sessions(ctx)
	if err != nil {
		return true, 0
	}
	return true, len(sessions)
}
