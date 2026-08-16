package tmux

import "context"

// Sessions materializes the server's session records. A tmux command or
// transport failure is returned rather than answered with no rows, so an
// empty result means the server held nothing; [ErrNoServer] classifies a
// server that was not reached. Canceling ctx stops this read-only snapshot
// wait; errors.Is can detect context.Canceled or context.DeadlineExceeded
// as applicable.
func (s Server) Sessions(ctx context.Context) ([]Session, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Sessions(), nil
}

// Windows materializes one window record per winlink. A tmux command or
// transport failure is returned rather than answered with no rows, so an
// empty result means the server held nothing; [ErrNoServer] classifies a
// server that was not reached. Canceling ctx stops this read-only snapshot
// wait; errors.Is can detect context.Canceled or context.DeadlineExceeded
// as applicable.
func (s Server) Windows(ctx context.Context) ([]Window, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Windows(), nil
}

// Panes materializes pane records for every winlink. A tmux command or
// transport failure is returned rather than answered with no rows, so an
// empty result means the server held nothing; [ErrNoServer] classifies a
// server that was not reached. Canceling ctx stops this read-only snapshot
// wait; errors.Is can detect context.Canceled or context.DeadlineExceeded
// as applicable.
func (s Server) Panes(ctx context.Context) ([]Pane, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Panes(), nil
}

// Clients materializes attached tmux client records. A tmux command or
// transport failure is returned rather than answered with no rows, so an
// empty result means the server held nothing; [ErrNoServer] classifies a
// server that was not reached. Canceling ctx stops this read-only snapshot
// wait; errors.Is can detect context.Canceled or context.DeadlineExceeded
// as applicable.
func (s Server) Clients(ctx context.Context) ([]Client, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Clients(), nil
}
