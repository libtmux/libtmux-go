package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libtmux/libtmux-go/tmux"
)

func ExampleOwned() {
	run := func() (err error) {
		directory, err := os.MkdirTemp("", "ltg-owner-example-")
		if err != nil {
			return err
		}
		server, err := tmux.NewServer(tmux.ServerOptions{
			SocketPath: filepath.Join(directory, "tmux.sock"), ConfigFile: "/dev/null",
		})
		if err != nil {
			return errors.Join(err, os.RemoveAll(directory))
		}
		ctx := context.Background()
		root, err := server.FindOrCreate(ctx, tmux.NewSessionRequest{Name: "keeper"}, tmux.OwnershipOptions{})
		if err != nil {
			return err
		}
		defer func() {
			if cleanupErr := root.Owner.Close(); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
				return
			}
			err = errors.Join(err, os.RemoveAll(directory))
		}()
		created, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{Name: "work"}, tmux.OwnershipOptions{})
		if err != nil {
			return err
		}
		defer created.Owner.CloseInto(&err)
		reused, err := server.FindOrCreateSession(ctx, tmux.NewSessionRequest{Name: "work"}, tmux.OwnershipOptions{})
		if err != nil {
			return err
		}
		fmt.Println("created:", created.Created, "reused owner absent:", reused.Owner == nil)
		adopted, err := reused.Value.Adopt(ctx, tmux.OwnershipOptions{})
		if err != nil {
			return err
		}
		if err := adopted.Close(); err != nil {
			return err
		}
		if err := adopted.Close(); err != nil {
			return err
		}
		_, lookupErr := reused.Value.Refresh(ctx)
		fmt.Println("adopted session removed:", errors.Is(lookupErr, tmux.ErrNotFound))
		discovered, err := server.Discover(ctx, tmux.DiscoveryOptions{Roots: []string{directory}})
		if err != nil {
			return err
		}
		fmt.Println("servers:", len(discovered.Servers), "truncated:", discovered.Truncated)
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// created: true reused owner absent: true
	// adopted session removed: true
	// servers: 1 truncated: false
}
