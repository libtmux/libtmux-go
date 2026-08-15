package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmuxq"
)

// killExampleServer stops an example's tmux server on a context of its own.
//
// An example's ctx is expired exactly when the run failed on its deadline,
// which is when cleanup matters most, and every example names a fixed socket.
// A server left running therefore fails every later run of that example with a
// session that already exists, long after the slow machine that caused it.
// exampleWaitBudget bounds an example that waits for a program running in a
// pane. It is generous because it is a ceiling rather than a delay: each wait
// below ends as soon as its condition holds. A budget tight enough to be
// exceeded on a busy machine turns a passing example into a failing one
// without anything being wrong with what it demonstrates.
const exampleWaitBudget = 60 * time.Second

func killExampleServer(server tmux.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Kill(ctx)
}

func Example() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "project", WindowName: "editor",
	})
	if err != nil {
		return
	}
	windowName := "tests"
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: &windowName, Attach: true,
	})
	if err != nil {
		return
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight,
	})
	if err != nil {
		return
	}
	if _, err := pane.Select(ctx, tmux.PaneSelectRequest{}); err != nil {
		return
	}
}

func ExamplePane_SendKeys() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"}).WithStrictErrors()

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		return
	}
	pane := panes[0]
	command := "printf 'build ready\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command,
		Literal: true,
	}); err != nil {
		return
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{
			Start: tmux.CaptureBoundary,
			End:   tmux.CaptureBoundary,
		})
		if err != nil {
			return
		}
		if slices.Contains(lines, "build ready") {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func ExamplePane_CaptureBytes() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"}).WithStrictErrors()

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		return
	}
	output, err := panes[0].CaptureBytes(ctx, tmux.CapturePaneRequest{
		Start: tmux.CaptureBoundary,
		End:   tmux.CaptureBoundary,
	})
	if err != nil {
		return
	}
	fmt.Printf("%x\n", output)
}

func ExampleServer_Sessions() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	sessions, err := server.WithStrictErrors().Sessions(ctx)
	if err != nil {
		return
	}
	for _, session := range sessions {
		name, _ := session.Name()
		fmt.Println(name)
		for _, window := range session.Windows() {
			for _, pane := range window.Panes() {
				fmt.Println(window.ID(), pane.ID())
			}
		}
	}
}

func ExampleServer_Cmd() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	result, err := server.Cmd(ctx, "display-message", "-p", "#{session_name}")
	if err != nil {
		return
	}
	if result.ExitCode != 0 {
		fmt.Println(result.Stderr)
		return
	}
	fmt.Println(result.Stdout)
	fmt.Printf("%q\n", result.RawStdout)
}

func ExampleServer_OpenControl() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"}).WithStrictErrors()

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return
	}
	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		return
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = client.CloseContext(closeCtx)
	}()

	result, err := client.Cmd(ctx, "display-message", "-p", "ready")
	if err != nil || result.Failed {
		return
	}
	reconnected, err := client.Reconnect(ctx)
	if err != nil {
		return
	}
	client = reconnected
}

func ExampleServer_ShowBufferBytes() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	name := "clipboard"
	output, err := server.ShowBufferBytes(ctx, &name)
	if err != nil {
		return
	}
	fmt.Printf("%x\n", output)
}

func ExampleServer_Snapshot() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	snapshot, err := server.WithStrictErrors().Snapshot(ctx)
	if err != nil {
		return
	}
	for _, session := range snapshot.Sessions() {
		for _, pane := range session.Panes() {
			command, _ := pane.CurrentCommand()
			fmt.Println(session.ID(), pane.ID(), command)
		}
	}
}

func ExampleSession_ResolveActiveWindow() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	session, err := server.Session(ctx, tmux.SessionID("$1"))
	if err != nil {
		return
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		return
	}
	fmt.Println(window.ID())
}

