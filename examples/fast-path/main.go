// Command fast-path compares subprocess and control-mode execution by counting
// the tmux processes each path starts.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := start(); err != nil {
		log.Fatal(err)
	}
}

// start owns cleanup because log.Fatal skips deferred calls in main.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	return run(ctx, tmux.ServerOptions{SocketName: "libtmux-go-example-fast-path"})
}

// counter wraps the default runner and counts tmux processes.
type counter struct {
	mutex   sync.Mutex
	started int
}

func (c *counter) runner() tmux.CommandRunner {
	return tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		c.mutex.Lock()
		c.started++
		c.mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})
}

func (c *counter) reset() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.started = 0
}

func (c *counter) total() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.started
}

// run accepts options so tests can isolate the server before installing the counter.
func run(ctx context.Context, options tmux.ServerOptions) error {
	processes := &counter{}
	options.Runner = processes.runner()
	server := tmux.NewServer(options)
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	}()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	processes.reset()
	if err := readTenTimes(ctx, session); err != nil {
		return err
	}
	fmt.Printf("over tmux processes: %d started\n", processes.total())

	// Keep the connected record; the original still starts subprocesses.
	// docs:control-pool
	_, connected, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{})
	if err != nil {
		return fmt.Errorf("open control pool: %w", err)
	}
	defer func() { _ = pool.Close() }()
	// docs:end

	processes.reset()
	if err := readTenTimes(ctx, connected); err != nil {
		return err
	}
	fmt.Printf("over a connection:   %d started\n", processes.total())

	// Arbitrary pane output can end a control frame, so exact capture stages
	// through a buffer and file instead.
	pane, ok, err := connected.ResolveActivePane(ctx)
	if err != nil || !ok {
		return fmt.Errorf("resolve pane: %w", err)
	}
	directory, err := os.MkdirTemp("", "fast-path")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()

	processes.reset()
	if _, err := pane.Capture(ctx, tmux.CapturePaneRequest{}); err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	printed := processes.total()

	processes.reset()
	if _, err := pane.CaptureToFile(
		ctx, filepath.Join(directory, "pane.txt"), tmux.CapturePaneRequest{},
	); err != nil {
		return fmt.Errorf("capture to file: %w", err)
	}
	fmt.Printf("printed capture:     %d started\n", printed)
	fmt.Printf("capture to a file:   %d started\n", processes.total())
	return nil
}

func readTenTimes(ctx context.Context, session tmux.Session) error {
	for range 10 {
		if _, err := session.SearchWindows(ctx, nil); err != nil {
			return fmt.Errorf("search windows: %w", err)
		}
	}
	return nil
}
