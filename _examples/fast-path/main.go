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

	tmux "github.com/libtmux/libtmux-go"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
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

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	processes := &counter{}
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-fast-path",
		Runner:     processes.runner(),
	}).WithStrictErrors()
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