func ExamplePaneFilter_Predicate() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})
	panes, err := server.WithStrictErrors().Panes(ctx)
	if err != nil {
		return
	}
	minimumIndex := 0
	predicate, err := (tmux.PaneFilter{
		IDIn:    []tmux.PaneID{"%1", "%3"},
		IndexGT: &minimumIndex,
	}).Predicate()
	if err != nil {
		return
	}

	for _, pane := range tmuxq.Where(panes, predicate) {
		fmt.Println(pane.ID())
	}
}

func ExamplePtr() {
	value := tmux.Ptr(0)
	fmt.Println(*value)

	// Output:
	// 0
}

func ExamplePaneCommandIs() {
	filter := tmux.PaneCommandIs("nvim")
	fmt.Println(*filter.Command)

	// Output:
	// nvim
}

func ExampleSparseArray() {
	values, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 1, Value: "first"},
		tmux.SparseEntry[string]{Index: 4, Value: "fourth"},
	)
	if err != nil {
		return
	}

	value, present := values.Get(2)
	fmt.Println(values.Indices())
	fmt.Println(value, present)
	// Output:
	// [1 4]
	//  false
}

func ExampleServer_WithStrictErrors() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	_, err := server.WithStrictErrors().Sessions(ctx)
	if err == nil {
		return
	}
	var commandError *tmux.CommandError
	if errors.As(err, &commandError) {
		fmt.Println(commandError.Subcommand, commandError.Result.ExitCode)
	}
}

func ExampleSession_Options() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	sessions, err := server.WithStrictErrors().Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return
	}
	options, err := sessions[0].Options(ctx)
	if err != nil {
		return
	}
	mouse, present := options.Mouse().Get()
	origin, _ := options.Mouse().Origin()
	fmt.Println(mouse, present, origin)
}

func ExampleSession_SetMouse() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})

	sessions, err := server.WithStrictErrors().Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return
	}
	_ = sessions[0].SetMouse(ctx, true)
}

func ExampleSession_SetUpdateEnvironment() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})
	values, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 0, Value: "DISPLAY"},
		tmux.SparseEntry[string]{Index: 3, Value: "SSH_AUTH_SOCK"},
	)
	if err != nil {
		return
	}

	sessions, err := server.WithStrictErrors().Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		return
	}
	result, err := sessions[0].SetUpdateEnvironment(ctx, values)
	if err != nil {
		return
	}
	fmt.Println(result.Replaced, result.AppliedIndices)
}

func ExampleGlobalSessionScope_SetHook() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})
	global := server.GlobalSessionScope()

	if err := global.SetHook(ctx, "client-attached", "display-message 'client attached'"); err != nil {
		return
	}
	hooks, err := global.Hooks(ctx)
	if err != nil {
		return
	}
	_, present := hooks.ClientAttached().Get()
	fmt.Println(present)
}

func ExampleServer_SearchPanes() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{SocketName: "my-application"})
	filter := tmux.TmuxFilter("#{==:#{pane_current_command},nvim}")

	panes, err := server.WithStrictErrors().SearchPanes(ctx, &filter)
	if err != nil {
		return
	}
	for _, pane := range panes {
		fmt.Println(pane.ID())
	}
}

func ExampleNewServer() {
	// NewServer records configuration; it does not start tmux.
	server := tmux.NewServer(tmux.ServerOptions{SocketPath: "/tmp/libtmux-go-example.sock"})

	fmt.Println(server.SocketPath())
	// Output: /tmp/libtmux-go-example.sock
}

func ExampleServer_NewSession() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-new-session",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	name, ok := session.Name()
	fmt.Println(name, ok)
	// Output: build true
}

func ExampleSession_NewWindow() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-new-window",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	// Name is a pointer because a nonnil empty string is an explicit -n operand,
	// while nil lets tmux apply automatic-rename.
	windowName := "editor"
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{Name: &windowName})
	if err != nil {
		fmt.Println("create window:", err)
		return
	}

	name, ok := window.Name()
	fmt.Println(name, ok)
	// Output: editor true
}

