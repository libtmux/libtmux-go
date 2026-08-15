// Command fast-path demonstrates driving tmux without starting a tmux process
// per command, and reading a pane without starting one at all.
//
// It counts the processes it starts so the difference is visible rather than
// asserted: the same work is done twice, once over tmux processes and once
// over a control-mode connection.
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

// start owns the context and the server, so that main does nothing but
// report a failure. log.Fatal exits without running deferred calls, and the
// cancel below has to run.
func start() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	return run(ctx, tmux.ServerOptions{SocketName: "libtmux-go-example-fast-path"})
}

// counter records how many tmux processes a server started, by wrapping the
// runner a server uses when ServerOptions.Runner is nil. Wrapping rather than
// replacing keeps the result shape the rest of the package reads.
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

// run holds the example itself. It takes the options rather than a built server
// because the counting runner has to be installed before the server exists,
// which lets main run it on a socket of this example's own and the test beside
// it run the same code on a socket path the test owns.
func run(ctx context.Context, options tmux.ServerOptions) error {
	processes := &counter{}
	options.Runner = processes.runner()
	server := tmux.NewServer(options).WithStrictErrors()
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	}()

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Ten reads the ordinary way: each one starts a tmux process.
	processes.reset()
	if err := readTenTimes(ctx, session); err != nil {
		return err
	}
	fmt.Printf("over tmux processes: %d started\n", processes.total())

	// The same ten reads over a control-mode connection. The pool returns the
	// session on the connected handle; the one passed in still starts a
	// process per command, so the returned value is the one to keep.
	_, connected, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{})
	if err != nil {
		return fmt.Errorf("open control pool: %w", err)
	}
	defer func() { _ = pool.Close() }()

	processes.reset()
	if err := readTenTimes(ctx, connected); err != nil {
		return err
	}
	fmt.Printf("over a connection:   %d started\n", processes.total())

	// A printed capture starts a process whatever handle it runs on, because
	// tmux does not escape a command's output and pane content could end the
	// connection's frame. Staging through a buffer and a file avoids that.
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
