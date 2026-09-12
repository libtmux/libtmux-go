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

	server, err := tmux.NewServer(tmux.ServerOptions{})
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

	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil {
		return fmt.Errorf("find pane: %w", err)
	}
	if !ok {
		return errors.New("session reported no pane")
	}

	// A payload with the characters a shell would fight over, from anything
	// that reads: a file, a socket, an HTTP body, or this strings.Reader.
	payload := strings.NewReader("$HOME 'quoted' \"double\" `backtick` \\ done\n")

	// docs:byte-streams
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

	written, err := file.Stat()
	if err != nil {
		return fmt.Errorf("measure archive: %w", err)
	}
	fmt.Printf("pasted %d bytes, compressed the screen into %d\n",
		payload.Size(), written.Size())
	fmt.Println("archive:", archive)
	return nil
}