func ExampleWindow_SplitPane() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-split-pane",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		fmt.Println("resolve window:", err)
		return
	}
	if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight,
	}); err != nil {
		fmt.Println("split pane:", err)
		return
	}

	// Window.Panes reads the record's own materialized state and never queries
	// tmux, so a record from a point lookup carries none. Ask tmux for the
	// window's current panes instead; a nil filter matches every pane.
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		fmt.Println("list panes:", err)
		return
	}
	fmt.Println(len(panes))
	// Output: 2
}

func ExamplePane_Capture() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-capture",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	// ResolveActivePane reports absence as ok=false rather than as an error,
	// because a session can exist with no active pane.
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}
	command := "printf 'build ready\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
		fmt.Println("send keys:", err)
		return
	}

	// tmux accepts the keys before the shell has run them, so poll the pane
	// until the output appears or ctx expires.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err != nil {
			fmt.Println("capture:", err)
			return
		}
		if slices.Contains(lines, "build ready") {
			fmt.Println("build ready")
			return
		}
		select {
		case <-ctx.Done():
			fmt.Println("timed out waiting for output")
			return
		case <-ticker.C:
		}
	}
	// Output: build ready
}

func ExampleServerOptions_Runner() {
	// Runner substitutes the transport, so code that drives tmux can be tested
	// without a tmux binary present.
	fake := tmux.CommandRunnerFunc(
		func(_ context.Context, request tmux.CommandRequest) (tmux.CommandResult, error) {
			return tmux.CommandResult{
				Command: request.Arguments,
				Stdout:  []string{"tmux 3.7b"},
			}, nil
		},
	)
	server := tmux.NewServer(tmux.ServerOptions{Runner: fake})

	version, err := server.Version(context.Background())
	fmt.Println(version, err)
	// Output: 3.7b <nil>
}

func ExampleServer_Session() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-session-lookup",
	}).WithStrictErrors()
	defer killExampleServer(server)

	created, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	// A point lookup asks tmux for one object by its stable identifier.
	session, err := server.Session(ctx, created.ID())
	if err != nil {
		fmt.Println("look up session:", err)
		return
	}
	name, _ := session.Name()
	fmt.Println(name)
	// Output: build
}

func ExampleServer_Window() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-window-lookup",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "build", WindowName: "editor",
	})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	created, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		fmt.Println("resolve window:", err)
		return
	}

	window, err := server.Window(ctx, created.ID())
	if err != nil {
		fmt.Println("look up window:", err)
		return
	}
	name, _ := window.Name()
	fmt.Println(name)
	// Output: editor
}

func ExampleServer_Pane() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-pane-lookup",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	created, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}

	pane, err := server.Pane(ctx, created.ID())
	if err != nil {
		fmt.Println("look up pane:", err)
		return
	}
	fmt.Println(pane.ID() == created.ID())
	// Output: true
}

func ExampleServer_Client() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-client-lookup",
	}).WithStrictErrors()
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// A detached server has no clients, so the lookup reports absence as a
	// classified error rather than an empty value.
	_, err := server.Client(ctx, tmux.ClientName("/dev/pts/999"))
	fmt.Println(errors.Is(err, tmux.ErrSnapshotNotFound))
	// Output: true
}

func ExampleWindow_SearchPanes() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-search-panes",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		fmt.Println("resolve window:", err)
		return
	}
	if _, err := window.SplitPane(ctx, tmux.SplitPaneRequest{}); err != nil {
		fmt.Println("split:", err)
		return
	}

	// SearchPanes asks tmux; a nil filter matches every pane in the window.
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		fmt.Println("search panes:", err)
		return
	}
	fmt.Println(len(panes))
	// Output: 2
}

