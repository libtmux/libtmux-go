package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// countingRunner delegates to the default runner while counting tmux processes.
type countingRunner struct {
	mu    sync.Mutex
	count int
}

func (c *countingRunner) runner() tmux.CommandRunner {
	inner := tmux.SubprocessRunner()
	return tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		c.mu.Lock()
		c.count++
		c.mu.Unlock()
		return inner.Run(ctx, request)
	})
}

func (c *countingRunner) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count = 0
}

func (c *countingRunner) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// countingEngine omits [tmux.InstanceBoundEngine] so boundEngine can isolate
// the effect of that property.
type countingEngine struct {
	inner tmux.Engine
	mu    sync.Mutex
	count int
}

func (e *countingEngine) Supports(kind tmux.CommandKind) bool { return e.inner.Supports(kind) }

func (e *countingEngine) Run(
	ctx context.Context,
	kind tmux.CommandKind,
	request tmux.CommandRequest,
) (tmux.CommandResult, error) {
	e.mu.Lock()
	e.count++
	e.mu.Unlock()
	return e.inner.Run(ctx, kind, request)
}

func (e *countingEngine) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count = 0
}

func (e *countingEngine) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

type boundEngine struct{ *countingEngine }

func (boundEngine) InstanceBound() bool { return true }

type harness struct {
	server    tmux.Server
	session   tmux.Session
	processes *countingRunner
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

	processes := &countingRunner{}
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath:         filepath.Join(directory, "s.sock"),
		ConfigFile:         config,
		Runner:             processes.runner(),
		ProcessEnvironment: cleanEnvironment(),
	})

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
		connected, _, pool, err := h.server.OpenControlPool(
			ctx, h.session, tmux.ControlPoolRequest{Connections: connections},
		)
		if err != nil {
			return row{}, fmt.Errorf("%s: open control pool: %w", mode, err)
		}
		defer func() { _ = pool.Close() }()
		handle = connected
		refreshed, err := connected.Window(ctx, window.ID())
		if err != nil {
			return row{}, fmt.Errorf("%s: resolve window: %w", mode, err)
		}
		window = refreshed
	}

	h.processes.reset()
	start := time.Now()
	if err := run(ctx, handle, window); err != nil {
		return row{}, fmt.Errorf("%s: build: %w", mode, err)
	}
	elapsed := time.Since(start)
	processes := h.processes.total()

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

// searchAnswer lets the matrix verify that every lane measured equivalent work.
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

// measureSnapshot compares ordinary and instance-bound snapshot reads. It counts
// commands because both paths share one connection and start no processes.
func measureSnapshot(ctx context.Context, bound bool) (row, error) {
	mode := "snapshot"
	if bound {
		mode = "snapshot, bound"
	}
	h, err := newHarness(ctx)
	if err != nil {
		return row{}, err
	}
	defer h.close()

	if _, err := h.session.NewWindow(ctx, tmux.NewWindowRequest{}); err != nil {
		return row{}, fmt.Errorf("%s: create window: %w", mode, err)
	}
	client, err := h.server.OpenControl(ctx, h.session)
	if err != nil {
		return row{}, fmt.Errorf("%s: open control: %w", mode, err)
	}
	defer func() { _ = client.Close() }()

	counter := &countingEngine{inner: client.Engine()}
	var engine tmux.Engine = counter
	if bound {
		engine = boundEngine{countingEngine: counter}
	}
	handle := h.server.WithEngine(engine)

	// Warm the version cache so the measurement isolates snapshot reads.
	if _, err := handle.Snapshot(ctx); err != nil {
		return row{}, fmt.Errorf("%s: warm snapshot: %w", mode, err)
	}
	counter.reset()
	start := time.Now()
	snapshot, err := handle.Snapshot(ctx)
	if err != nil {
		return row{}, fmt.Errorf("%s: snapshot: %w", mode, err)
	}
	elapsed := time.Since(start)
	commands := counter.total()

	clients, err := handle.Clients(ctx)
	if err != nil {
		return row{}, fmt.Errorf("%s: list clients: %w", mode, err)
	}
	panes := snapshot.Panes()
	indexes := make([]string, 0, len(panes))
	for _, pane := range panes {
		index, _ := pane.Formats().Raw("pane_index")
		indexes = append(indexes, index)
	}
	return row{
		mode:      mode,
		elapsed:   elapsed,
		processes: commands,
		clients:   len(clients),
		answer:    fmt.Sprintf("%d panes on the server %v", len(panes), indexes),
	}, nil
}

// measureAll runs rows in display order. Negative connections selects
// subprocesses; zero uses the pool default.
func measureAll(ctx context.Context) ([]row, error) {
	rows := make([]row, 0, 7)
	for _, measurement := range []struct {
		mode        string
		connections int
		run         build
	}{
		{"process", -1, buildDirect},
		{"control mode", 0, buildDirect},
		{"concurrent x4", 4, buildDirect},
		{"chained", -1, buildPlanned},
		{"chained + control", 0, buildPlanned},
	} {
		measured, err := measureBuild(
			ctx, measurement.mode, measurement.connections, measurement.run,
		)
		if err != nil {
			return nil, err
		}
		rows = append(rows, measured)
	}
	for _, bound := range []bool{false, true} {
		measured, err := measureSnapshot(ctx, bound)
		if err != nil {
			return nil, err
		}
		rows = append(rows, measured)
	}
	return rows, nil
}
