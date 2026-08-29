package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// panesPerWindow balances matrix runtime against visible per-command costs.
const panesPerWindow = 6

type row struct {
	mode      string
	elapsed   time.Duration
	processes int
	clients   int
	answer    string
}

type harness struct {
	server    tmux.Server
	session   tmux.Session
	processes *processCounter
	directory string
}

// newHarness isolates the socket, configuration, and inherited tmux variables.
func newHarness(ctx context.Context) (*harness, error) {
	root := os.Getenv("TMUX_TMPDIR")
	if root == "" {
		root = filepath.Join(os.TempDir(), "libtmux-go-bench")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create socket root: %w", err)
	}
	directory, err := os.MkdirTemp(root, "run")
	if err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	config := filepath.Join(directory, "tmux.conf")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		return nil, fmt.Errorf("write tmux configuration: %w", err)
	}

	processes, err := newProcessCounter(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         filepath.Join(directory, "s.sock"),
		ConfigFile:         config,
		Binary:             processes.proxy,
		ProcessEnvironment: processes.environment(cleanEnvironment()),
	})
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("construct tmux server: %w", err)
	}

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "bench"})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &harness{
		server:    server,
		session:   session,
		processes: processes,
		directory: directory,
	}, nil
}

func (h *harness) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.server.Kill(ctx)
	_ = os.RemoveAll(h.directory)
}

// cleanEnvironment removes variables that could select an external tmux server.
func cleanEnvironment() []string {
	environment := os.Environ()
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		switch strings.SplitN(entry, "=", 2)[0] {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// buildDirect creates panes one object-API call at a time.
func buildDirect(ctx context.Context, _ tmux.Server, window tmux.Window) error {
	for range panesPerWindow - 1 {
		if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
			return err
		}
		// Reapply the layout so repeated splits do not exhaust one pane's width.
		if err := window.SelectLayout(
			ctx, tmux.SelectLayoutRequest{Layout: "tiled"},
		); err != nil {
			return err
		}
	}
	_, err := window.Rename(ctx, "built")
	return err
}

// buildPlanned records and sends the same requests as buildDirect.
func buildPlanned(ctx context.Context, handle tmux.Server, window tmux.Window) error {
	plan := tmux.NewPlan()
	for range panesPerWindow - 1 {
		plan.SplitPane(window.Ref(), tmux.SplitPaneRequest{})
		plan.SelectLayout(window.Ref(), tmux.SelectLayoutRequest{Layout: "tiled"})
	}
	plan.RenameWindow(window.Ref(), "built")
	result, err := plan.Run(ctx, handle)
	if err != nil {
		return err
	}
	return result.Err()
}

type build func(context.Context, tmux.Server, tmux.Window) error

type measurement struct {
	mode        string
	connections int
	run         build
}

func measureBuild(
	ctx context.Context,
	mode string,
	connections int,
	run build,
) (row, error) {
	h, err := newHarness(ctx)
	if err != nil {
		return row{}, err
	}
	defer h.close()

	window, err := h.session.NewWindow(ctx, tmux.NewWindowRequest{})
	if err != nil {
		return row{}, fmt.Errorf("%s: create window: %w", mode, err)
	}

	handle := h.server
	if connections >= 0 {
		connection, err := h.session.OpenControl(
			ctx, tmux.ConnectionOptions{Lanes: connections},
		)
		if err != nil {
			return row{}, fmt.Errorf("%s: open control connection: %w", mode, err)
		}
		defer func() { _ = connection.Close() }()
		handle = connection.Server()
		refreshed, err := handle.Window(ctx, window.ID())
		if err != nil {
			return row{}, fmt.Errorf("%s: resolve window: %w", mode, err)
		}
		window = refreshed
	}

	if err := h.processes.reset(); err != nil {
		return row{}, fmt.Errorf("%s: %w", mode, err)
	}
	start := time.Now()
	if err := run(ctx, handle, window); err != nil {
		return row{}, fmt.Errorf("%s: build: %w", mode, err)
	}
	elapsed := time.Since(start)
	processes, err := h.processes.total()
	if err != nil {
		return row{}, fmt.Errorf("%s: %w", mode, err)
	}

	answer, err := searchAnswer(ctx, handle)
	if err != nil {
		return row{}, fmt.Errorf("%s: %w", mode, err)
	}
	clients, err := handle.Clients(ctx)
	if err != nil {
		return row{}, fmt.Errorf("%s: list clients: %w", mode, err)
	}
	return row{
		mode:      mode,
		elapsed:   elapsed,
		processes: processes,
		clients:   len(clients),
		answer:    answer,
	}, nil
}

// searchAnswer verifies equivalent results across the build lanes.
func searchAnswer(ctx context.Context, handle tmux.Server) (string, error) {
	panes, err := handle.SearchPanes(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("search panes: %w", err)
	}
	indexes := make([]string, 0, len(panes))
	for _, pane := range panes {
		index, _ := pane.Formats().Raw("pane_index")
		indexes = append(indexes, index)
	}
	return fmt.Sprintf("%d panes on the server %v", len(panes), indexes), nil
}

// measureAll runs compatible rows in display order. Negative connections
// selects subprocesses; zero opens one connection lane. Terminal connections
// require tmux 3.6, so older releases retain the process and planned rows.
func measureAll(ctx context.Context, version tmux.Version) ([]row, error) {
	measurements := []measurement{{"process", -1, buildDirect}}
	if supportsOwnedConnections(version) {
		measurements = append(measurements,
			measurement{"connection", 0, buildDirect},
			measurement{"concurrent x4", 4, buildDirect},
		)
	}
	measurements = append(measurements, measurement{"chained", -1, buildPlanned})
	if supportsOwnedConnections(version) {
		measurements = append(measurements, measurement{"chained + connection", 0, buildPlanned})
	}

	rows := make([]row, 0, len(measurements))
	for _, measurement := range measurements {
		measured, err := measureBuild(
			ctx, measurement.mode, measurement.connections, measurement.run,
		)
		if err != nil {
			return nil, err
		}
		rows = append(rows, measured)
	}
	return rows, nil
}

func supportsOwnedConnections(version tmux.Version) bool {
	return version.Major() > 3 || version.Major() == 3 && version.Minor() >= 6
}
