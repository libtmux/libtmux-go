package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

// waitPollInterval is how often the polled lane re-reads the screen. It is
// what tmuxtest polled at before waiting became event-driven, and a shorter
// one only trades more tmux processes for less of the delay it measures.
const waitPollInterval = 10 * time.Millisecond

// waitSettleDelay is how long the command waited for takes to arrive. It is
// long enough that neither lane answers by accident and short enough that ten
// rounds stay inside the matrix's budget.
const waitSettleDelay = 150 * time.Millisecond

// waitRounds is how many times each lane waits. One round is dominated by
// whatever the machine was doing at that moment.
const waitRounds = 10

// measureWaits reports what waiting for a pane to print something costs each
// way: re-reading the screen on an interval, or being woken by tmux. Each lane
// prepares once and then waits waitRounds times, which is how a caller uses
// it - a wait that opened its own stream every time would be measuring the
// opening.
func measureWaits(ctx context.Context) ([]row, error) {
	lanes := []struct {
		name    string
		prepare func(context.Context, tmux.Pane) (waiter, error)
	}{
		{name: "polled capture", prepare: pollingWaiter},
		{name: "event-driven", prepare: observingWaiter},
	}
	rows := make([]row, 0, len(lanes))
	for _, lane := range lanes {
		measured, err := measureWaitLane(ctx, lane.prepare)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", lane.name, err)
		}
		measured.mode = lane.name
		rows = append(rows, measured)
	}
	return rows, nil
}

// waiter waits for one command's output, and releases whatever it holds.
type waiter struct {
	wait  func(ctx context.Context, command string) error
	close func()
}

// measureWaitLane runs one lane's wait waitRounds times in a pane of its own.
func measureWaitLane(
	ctx context.Context,
	prepare func(context.Context, tmux.Pane) (waiter, error),
) (row, error) {
	h, err := newHarness(ctx)
	if err != nil {
		return row{}, err
	}
	defer h.close()
	pane, err := h.session.ResolveActivePane(ctx)
	if err != nil {
		return row{}, fmt.Errorf("resolve pane: %w", err)
	}
	// A plain shell, so the pane's prompt is not whatever the machine's login
	// shell prints, and the typed line is the only thing that arrives.
	respawned, err := pane.Respawn(ctx, tmux.RespawnRequest{
		Command: new("sh"), Kill: true,
	})
	if err != nil {
		return row{}, fmt.Errorf("start shell: %w", err)
	}
	if err := waitForShellReading(ctx, respawned); err != nil {
		return row{}, err
	}
	lane, err := prepare(ctx, respawned)
	if err != nil {
		return row{}, err
	}
	defer lane.close()

	if err := h.processes.reset(); err != nil {
		return row{}, err
	}
	clients, err := h.server.Clients(ctx)
	if err != nil {
		return row{}, fmt.Errorf("list clients: %w", err)
	}
	started := time.Now()
	for round := range waitRounds {
		marker := fmt.Sprintf("round-%d-done", round)
		command := fmt.Sprintf("sleep %.3f; printf '%s\\n'", waitSettleDelay.Seconds(), marker)
		if err := respawned.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
			return row{}, fmt.Errorf("send command: %w", err)
		}
		if err := lane.wait(ctx, command); err != nil {
			return row{}, err
		}
	}
	elapsed := time.Since(started)
	processes, err := h.processes.total()
	if err != nil {
		return row{}, err
	}
	return row{
		elapsed:   elapsed / waitRounds,
		processes: processes / waitRounds,
		clients:   len(clients),
		answer:    fmt.Sprintf("%d waits for output %s away", waitRounds, waitSettleDelay),
	}, nil
}

// waitForShellReading sends a marker until the pane prints it back. A shell
// that has not started reading drops what it is sent, and a lane that timed
// its first round against that would be measuring the shell's start.
func waitForShellReading(ctx context.Context, pane tmux.Pane) error {
	const marker = "bench-ready"
	command := "printf '" + marker + `\n'`
	deadline := time.Now().Add(20 * time.Second)
	for {
		if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
			return fmt.Errorf("send readiness marker: %w", err)
		}
		for range 20 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
			lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
			if err != nil {
				return fmt.Errorf("read pane: %w", err)
			}
			if slices.Contains(lines, marker) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pane %s never ran a command", pane.ID())
		}
	}
}

// pollingWaiter re-reads the whole screen on an interval, which is one tmux
// process per read.
func pollingWaiter(_ context.Context, pane tmux.Pane) (waiter, error) {
	return waiter{
		wait: func(ctx context.Context, command string) error {
			marker, err := markerOf(command)
			if err != nil {
				return err
			}
			return tmux.Poll(ctx, waitPollInterval, func(ctx context.Context) (bool, error) {
				lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
				if err != nil {
					return false, err
				}
				// Whole lines: the echo of the command carries the marker too.
				return slices.Contains(lines, marker), nil
			})
		},
		close: func() {},
	}, nil
}

// observingWaiter opens the pane's output stream once and is woken by tmux
// each time the pane writes.
func observingWaiter(ctx context.Context, pane tmux.Pane) (waiter, error) {
	observation, err := pane.OpenObservation(ctx)
	if err != nil {
		return waiter{}, fmt.Errorf("observe pane: %w", err)
	}
	return waiter{
		wait: func(ctx context.Context, command string) error {
			marker, err := markerOf(command)
			if err != nil {
				return err
			}
			_, err = observation.WaitFor(ctx, func(text string) bool {
				return slices.Contains(strings.Split(text, "\n"), marker)
			})
			return err
		},
		close: func() { _ = observation.Close() },
	}, nil
}

// markerOf reads back the line the command prints.
func markerOf(command string) (string, error) {
	_, quoted, found := strings.Cut(command, "printf '")
	marker, _, terminated := strings.Cut(quoted, `\n'`)
	if !found || !terminated {
		return "", fmt.Errorf("command prints no marker: %q", command)
	}
	return marker, nil
}

// waitTable renders what each way of waiting cost, per wait.
func waitTable(rows []row) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\nwaiting for a pane to print, per wait\n\n")
	fmt.Fprintf(&out, "%-20s %10s %11s %8s  %s\n",
		"path", "wall", "processes", "clients", "measurement")
	fmt.Fprintln(&out, strings.Repeat("-", 88))
	for _, r := range rows {
		fmt.Fprintf(&out, "%-20s %10s %11d %8d  %s\n",
			r.mode, r.elapsed.Round(time.Millisecond), r.processes, r.clients, r.answer)
	}
	return out.String()
}
