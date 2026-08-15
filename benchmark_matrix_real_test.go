package tmux_test

// The switch matrix: what each way of reaching tmux costs, and what it answers.
//
// A caller choosing between a tmux process, a control connection, and a plan is
// choosing cost. This runs one workload every way and prints what each one
// spent, so the choice can be read rather than guessed. It also runs the same
// query through each and compares the answers, because the only thing that
// makes the table worth trusting is that nothing but the cost changed.
//
// Run it with:
//
//	go test -run TestSwitchMatrix -v .
//
// It reuses measuredServer and countingRunner from engine_real_test.go, which
// count tmux processes at ServerOptions.Runner -- the seam process execution
// actually passes through -- rather than estimating them from the code.
//
// One difference between the rows is not cost, and the table says so rather
// than letting the numbers imply otherwise. The direct build materializes a
// record after each mutation, because that is what its methods return; the
// planned build does not, because a plan reports IDs and statuses instead. Part
// of the gap between those rows is therefore work the planned build did not do.
// A caller who needs a record per step is choosing the direct API knowingly.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// boundEngine forwards InstanceBound from the engine it wraps, which
// countingEngine deliberately does not. The pair is the A/B below: the same
// control connection, counted the same way, differing only in whether the
// snapshot is told that staying connected already proves what its identity
// probes were asking.
type boundEngine struct{ *countingEngine }

func (e boundEngine) InstanceBound() bool { return true }

// measureSnapshot is the read half of the matrix.
//
// Server.Snapshot proves that everything it read describes one tmux server by
// reading the server's identity on each side of the listing. A transport that
// implements InstanceBoundEngine has already proved it by staying connected --
// tmux ends a client with the server it attached to -- so the snapshot skips
// the closing read. The opening one stays, because it reports the version the
// listing formats are chosen from. Counting tmux commands rather than processes
// is what isolates that saving: both rows here run over one control connection
// and start no processes at all, so a process count would show nothing.
func measureSnapshot(ctx context.Context, t *testing.T, bound bool) switchMeasurement {
	t.Helper()
	mode := "snapshot"
	if bound {
		mode = "snapshot, bound"
	}
	harness := tmuxtest.NewServer(ctx, t)

	sessions, err := harness.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("%s: Sessions() = (%d, %v)", mode, len(sessions), err)
	}
	if _, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{}); err != nil {
		t.Fatalf("%s: NewWindow() error = %v", mode, err)
	}
	client, err := harness.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("%s: OpenControl() error = %v", mode, err)
	}
	t.Cleanup(func() { _ = client.Close() })

	counter := &countingEngine{inner: client.Engine()}
	var engine tmux.Engine = counter
	if bound {
		engine = boundEngine{countingEngine: counter}
	}
	handle := harness.WithEngine(engine)

	// A first snapshot warms the version cache, so the measured one counts the
	// reads a snapshot makes rather than the probe behind it.
	if _, err := handle.Snapshot(ctx); err != nil {
		t.Fatalf("%s: Snapshot() error = %v", mode, err)
	}
	counter.reset()
	start := time.Now()
	snapshot, err := handle.Snapshot(ctx)
	if err != nil {
		t.Fatalf("%s: Snapshot() error = %v", mode, err)
	}
	elapsed := time.Since(start)
	commands := counter.total()

	clients, err := handle.Clients(ctx)
	if err != nil {
		t.Fatalf("%s: Clients() error = %v", mode, err)
	}
	return switchMeasurement{
		mode:      mode,
		elapsed:   elapsed,
		processes: commands,
		clients:   len(clients),
		answer: fmt.Sprintf("%d panes on the server %v",
			len(snapshot.Panes()), snapshotPaneIndexes(snapshot)),
	}
}

// snapshotPaneIndexes renders a snapshot's panes the way the build rows render
// a search, so the answer column compares across both halves of the table.
func snapshotPaneIndexes(snapshot tmux.Snapshot) []string {
	panes := snapshot.Panes()
	indexes := make([]string, 0, len(panes))
	for _, pane := range panes {
		index, _ := pane.Formats().Raw("pane_index")
		indexes = append(indexes, index)
	}
	return indexes
}

// switchMeasurement is one row of the matrix.
type switchMeasurement struct {
	mode      string
	elapsed   time.Duration
	processes int
	clients   int
	answer    string
}

// panesPerSwitchWindow is the workload size. It is small enough to stay quick
// under the version matrix and large enough that a per-command process cost
// shows up against the noise.
const panesPerSwitchWindow = 6