func ExampleWindow_Panes() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-window-panes",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		fmt.Println("resolve window:", err)
		return
	}

	// Panes reads the record's own materialized state and never queries tmux, so
	// it is empty unless the record came from a snapshot. A targeted point
	// lookup does not carry relations; a resolver does.
	looked, err := server.Window(ctx, window.ID())
	if err != nil {
		fmt.Println("look up window:", err)
		return
	}
	fmt.Println("from a point lookup:", len(looked.Panes()))
	fmt.Println("from a resolver:", len(window.Panes()))

	// A snapshot materializes the whole hierarchy, so its records do carry them.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		fmt.Println("snapshot:", err)
		return
	}
	for _, materialized := range snapshot.Windows() {
		fmt.Println("from a snapshot:", len(materialized.Panes()))
	}
	// Output:
	// from a point lookup: 0
	// from a resolver: 1
	// from a snapshot: 1
}

func ExampleServer_SetOption() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-set-option",
	}).WithStrictErrors()
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// SetOption takes any tmux option name, including one outside the generated
	// catalog. The empty options value states that no mutation flag applies.
	if err := server.SetOption(ctx, "exit-empty", "off", tmux.SetOptionOptions{}); err != nil {
		fmt.Println("set option:", err)
		return
	}
	value, ok, err := server.RawOption(ctx, "exit-empty")
	fmt.Println(value, ok, err)
	// Output: off true <nil>
}

func ExampleServer_RawOption() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-raw-option",
	}).WithStrictErrors()
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// An option tmux does not have reports absence through ok, not through err.
	_, ok, err := server.RawOption(ctx, "no-such-option")
	fmt.Println(ok, err)
	// Output: false <nil>
}

func ExampleSession_SetBellAction() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-bell-action",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	// A typed setter rejects a value tmux would not take, at compile time for
	// the type and through Valid for the connected tmux version.
	if err := session.SetBellAction(ctx, tmux.BellActionNone); err != nil {
		fmt.Println("set bell-action:", err)
		return
	}
	values, err := session.Options(ctx)
	if err != nil {
		fmt.Println("read options:", err)
		return
	}
	action, ok := values.BellAction().Get()
	fmt.Println(action, ok)
	// Output: none true
}

func ExampleServer_WithEngine() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-with-engine",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	// A control-mode client is one persistent tmux process. Selecting its
	// engine makes the operations below reuse that connection instead of
	// starting a tmux process each.
	client, err := server.OpenControl(ctx, session)
	if err != nil {
		fmt.Println("open control:", err)
		return
	}
	defer func() { _ = client.Close() }()
	connected := server.WithEngine(client.Engine())

	window, err := connected.Session(ctx, session.ID())
	if err != nil {
		fmt.Println("look up session:", err)
		return
	}
	name, _ := window.Name()
	fmt.Println(name)
	// Output: build
}

func ExampleControlClient_Engine() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-client-engine",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	client, err := server.OpenControl(ctx, session)
	if err != nil {
		fmt.Println("open control:", err)
		return
	}
	defer func() { _ = client.Close() }()

	// The engine borrows the client; closing the client stops the process, and
	// SubprocessEngine returns a handle to starting one per command again.
	connected := server.WithEngine(client.Engine())
	forking := connected.WithEngine(server.SubprocessEngine())

	for _, handle := range []tmux.Server{connected, forking} {
		sessions, err := handle.Sessions(ctx)
		if err != nil {
			fmt.Println("list sessions:", err)
			return
		}
		fmt.Println(len(sessions))
	}
	// Output:
	// 1
	// 1
}

func ExampleServer_WaitFor() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-wait-for",
	}).WithStrictErrors()
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// WaitFor is tmux's wait-for channel between commands, not a way to wait
	// for a pane's output. A waiter blocks until something signals the channel,
	// so signal it from another goroutine.
	go func() {
		signalCtx, signalCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer signalCancel()
		_ = server.WaitFor(signalCtx, tmux.WaitForRequest{
			Channel: "ready",
			Mode:    tmux.WaitForModeSignal,
		})
	}()

	if err := server.WaitFor(ctx, tmux.WaitForRequest{Channel: "ready"}); err != nil {
		fmt.Println("wait:", err)
		return
	}
	fmt.Println("signalled")
	// Output: signalled
}

