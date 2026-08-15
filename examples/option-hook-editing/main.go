// Command option-hook-editing demonstrates typed option and hook updates.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{}).WithStrictErrors()
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "libtmux-options"})
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		err = errors.Join(err, session.Kill(cleanupCtx))
	}()

	if err := session.SetMouse(ctx, true); err != nil {
		return err
	}
	status, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 0, Value: "#[align=left]#{session_name}"},
		tmux.SparseEntry[string]{Index: 2, Value: "#[align=right]#{window_name}"},
	)
	if err != nil {
		return err
	}
	if _, err := session.SetStatusFormat(ctx, status); err != nil {
		return err
	}

	global := server.GlobalSessionScope()
	if err := global.SetStatus(ctx, tmux.StatusOn); err != nil {
		return err
	}
	if err := global.SetHook(ctx, "client-attached", "display-message 'client attached'"); err != nil {
		return err
	}
	hooks, err := global.Hooks(ctx)
	if err != nil {
		return err
	}
	_, present := hooks.ClientAttached().Get()
	fmt.Println("client-attached hook present:", present)
	return nil
}
