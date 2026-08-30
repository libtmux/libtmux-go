package tmux

import "context"

// Sessions materializes the server's sessions. Failures, including
// [ErrNoServer], remain errors rather than empty results.
func (s Server) Sessions(ctx context.Context) ([]Session, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Sessions(), nil
}

// Windows materializes one record per winlink. Failures, including
// [ErrNoServer], remain errors rather than empty results.
func (s Server) Windows(ctx context.Context) ([]Window, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Windows(), nil
}

// Panes materializes pane records for every winlink. Failures, including
// [ErrNoServer], remain errors rather than empty results.
func (s Server) Panes(ctx context.Context) ([]Pane, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Panes(), nil
}

// Clients materializes attached clients. Failures, including [ErrNoServer],
// remain errors rather than empty results.
func (s Server) Clients(ctx context.Context) ([]Client, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Clients(), nil
}