// ExampleServer_WaitFor_paneCompletion gates on a pane command finishing
// without reading the pane at all, which no echo can defeat.
func ExampleServer_WaitFor_paneCompletion() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	socket := "libtmux-go-example-wait-for-pane"
	server := tmux.NewServer(tmux.ServerOptions{SocketName: socket}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}

	// The command signals the channel itself, so the wait ends when the work
	// ends rather than when a matching line happens to reach the screen.
	command := "printf 'building\n'; tmux -L " + socket + " wait-for -S built"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
		fmt.Println("send keys:", err)
		return
	}
	if err := server.WaitFor(ctx, tmux.WaitForRequest{Channel: "built"}); err != nil {
		fmt.Println("wait:", err)
		return
	}
	fmt.Println("build finished")
	// Output: build finished
}

func ExamplePoll() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-poll",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}
	command := "printf 'build ready\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
		fmt.Println("send keys:", err)
		return
	}

	// tmux accepts the keys before the shell runs them, so the read is a wait.
	// Poll stops when the condition holds or ctx expires, whichever is first.
	//
	// slices.Contains compares whole lines on purpose. The shell echoes the
	// command, so the screen holds printf 'build ready\n' before the program
	// runs; searching that screen for the substring "build ready" would match
	// the echo and report success immediately. The echoed line carries the
	// surrounding command and so never equals the output on its own.
	err = tmux.Poll(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{})
		if err != nil {
			return false, err
		}
		return slices.Contains(lines, "build ready"), nil
	})
	if err != nil {
		fmt.Println("wait for output:", err)
		return
	}
	fmt.Println("build ready")
	// Output: build ready
}

// ExampleSubprocessRunner counts the requests that become tmux processes,
// which is how a caller confirms an engine is carrying work. The wrapper
// delegates execution, so it stays correct without knowing what a result
// has to look like.
func ExampleSubprocessRunner() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var mutex sync.Mutex
	var processes int
	counting := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		mutex.Lock()
		processes++
		mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})

	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-subprocess-runner",
		Runner:     counting,
	}).WithStrictErrors()
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"}); err != nil {
		fmt.Println("create session:", err)
		return
	}
	mutex.Lock()
	counted := processes > 0
	mutex.Unlock()
	fmt.Println("requests became processes:", counted)
	// Output: requests became processes: true
}

// ExampleControlClient_NextNotification waits for a pane's output without
// reading the pane. tmux sends what a pane writes as it is written, so nothing
// polls, nothing forks a tmux process per round, and no screen is searched.
//
// The window runs the program directly instead of typing it into a shell, so
// there is no echoed command line to tell apart from the program's own output.
func ExampleControlClient_NextNotification() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-output-events",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "stream"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	control, err := server.OpenControl(ctx, session)
	if err != nil {
		fmt.Println("open control:", err)
		return
	}
	defer func() { _ = control.Close() }()

	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Command: "sh -c 'sleep 1; printf \"service ready\\n\"; sleep 60'",
	})
	if err != nil {
		fmt.Println("create window:", err)
		return
	}
	pane, ok, err := window.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}

	var written []byte
	for !bytes.Contains(written, []byte("service ready")) {
		notification, err := control.NextNotification(ctx)
		if err != nil {
			fmt.Println("wait for output:", err)
			return
		}
		id, data, isOutput := notification.Output()
		if isOutput && id == pane.ID() {
			written = append(written, data...)
		}
	}
	fmt.Println("the pane reported it was ready")
	// Output: the pane reported it was ready
}

