// Command lifecycle demonstrates ownership on a disposable tmux endpoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Whole-server ownership uses a disposable endpoint.
	directory, err := os.MkdirTemp("", "ltg-owned-")
	if err != nil {
		return err
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(directory, "tmux.sock"), ConfigFile: "/dev/null",
	})
	if err != nil {
		return errors.Join(err, os.RemoveAll(directory))
	}
	root, err := server.FindOrCreate(ctx, tmux.NewSessionRequest{Name: "keeper"}, tmux.OwnershipOptions{})
	if err != nil {
		return fmt.Errorf("inspect endpoint %s after startup failure: %w", server.SocketPath(), err)
	}
	if !root.Created || root.Owner == nil {
		return fmt.Errorf("endpoint %s was borrowed; retained for inspection", server.SocketPath())
	}
	defer func() {
		if cleanupErr := root.Owner.Close(); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
			return
		}
		err = errors.Join(err, os.RemoveAll(directory))
	}()
	fmt.Println("created server:", root.Created)
	adoptedServer, err := root.Value.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		return err
	}
	defer adoptedServer.CloseInto(&err)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
	if err != nil {
		return err
	}
	adoptedSession, err := session.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		return err
	}
	defer adoptedSession.CloseInto(&err)
	reused, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{Name: "work"}, tmux.OwnershipOptions{})
	if err != nil {
		return err
	}
	fmt.Println("reused session is borrowed:", !reused.Created && reused.Owner == nil)
	window, err := session.FindOrCreateWindow(ctx, tmux.NewWindowRequest{Name: new("tools")}, tmux.OwnershipOptions{})
	if err != nil {
		return err
	}
	defer window.Owner.CloseInto(&err)
	adoptedWindow, err := window.Value.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		return err
	}
	defer adoptedWindow.CloseInto(&err)
	pane, err := window.Value.FindOrCreatePane(ctx,
		tmux.PaneIdentity{Key: "@example_role", Value: "worker"},
		tmux.SplitPaneRequest{Command: "cat"}, tmux.OwnershipOptions{})
	if err != nil {
		return err
	}
	defer pane.Owner.CloseInto(&err)
	adoptedPane, err := pane.Value.Adopt(ctx, tmux.OwnershipOptions{})
	if err != nil {
		return err
	}
	bodyErr := errors.New("example body failed")
	var bodyCleanupErr error
	body := func() (err error) {
		defer func() {
			bodyCleanupErr = adoptedPane.Close()
			err = errors.Join(err, bodyCleanupErr)
		}()
		return bodyErr
	}
	if err := body(); !errors.Is(err, bodyErr) || bodyCleanupErr != nil {
		return fmt.Errorf("body error lost: %w", err)
	}
	_, lookupErr := pane.Value.Refresh(ctx)
	fmt.Println("failed body cleaned pane:", errors.Is(lookupErr, tmux.ErrNotFound))
	found, err := server.Discover(ctx, tmux.DiscoveryOptions{Roots: []string{directory}})
	if err != nil {
		return err
	}
	fmt.Println("discovered servers:", len(found.Servers), "truncated:", found.Truncated)
	for _, diagnostic := range found.Diagnostics {
		if diagnostic.Err != nil {
			return fmt.Errorf("discovery %s: %w", diagnostic.Path, diagnostic.Err)
		}
	}
	return nil
}
