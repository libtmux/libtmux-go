// Command byte-streams moves bytes in and out of tmux through io.Reader and
// io.Writer: a payload no shell has to quote, and a scrollback no temporary
// file has to hold.
package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Choose an unused socket name: cleanup kills the server selected here.
	// The empty config keeps personal settings out of a new server.
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-byte-streams",
		ConfigFile: os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("configure tmux server: %w", err)
	}
	// One path per process, so two runs at once do not write each other's
	// archive.
	archive := filepath.Join(
		os.TempDir(), fmt.Sprintf("libtmux-byte-streams-%d.gz", os.Getpid()),
	)
	return run(ctx, server, archive)
}

// run accepts injected server state so tests can isolate the example.
func run(ctx context.Context, server tmux.Server, archive string) (err error) {
	// A plain POSIX shell, so the pane does not depend on what your login
	// shell does at startup.
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "libtmux-byte-streams", Command: "sh",
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	pane, err := session.ResolveActivePane(ctx)
	if err != nil {
		return fmt.Errorf("find pane: %w", err)
	}

	// A payload with the characters a shell would fight over, from anything
	// that reads: a file, a socket, an HTTP body, or this strings.Reader.
	const marker = "'quoted'"
	payload := strings.NewReader("$HOME 'quoted' \"double\" `backtick` \\ done\n")

	// Pasting hands the bytes to the pane's pty; the program reading it echoes
	// them back on its own schedule. Watch before pasting, so the wait cannot
	// start after the bytes it is waiting for already arrived.
	observation, err := pane.OpenObservation(ctx)
	if err != nil {
		return fmt.Errorf("observe pane: %w", err)
	}
	defer func() { err = errors.Join(err, observation.Close()) }()

	// docs:byte-streams-in given:ctx context.Context; server tmux.Server; pane tmux.Pane; payload *strings.Reader
	// tmux reads the buffer from the reader as a stream, so no quoting rule
	// applies to the payload and no command line has to hold it.
	name := "payload"
	if err := server.LoadBufferFrom(ctx, payload, tmux.LoadBufferFromOptions{
		Name: &name,
	}); err != nil {
		return fmt.Errorf("load payload: %w", err)
	}
	if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
		BufferName: &name, DeleteAfter: true,
	}); err != nil {
		return fmt.Errorf("paste payload: %w", err)
	}
	// docs:end

	// docs:byte-streams-out given:ctx context.Context; pane tmux.Pane; observation *tmux.PaneObservation; archive string; marker string
	// Capture only once the bytes are on the screen. A capture taken straight
	// after the paste archives whatever the pane happened to be showing, which
	// on a loaded machine is still an empty screen. The pane announces the
	// bytes as it echoes them, so nothing here guesses how long that takes.
	seen := strings.Builder{}
	for _, line := range observation.Baseline() {
		seen.WriteString(line)
	}
	for !strings.Contains(seen.String(), marker) {
		notification, err := observation.NextNotification(ctx)
		if err != nil {
			return fmt.Errorf("follow pane output: %w", err)
		}
		if _, output, ok := notification.Output(); ok {
			seen.Write(output)
		}
	}

	file, err := os.Create(archive)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	compressor := gzip.NewWriter(file)
	if err := pane.CaptureTo(ctx, compressor, tmux.CapturePaneRequest{
		Start: tmux.CaptureBoundary, End: tmux.CaptureBoundary,
	}); err != nil {
		return fmt.Errorf("capture scrollback: %w", err)
	}
	if err := compressor.Close(); err != nil {
		return fmt.Errorf("finish archive: %w", err)
	}
	// docs:end

	fmt.Printf("pasted %d bytes\n", payload.Size())
	fmt.Println("archived the pane's scrollback as gzip")
	// The path differs on every run, so it goes to standard error and what
	// the program prints stays the same.
	fmt.Fprintln(os.Stderr, "archive:", archive)
	return nil
}
