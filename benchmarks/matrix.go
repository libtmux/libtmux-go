package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tmux "github.com/libtmux/libtmux-go"
)

// panesPerWindow is the workload size. It is small enough to stay quick under
// the version matrix and large enough that a per-command process cost shows up
// against the noise.
const panesPerWindow = 6

// row is one line of the table: a mode, what it spent, and what it answered.
type row struct {
	mode      string
	elapsed   time.Duration
	processes int
	clients   int
	answer    string
}

// countingRunner counts the tmux processes a server starts, by wrapping the
// runner it would have used anyway.
//
// Wrapping rather than replacing is what the package documentation asks for:
// the result shape is what the rest of the package reads, and reimplementing
// execution to count it would be measuring something else.
type countingRunner struct {
	mu    sync.Mutex
	count int
}

// runner returns a [tmux.CommandRunner] that counts and then delegates.
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

// reset starts a new measurement.
func (c *countingRunner) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count = 0
}

// total reports how many tmux processes have been started since the last reset.
func (c *countingRunner) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// countingEngine counts the tmux commands an engine carried. It deliberately
// does not forward [tmux.InstanceBoundEngine]; boundEngine is the half that
// does, and the pair is what isolates what that property saves.
type countingEngine struct {
	inner tmux.Engine
	mu    sync.Mutex
	count int
}

// Supports reports what the wrapped engine supports.
func (e *countingEngine) Supports(kind tmux.CommandKind) bool { return e.inner.Supports(kind) }

// Run counts one command and passes it to the wrapped engine.
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

// reset starts a new measurement.
func (e *countingEngine) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count = 0
}

// total reports how many commands have been carried since the last reset.
func (e *countingEngine) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

// boundEngine is countingEngine plus the property it withholds.
type boundEngine struct{ *countingEngine }

// InstanceBound reports that consecutive commands reached one tmux server,
// which staying connected is what proves.
func (boundEngine) InstanceBound() bool { return true }

// harness is one throwaway tmux server and the counter watching it.
type harness struct {
	server    tmux.Server
	session   tmux.Session
	processes *countingRunner
	directory string
}

// newHarness starts a tmux server of this benchmark's own and creates the
// session the workload runs in.
//
// The socket path and the configuration file are both this program's, so the
// numbers do not move with whatever the person running it has configured, and
// the inherited tmux variables are dropped so running this from inside a pane
// does not reach the server hosting it.
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
	}).WithStrictErrors()

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

// close ends the tmux server and removes what it left behind.
func (h *harness) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.server.Kill(ctx)
	_ = os.RemoveAll(h.directory)
}

// cleanEnvironment is this process's environment without the three variables
// that would point tmux at a server this program does not own.
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

// buildDirect creates the window's panes one command at a time, which is what
// the object API does: every call returns the record it changed.
func buildDirect(ctx context.Context, _ tmux.Server, window tmux.Window) error {
	for range panesPerWindow - 1 {
		if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
			return err
		}
		// The layout is reapplied between splits because a pane halved five
		// times has no room left to split; it is part of the workload rather
		// than decoration.
		if err := window.SelectLayout(
			ctx, tmux.SelectLayoutRequest{Layout: "tiled"},
		); err != nil {
			return err
		}
	}
	_, err := window.Rename(ctx, "built")
	return err
}

// buildPlanned records the same window and sends it, which is the same API
// shape taking the same requests.
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

// build is one way of creating the window every row creates.
type build func(context.Context, tmux.Server, tmux.Window) error

// measureBuild runs one build row: it times the work, counts what it started,
// and then asks the same question every other row is asked.
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

	// Setup is done; only the build below is measured.
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

// searchAnswer is the one query every row is asked. It is the column that
// matters: if it ever differs between rows, they are not measuring the same
// thing.
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

// measureSnapshot is the read half of the table.
//
// A snapshot proves everything it read describes one tmux server by reading the
// server's identity on each side of its listing. A transport that reports
// [tmux.InstanceBoundEngine] has already proved it by staying connected, so the
// closing read is dropped. The opening one stays, because the listing formats
// are chosen from the version it reports.
//
// These rows count tmux commands rather than processes, because both run over
// one connection and start none: a process count would read zero for both and
// show nothing.
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

	// A first snapshot warms the version cache, so the measured one counts the
	// reads a snapshot makes rather than the probe behind it.
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

// measureAll runs every row of the table, in the order the table prints them.
//
// A connections value below zero means the row runs over tmux processes; zero
// or more opens a control pool of that size, where zero is the pool's own
// default of one connection.
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
