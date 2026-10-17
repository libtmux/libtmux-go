package tmux

import (
	"context"
	"errors"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

// pinControlSocket prevents public-path replacement from redirecting control
// initialization before tmux can evaluate a command-level guard.
func (s Server) pinControlSocket(ctx context.Context) (Server, func() error, error) {
	if s.daemon == nil {
		return Server{}, nil, errors.New("tmux: control acquisition needs a live daemon")
	}
	if !s.daemon.version.meetsMinimum(controlMinimumVersion) {
		return Server{}, nil, &VersionTooLowError{
			Current: s.daemon.version, Minimum: controlMinimumVersion,
		}
	}
	alias, cleanup, err := tmuxcmd.PinSocket(s.SocketPath())
	if err != nil {
		return Server{}, nil, err
	}
	fail := func(err error) (Server, func() error, error) {
		return Server{}, nil, errors.Join(err, cleanup())
	}
	launch, err := s.WithSocketPath(alias)
	if err != nil {
		return fail(err)
	}
	identity, err := launch.probeSnapshotIdentity(ctx)
	if err != nil {
		return fail(err)
	}
	if !sameSnapshotIdentity(*s.daemon, identity) {
		return fail(ErrDaemonReplaced)
	}
	return launch, cleanup, nil
}
