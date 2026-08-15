// Command benchmarks prints what each way of reaching tmux costs.
//
// A caller choosing between a tmux process, a control connection, and a plan is
// choosing cost. This builds one window every way and prints what each way
// spent, so the choice can be read rather than guessed:
//
//	go -C benchmarks run .
//
// The last column is the point. It is one SearchPanes query, asked of every
// mode, printed beside what that mode cost. It has to be identical everywhere,
// because a table comparing modes that answer differently is comparing nothing;
// TestMatrixAnswersAgree in this package is what holds it to that.
//
// The output is what BENCHMARKS.md holds, one table per supported tmux. CI
// runs this on each of them so the checked-in numbers can be compared against a
// version they were not recorded on.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	tmux "github.com/tmux-python/libtmux/golang"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchmarks:", err)
		os.Exit(1)
	}
}

// run measures every row and prints the table.
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	version, err := probeVersion(ctx)
	if err != nil {
		return err
	}
	rows, err := measureAll(ctx)
	if err != nil {
		return err
	}
	fmt.Print(table(rows, version, describeMachine()))
	return nil
}

// probeVersion asks a throwaway server which tmux is on PATH, so the table says
// what it was measured against rather than leaving the reader to guess.
func probeVersion(ctx context.Context) (tmux.Version, error) {
	h, err := newHarness(ctx)
	if err != nil {
		return tmux.Version{}, err
	}
	defer h.close()
	return h.server.Version(ctx)
}

// table renders the measured rows.
func table(rows []row, version tmux.Version, machine string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\nbuilding a %d-pane window, tmux %s\n", panesPerWindow, version)
	fmt.Fprintf(&out, "%s\n\n", machine)
	fmt.Fprintf(&out, "%-18s %10s %11s %8s  %s\n",
		"mode", "wall", "processes", "clients", "query answer")
	fmt.Fprintln(&out, strings.Repeat("-", 86))
	for _, r := range rows {
		fmt.Fprintf(&out, "%-18s %10s %11d %8d  %s\n",
			r.mode, r.elapsed.Round(time.Millisecond), r.processes, r.clients, r.answer)
	}
	return out.String()
}

// describeMachine names what the numbers were measured on, because wall clock
// means nothing without it.
func describeMachine() string {
	return fmt.Sprintf("%s, %d threads, %s, %s",
		processorName(), runtime.NumCPU(), runtime.GOOS, runtime.Version())
}

// processorName reads the processor model where the OS exposes one, and says so
// plainly where it does not rather than inventing a name.
func processorName() string {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "unnamed processor"
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, value, found := strings.Cut(scanner.Text(), ":")
		if found && strings.TrimSpace(name) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return "unnamed processor"
}