//libtmux:real-tmux
func TestSwitchMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// The workload, written both ways. Every row below runs one of these, so
	// the rows compare like with like.
	//
	// The direct build ignores the handle because a record carries the one that
	// produced it, and the caller below hands it a window looked up through
	// whichever handle the row is measuring. The planned build takes the handle
	// because a plan is not a record and has none.
	buildDirect := func(_ tmux.Server, window tmux.Window) error {
		for range panesPerSwitchWindow - 1 {
			if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
				return err
			}
			if err := window.SelectLayout(
				ctx, tmux.SelectLayoutRequest{Layout: "tiled"},
			); err != nil {
				return err
			}
		}
		_, err := window.Rename(ctx, "built")
		return err
	}
	buildPlanned := func(handle tmux.Server, window tmux.Window) error {
		plan := tmux.NewPlan()
		for range panesPerSwitchWindow - 1 {
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

	measure := func(
		mode string,
		control bool,
		build func(tmux.Server, tmux.Window) error,
	) switchMeasurement {
		t.Helper()
		harness := tmuxtest.NewServer(ctx, t)
		server, runner := measuredServer(t, harness)

		sessions, err := server.Sessions(ctx)
		if err != nil || len(sessions) == 0 {
			t.Fatalf("%s: Sessions() = (%d, %v)", mode, len(sessions), err)
		}
		window, err := sessions[0].NewWindow(ctx, tmux.NewWindowRequest{})
		if err != nil {
			t.Fatalf("%s: NewWindow() error = %v", mode, err)
		}

		handle := server
		if control {
			connections := 0
			if strings.HasPrefix(mode, "concurrent") {
				connections = 4
			}
			connected, _, pool, err := server.OpenControlPool(
				ctx, sessions[0], tmux.ControlPoolRequest{Connections: connections},
			)
			if err != nil {
				t.Fatalf("%s: OpenControlPool() error = %v", mode, err)
			}
			defer func() { _ = pool.Close() }()
			handle = connected
			refreshed, err := connected.Window(ctx, window.ID())
			if err != nil {
				t.Fatalf("%s: Window() error = %v", mode, err)
			}
			window = refreshed
		}

		// Setup is done; only the build below is measured.
		runner.reset()
		start := time.Now()
		if err := build(handle, window); err != nil {
			t.Fatalf("%s: build error = %v", mode, err)
		}
		elapsed := time.Since(start)
		processes := runner.total()

		// The same query, every mode. This is the column that matters: if it
		// ever differs, the rows above are not measuring the same thing.
		panes, err := handle.SearchPanes(ctx, nil)
		if err != nil {
			t.Fatalf("%s: SearchPanes() error = %v", mode, err)
		}
		indexes := make([]string, 0, len(panes))
		for _, pane := range panes {
			index, _ := pane.Formats().Raw("pane_index")
			indexes = append(indexes, index)
		}

		clients, err := handle.Clients(ctx)
		if err != nil {
			t.Fatalf("%s: Clients() error = %v", mode, err)
		}

		return switchMeasurement{
			mode:      mode,
			elapsed:   elapsed,
			processes: processes,
			clients:   len(clients),
			answer:    fmt.Sprintf("%d panes on the server %v", len(panes), indexes),
		}
	}

	rows := []switchMeasurement{
		measure("process", false, buildDirect),
		measure("control mode", true, buildDirect),
		measure("concurrent x4", true, buildDirect),
		measure("chained", false, buildPlanned),
		measure("chained + control", true, buildPlanned),
	}
	rows = append(rows, measureSnapshot(ctx, t, false), measureSnapshot(ctx, t, true))

	version, err := tmuxtest.NewServer(ctx, t).Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}

	var table strings.Builder
	fmt.Fprintf(&table,
		"\nbuilding a %d-pane window, tmux %s\n\n", panesPerSwitchWindow, version)
	fmt.Fprintf(&table, "%-18s %10s %11s %8s  %s\n",
		"mode", "wall", "processes", "clients", "query answer")
	fmt.Fprintln(&table, strings.Repeat("-", 86))
	for _, row := range rows {
		fmt.Fprintf(&table, "%-18s %10s %11d %8d  %s\n",
			row.mode, row.elapsed.Round(time.Millisecond),
			row.processes, row.clients, row.answer)
	}
	fmt.Fprint(&table, `
The query answer is identical in every row; only the cost differs. Wall clock
moves between machines and tmux versions -- the invocation count is the stable
number. The direct rows materialize a record after each mutation and the
planned rows do not, so part of their difference is work, not just batching.
A control connection is an attached tmux client for as long as it is open,
which is the entry in the clients column.

The snapshot rows count tmux commands rather than processes, because both run
over one connection and start none. They differ only in whether the transport
reports InstanceBoundEngine: staying connected already proves what a snapshot's
closing identity read asks, so a bound one skips it. The opening read stays,
because the listing formats are chosen from the version it reports. An engine
that wraps another and forgets to forward that pays for the closing read again,
which is the upper row.
`)
	t.Log(table.String())

	// The claim the whole table rests on. Cost may differ; the answer may not.
	// The snapshot rows read a server built differently and are compared with
	// each other rather than with the build rows.
	for _, row := range rows[1:5] {
		if row.answer != rows[0].answer {
			t.Errorf("%s answered %q, process answered %q",
				row.mode, row.answer, rows[0].answer)
		}
	}
	if rows[6].answer != rows[5].answer {
		t.Errorf("a bound snapshot answered %q, an unbound one %q",
			rows[6].answer, rows[5].answer)
	}

	// What each row is supposed to demonstrate, asserted rather than left to
	// the reader to infer from numbers that move between machines.
	byMode := map[string]switchMeasurement{}
	for _, row := range rows {
		byMode[row.mode] = row
	}
	if got := byMode["control mode"].processes; got != 0 {
		t.Errorf("control mode started %d tmux processes, want 0", got)
	}
	if byMode["control mode"].clients <= byMode["process"].clients {
		t.Errorf("a control connection did not appear as an extra client: %d vs %d",
			byMode["control mode"].clients, byMode["process"].clients)
	}
	if byMode["chained"].processes >= byMode["process"].processes {
		t.Errorf("chaining did not reduce tmux invocations: %d vs %d",
			byMode["chained"].processes, byMode["process"].processes)
	}
	// A snapshot over a bound transport skips the identity probe on each side
	// of its listing, because staying connected already proves what they were
	// asking. That is two fewer reads, and it is why the row is here.
	if got, want := byMode["snapshot"].processes-byMode["snapshot, bound"].processes, 1; got != want {
		t.Errorf("InstanceBoundEngine saved %d tmux commands, want exactly %d (%d vs %d)",
			got, want, byMode["snapshot"].processes, byMode["snapshot, bound"].processes)
	}
}