// ExamplePane_WithServer counts what a record costs before and after it is
// moved onto a connected handle. The counter is there because the failure it
// prevents is silent: a record made before the engine existed keeps starting a
// tmux process for every command and reports nothing wrong while doing so.
func ExamplePane_WithServer() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var mutex sync.Mutex
	var processes int
	counting := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		mutex.Lock()
		processes++
		mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})
	count := func() int {
		mutex.Lock()
		defer mutex.Unlock()
		return processes
	}

	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-with-server",
		Runner:     counting,
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}
	// tmux -V is a client-global option rather than a command, so no engine can
	// carry it. Reading it once now keeps that one process out of the counts.
	if _, err := server.Version(ctx); err != nil {
		fmt.Println("read version:", err)
		return
	}
	client, err := server.OpenControl(ctx, session)
	if err != nil {
		fmt.Println("open control:", err)
		return
	}
	defer func() { _ = client.Close() }()
	connected := server.WithEngine(client.Engine())

	before := count()
	if _, err := pane.Refresh(ctx); err != nil {
		fmt.Println("refresh the held record:", err)
		return
	}
	fmt.Println("processes for the record made before the engine:", count() > before)

	// The move is a value operation: the pane's handle is configuration, so
	// nothing is looked up again and no command is sent.
	pane = pane.WithServer(connected)

	before = count()
	if _, err := pane.Refresh(ctx); err != nil {
		fmt.Println("refresh the moved record:", err)
		return
	}
	fmt.Println("processes for the same record after the move:", count()-before)
	// Output:
	// processes for the record made before the engine: true
	// processes for the same record after the move: 0
}

// ExamplePane_CaptureToFile watches a pane on a connected handle. Every tmux
// command it sends prints nothing, so the loop reuses the control connection
// instead of starting a tmux process per round the way Pane.Capture does.
func ExamplePane_CaptureToFile() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-capture-to-file",
	}).WithStrictErrors()
	defer killExampleServer(server)

	// tmux writes this file, so it has to be a path the tmux server can reach.
	// A directory this process owns is one, because both run on this machine.
	directory, err := os.MkdirTemp("", "libtmux-go-capture")
	if err != nil {
		fmt.Println("create a directory for the capture:", err)
		return
	}
	defer func() { _ = os.RemoveAll(directory) }()
	path := filepath.Join(directory, "pane.txt")

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}
	client, err := server.OpenControl(ctx, session)
	if err != nil {
		fmt.Println("open control:", err)
		return
	}
	defer func() { _ = client.Close() }()
	pane = pane.WithServer(server.WithEngine(client.Engine()))

	command := "printf 'build ready\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &command}); err != nil {
		fmt.Println("send keys:", err)
		return
	}

	// The lines are the ones Pane.Capture reports, so the whole-line comparison
	// that survives the shell's echo is unchanged.
	err = tmux.Poll(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		lines, err := pane.CaptureToFile(ctx, path, tmux.CapturePaneRequest{})
		if err != nil {
			return false, err
		}
		return slices.Contains(lines, "build ready"), nil
	})
	if err != nil {
		fmt.Println("wait for output:", err)
		return
	}
	fmt.Println("build ready")
	// Output: build ready
}

// ExampleServer_OpenControlPool puts a handle on a control-mode transport in
// one call, so the commands that follow start no tmux processes.
func ExampleServer_OpenControlPool() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-control-pool",
	}).WithStrictErrors()
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	connected, _, pool, err := server.OpenControlPool(ctx, session, tmux.ControlPoolRequest{})
	if err != nil {
		fmt.Println("open pool:", err)
		return
	}
	defer func() { _ = pool.Close() }()

	// The session was taken before the pool existed, so it still carries the
	// forking handle. Moving it across needs no tmux command.
	windows, err := session.WithServer(connected).SearchWindows(ctx, nil)
	if err != nil {
		fmt.Println("search windows:", err)
		return
	}
	fmt.Println("windows read over the connection:", len(windows))
	// Output: windows read over the connection: 1
}
