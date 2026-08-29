package workspace_test

import (
	"context"
	"fmt"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/workspace"
)

// Load a tmuxp-style document and build the session it describes.
func Example() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-workspace",
	})
	if err != nil {
		fmt.Println("server:", err)
		return
	}
	defer killExampleServer(server)

	document := []byte(`
session_name: review
windows:
  - window_name: editor
    panes:
      - shell_command: printf 'ready\n'
  - window_name: tests
    panes:
      - shell_command: printf 'ready\n'
      - shell_command: printf 'ready\n'
`)
	parsed, err := workspace.Parse(document)
	if err != nil {
		fmt.Println("parse:", err)
		return
	}

	session, err := workspace.Build(ctx, server, parsed)
	if err != nil {
		fmt.Println("build:", err)
		return
	}

	name, _ := session.Name()
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		fmt.Println("search windows:", err)
		return
	}
	fmt.Println(name, len(windows))
	// Output: review 2
}

// Create the initial session and its transport together, then populate the
// rest of the workspace without reconnecting it.
func ExampleBuildInto() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-workspace-continue",
	})
	if err != nil {
		fmt.Println("server:", err)
		return
	}
	defer killExampleServer(server)

	described := workspace.Workspace{
		SessionName: "review",
		Windows: []workspace.Window{
			{Name: "editor", Panes: []workspace.Pane{{Shell: "sleep 60"}}},
			{Name: "tests", Panes: []workspace.Pane{{Shell: "sleep 60"}}},
		},
	}
	request, err := described.InitialSessionRequest()
	if err != nil {
		fmt.Println("request:", err)
		return
	}
	_, connection, err := server.NewSessionConnection(
		ctx,
		request,
		tmux.ConnectionOptions{},
	)
	if err != nil {
		fmt.Println("create:", err)
		return
	}
	defer func() { _ = connection.Close() }()

	session := connection.Session()
	if err := workspace.BuildInto(ctx, session, described); err != nil {
		fmt.Println("build:", err)
		return
	}
	windows, err := session.SearchWindows(ctx, nil)
	if err != nil {
		fmt.Println("search windows:", err)
		return
	}
	fmt.Println(len(windows))
	// Output: 2
}

// A misspelled key fails the parse rather than being dropped, so a workspace
// that does not do what its author meant says so before anything is built.
func ExampleParse_unknownField() {
	_, err := workspace.Parse([]byte("session_name: review\nwindow:\n  - {}\n"))
	fmt.Println(err != nil)
	// Output: true
}

// killExampleServer stops an example's server on a context of its own, since an
// example's own context may already be spent by the time it returns.
func killExampleServer(server tmux.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Kill(ctx)
}
