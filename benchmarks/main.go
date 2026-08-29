// Command benchmarks compares subprocess, control-mode, and planned tmux work.
// Build lanes share one SearchPanes workload; snapshot lanes compare ordinary
// and instance-bound reads. TestMatrixAnswersAgree guards each group.
//
//	go -C benchmarks run .
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchmarks:", err)
		os.Exit(1)
	}
}

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

// probeVersion uses an isolated server to record the tmux binary on PATH.
func probeVersion(ctx context.Context) (tmux.Version, error) {
	h, err := newHarness(ctx)
	if err != nil {
		return tmux.Version{}, err
	}
	defer h.close()
	return h.server.Version(ctx)
}

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

// describeMachine supplies context for wall-clock measurements.
func describeMachine() string {
	return fmt.Sprintf("%s, %d threads, %s, %s",
		processorName(), runtime.NumCPU(), runtime.GOOS, runtime.Version())
}

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
