package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmuxq"
)

// exampleWaitBudget bounds an example waiting on a program in a pane. It is a
// ceiling rather than a delay -- each wait below ends as soon as its condition
// holds -- so it is generous: one tight enough to be exceeded on a busy machine
// fails an example with nothing wrong with it.
const exampleWaitBudget = 60 * time.Second

// killExampleServer stops an example's server on a context of its own. An
// example's ctx is expired exactly when its run failed on the deadline, which
// is when cleanup matters most, and the socket it names is fixed: a server left
// running fails every later run with a session that already exists.
func killExampleServer(server tmux.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Kill(ctx)
}

func Example() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-workflow",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "project", WindowName: "editor",
	})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	windowName := "tests"
	window, err := session.NewWindow(ctx, tmux.NewWindowRequest{
		Name: &windowName, Attach: true,
	})
	if err != nil {
		fmt.Println("create window:", err)
		return
	}
	pane, err := window.SplitPane(ctx, tmux.SplitPaneRequest{
		Direction: tmux.PaneDirectionRight,
	})
	if err != nil {
		fmt.Println("split pane:", err)
		return
	}
	selected, err := pane.Select(ctx, tmux.PaneSelectRequest{})
	if err != nil {
		fmt.Println("select pane:", err)
		return
	}

	// window is the record NewWindow returned, so the panes it carries are the
	// ones it was created with. SearchPanes asks tmux instead, which is what
	// sees the split.
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		fmt.Println("search panes:", err)
		return
	}
	name, _ := window.Name()
	fmt.Println(name, len(panes), selected.ID() == pane.ID())
	// Output: tests 2 true
}

func ExampleNewPlan() {
	plan := tmux.NewPlan()
	session := plan.NewSession(tmux.NewSessionRequest{Name: "project"})
	plan.RenameSession(session, "renamed")
	plan.SetEnvironment(session, "APP_MODE", "development")

	preview, err := plan.Preview(tmux.Version{})
	if err != nil {
		fmt.Println("preview:", err)
		return
	}
	fmt.Println(preview[0])
	fmt.Println(preview[1] == nil, preview[2] == nil)
	for _, dispatch := range plan.Explain() {
		fmt.Printf("steps %v: %s\n", dispatch.Ops, dispatch.Reason)
	}

	// Output:
	// [new-session -P -F#{session_id} -sproject -d]
	// true true
	// steps [0]: creates
	// steps [1 2]: chained
}

func ExamplePane_SendKeys() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-send-keys",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", err)
		return
	}

	command := "printf 'build ready\\n'"
	if err := pane.SendKeys(ctx, tmux.SendKeysRequest{
		Command: &command,
		Literal: true,
	}); err != nil {
		fmt.Println("send keys:", err)
		return
	}

	// Keys reach a shell, which runs them when it gets to them, so the pane is
	// read until the output appears rather than once afterwards.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		lines, err := pane.Capture(ctx, tmux.CapturePaneRequest{
			Start: tmux.CaptureBoundary,
			End:   tmux.CaptureBoundary,
		})
		if err != nil {
			fmt.Println("capture:", err)
			return
		}
		if slices.Contains(lines, "build ready") {
			break
		}
		select {
		case <-ctx.Done():
			fmt.Println("the pane never showed it")
			return
		case <-ticker.C:
		}
	}

	fmt.Println("the pane echoed the command's output")
	// Output: the pane echoed the command's output
}

func ExamplePane_CaptureBytes() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-capture-bytes",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	pane, ok, err := session.ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", err)
		return
	}

	// CaptureBytes returns what the pane holds without decoding it, which is
	// what a pane drawing anything but text has to be read with.
	output, err := pane.CaptureBytes(ctx, tmux.CapturePaneRequest{
		Start: tmux.CaptureBoundary,
		End:   tmux.CaptureBoundary,
	})
	if err != nil {
		fmt.Println("capture:", err)
		return
	}
	fmt.Println(utf8.Valid(output))
	// Output: true
}

func ExampleServer_Sessions() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-sessions",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "build", WindowName: "editor",
	}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// Sessions resolves the hierarchy, so each record carries its windows and
	// each window its panes. Reading them costs no further tmux command.
	sessions, err := server.Sessions(ctx)
	if err != nil {
		fmt.Println("list sessions:", err)
		return
	}
	for _, session := range sessions {
		name, _ := session.Name()
		windows, _ := session.Windows()
		for _, window := range windows {
			windowName, _ := window.Name()
			panes, _ := window.Panes()
			fmt.Println(name, windowName, len(panes))
		}
	}
	// Output: build editor 1
}

func ExampleServer_Cmd() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-cmd",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// Cmd runs a tmux command this package has no typed method for. It reports
	// tmux's own failure through the result rather than through err, which is
	// reserved for the command not running at all.
	result, err := server.Cmd(ctx, "display-message", "-p", "#{session_name}")
	if err != nil {
		fmt.Println("run display-message:", err)
		return
	}
	if result.ExitCode != 0 {
		fmt.Println(result.Stderr)
		return
	}
	fmt.Println(result.Stdout)
	fmt.Printf("%q\n", result.RawStdout)
	// Output:
	// [build]
	// "build\n"
}

func ExampleServer_OpenControl() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-open-control",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = client.CloseContext(closeCtx)
	}()

	result, err := client.Cmd(ctx, "display-message", "-p", "ready")
	if err != nil || result.Failed {
		fmt.Println("command over the control client:", err)
		return
	}

	// A control frame carries its payload bytes, line feed included, rather
	// than a decoded string: the frame is what tmux sent.
	fmt.Printf("%q\n", result.RawStdout)

	// Reconnect replaces the client with one on a new attachment. The old one
	// is spent, so the result is what later commands go through.
	reconnected, err := client.Reconnect(ctx)
	if err != nil {
		fmt.Println("reconnect:", err)
		return
	}
	client = reconnected

	result, err = client.Cmd(ctx, "display-message", "-p", "still here")
	if err != nil {
		fmt.Println("command after reconnecting:", err)
		return
	}
	fmt.Printf("%q\n", result.RawStdout)
	// Output:
	// "ready\n"
	// "still here\n"
}

func ExampleServer_ShowBufferBytes() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-show-buffer",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}
	name := "clipboard"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{
		Name: &name, Data: "copied text",
	}); err != nil {
		fmt.Println("set buffer:", err)
		return
	}

	// A buffer holds whatever was copied into it, which need not be text, so
	// the bytes accessor returns it undecoded.
	output, err := server.ShowBufferBytes(ctx, &name)
	if err != nil {
		fmt.Println("show buffer:", err)
		return
	}
	fmt.Printf("%q\n", output)
	// Output: "copied text"
}

func ExampleServer_Snapshot() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-snapshot",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// A snapshot reads the whole server once. Every record it holds carries its
	// relations, so walking the hierarchy afterwards runs no further command
	// and cannot see the server change underneath it.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		fmt.Println("snapshot:", err)
		return
	}
	for _, session := range snapshot.Sessions() {
		name, _ := session.Name()
		panes, _ := session.Panes()
		fmt.Println(name, len(panes))
	}
	fmt.Println(len(snapshot.Windows()), len(snapshot.Panes()))
	// Output:
	// build 1
	// 1 1
}

func ExampleSession_ResolveActiveWindow() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-resolve-window",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{
		Name: "build", WindowName: "editor",
	})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	// A session record names its active window but does not carry it. Resolving
	// asks tmux, so the answer reflects a window selected since.
	window, err := session.ResolveActiveWindow(ctx)
	if err != nil {
		fmt.Println("resolve window:", err)
		return
	}
	name, _ := window.Name()
	fmt.Println(name)
	// Output: editor
}

func ExamplePaneFilter_Predicate() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-pane-filter",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
		fmt.Println("split pane:", err)
		return
	}
	panes, err := window.SearchPanes(ctx, nil)
	if err != nil {
		fmt.Println("search panes:", err)
		return
	}

	// A filter compiles to a predicate once and is then applied in Go, which is
	// what makes it usable against records already in hand.
	minimumIndex := 0
	predicate, err := (tmux.PaneFilter{IndexGT: &minimumIndex}).Predicate()
	if err != nil {
		fmt.Println("compile filter:", err)
		return
	}

	fmt.Println(len(panes), len(tmuxq.Where(panes, predicate)))
	// Output: 2 1
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

func ExampleErrNoServer() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-no-server",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	// Nothing has started a server on this socket yet, which is the state
	// ErrNoServer reports.
	report := func() {
		sessions, err := server.Sessions(ctx)
		switch {
		case errors.Is(err, tmux.ErrNoServer):
			// Nothing is running yet, which is not a failure worth reporting: a
			// tmux server holding no sessions exits, so an absent server and an
			// empty one are the same state.
			fmt.Println("no sessions")
		case err != nil:
			// Anything else means the question could not be answered: a socket
			// that cannot be read, a path that is not a socket, a tmux too old.
			if commandError, ok := errors.AsType[*tmux.CommandError](err); ok {
				fmt.Println(commandError.Subcommand, commandError.Result.ExitCode)
			}
		default:
			fmt.Println(len(sessions), "sessions")
		}
	}

	report()
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}
	report()
	// Output:
	// no sessions
	// 1 sessions
}

func ExampleSession_Options() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-session-options",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	if err := session.SetMouse(ctx, true); err != nil {
		fmt.Println("set mouse:", err)
		return
	}

	options, err := session.Options(ctx)
	if err != nil {
		fmt.Println("read options:", err)
		return
	}

	// Origin separates a value set at this scope from one reaching it from a
	// parent, which is the difference between configured here and merely in
	// effect here.
	mouse, present := options.Mouse().Get()
	mouseOrigin, _ := options.Mouse().Origin()
	fmt.Println(mouse, present, mouseOrigin)

	// base-index was never set on this session, so it reaches it from the
	// global scope. What it holds depends on the configuration tmux loaded;
	// where it came from does not.
	baseOrigin, _ := options.BaseIndex().Origin()
	fmt.Println(baseOrigin)
	// Output:
	// true true local
	// inherited
}

func ExampleSession_SetMouse() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-set-mouse",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	// The typed setter takes the option's own type, so a boolean option is set
	// with a bool rather than with tmux's "on" and "off".
	if err := session.SetMouse(ctx, true); err != nil {
		fmt.Println("set mouse:", err)
		return
	}

	options, err := session.Options(ctx)
	if err != nil {
		fmt.Println("read options:", err)
		return
	}
	mouse, _ := options.Mouse().Get()
	fmt.Println(mouse)
	// Output: true
}

func ExampleSession_SetUpdateEnvironment() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-update-environment",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	// An array option is addressed by index, and the indices need not be
	// contiguous: this sets 0 and 3 and leaves 1 and 2 unset.
	values, err := tmux.NewSparseArray(
		tmux.SparseEntry[string]{Index: 0, Value: "DISPLAY"},
		tmux.SparseEntry[string]{Index: 3, Value: "SSH_AUTH_SOCK"},
	)
	if err != nil {
		fmt.Println("build array:", err)
		return
	}
	result, err := session.SetUpdateEnvironment(ctx, values)
	if err != nil {
		fmt.Println("set update-environment:", err)
		return
	}
	fmt.Println(result.Replaced, result.AppliedIndices)
	// Output: true [0 3]
}

func ExampleGlobalSessionScope_SetHook() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-global-hook",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// The global scope sets a hook for every session, including ones created
	// after it, rather than for one session that already exists.
	global := server.GlobalSessionScope()
	if err := global.SetHook(ctx, "client-attached", "display-message 'client attached'"); err != nil {
		fmt.Println("set hook:", err)
		return
	}
	hooks, err := global.Hooks(ctx)
	if err != nil {
		fmt.Println("read hooks:", err)
		return
	}
	// A hook holds an array of commands rather than one, so the value is
	// sparse: tmux numbers the entries and any index may be absent.
	commands, present := hooks.ClientAttached().Get()
	if !present {
		fmt.Println("no client-attached hook")
		return
	}
	command, _ := commands.Get(0)
	fmt.Println(commands.Indices(), command)
	// Output: [0] display-message "client attached"
}

func ExampleServer_SearchPanes() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-server-search-panes",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// tmux evaluates the filter, so a pane is matched by what it is running
	// rather than by anything this program has to fetch and compare.
	editors := tmux.TmuxFilter("#{==:#{pane_current_command},nvim}")
	matched, err := server.SearchPanes(ctx, &editors)
	if err != nil {
		fmt.Println("search panes:", err)
		return
	}
	fmt.Println("editors:", len(matched))

	// A nil filter asks for every pane.
	all, err := server.SearchPanes(ctx, nil)
	if err != nil {
		fmt.Println("search panes:", err)
		return
	}
	fmt.Println("panes:", len(all))
	// Output:
	// editors: 0
	// panes: 1
}

func ExampleNewServer() {
	// NewServer validates and records configuration; it does not start tmux.
	server, err := tmux.NewServer(tmux.ServerOptions{SocketPath: "/tmp/libtmux-go-example.sock"})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}

	fmt.Println(server.SocketPath())
	// Output: /tmp/libtmux-go-example.sock
}

func ExampleServer_WithSocketPath() {
	executable, err := os.Executable()
	if err != nil {
		fmt.Println("test executable:", err)
		return
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary:     executable,
		SocketName: "application",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	sibling, err := server.WithSocketPath("/tmp/sibling.sock")
	if err != nil {
		fmt.Println("sibling server:", err)
		return
	}

	fmt.Println(server)
	fmt.Println(sibling)
	// Output:
	// Server(socket_name=application)
	// Server(socket_path=/tmp/sibling.sock)
}

func ExampleServer_SocketSelection() {
	executable, err := os.Executable()
	if err != nil {
		fmt.Println("test executable:", err)
		return
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary:     executable,
		SocketPath: "/tmp/application.sock",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	selection, err := server.SocketSelection()
	if err != nil {
		fmt.Println("socket selection:", err)
		return
	}

	fmt.Println(selection.Path)
	// Output: /tmp/application.sock
}

func ExampleServer_NewSession() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-new-session",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-new-window",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-split-pane",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-capture",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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

func ExampleServerOptions_Binary() {
	executable, err := os.Executable()
	if err != nil {
		fmt.Println("test executable:", err)
		return
	}
	server, err := tmux.NewServer(tmux.ServerOptions{Binary: executable})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	fmt.Println(filepath.IsAbs(server.Executable()))
	// Output: true
}

func ExampleServer_Session() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-session-lookup",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-window-lookup",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-pane-lookup",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-client-lookup",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"}); err != nil {
		fmt.Println("create session:", err)
		return
	}

	// A detached server has no clients, so the lookup reports absence as a
	// classified error rather than an empty value.
	_, err = server.Client(ctx, tmux.ClientName("/dev/pts/999"))
	fmt.Println(errors.Is(err, tmux.ErrSnapshotNotFound))
	// Output: true
}

func ExampleWindow_SearchPanes() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-search-panes",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-window-panes",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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

	// Panes reads the record's own materialized state and never queries tmux.
	// The second result says whether the record carries relations at all, which
	// is the difference between "this window has no panes" -- a window tmux
	// would have destroyed -- and "this record cannot answer". A targeted point
	// lookup cannot; a resolver can.
	looked, err := server.Window(ctx, window.ID())
	if err != nil {
		fmt.Println("look up window:", err)
		return
	}
	lookedPanes, ok := looked.Panes()
	fmt.Println("from a point lookup:", len(lookedPanes), ok)

	resolvedPanes, ok := window.Panes()
	fmt.Println("from a resolver:", len(resolvedPanes), ok)

	// A snapshot materializes the whole hierarchy, so its records carry them.
	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		fmt.Println("snapshot:", err)
		return
	}
	for _, materialized := range snapshot.Windows() {
		panes, ok := materialized.Panes()
		fmt.Println("from a snapshot:", len(panes), ok)
	}

	// A record that cannot answer still can, through tmux: SearchPanes asks.
	searched, err := looked.SearchPanes(ctx, nil)
	if err != nil {
		fmt.Println("search panes:", err)
		return
	}
	fmt.Println("from a search:", len(searched))
	// Output:
	// from a point lookup: 0 false
	// from a resolver: 1 true
	// from a snapshot: 1 true
	// from a search: 1
}

func ExampleServer_SetOption() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-set-option",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-raw-option",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-bell-action",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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

func ExampleControlClient_Call() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-control-call",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "build"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	alias := "go-two=display-message -p one ; display-message -p two"
	configured, err := server.Cmd(ctx, "set-option", "-s", "command-alias[80]", alias)
	if err != nil || configured.ExitCode != 0 {
		fmt.Println("set alias:", err)
		return
	}
	client, err := server.OpenControl(ctx, session)
	if err != nil {
		fmt.Println("open control:", err)
		return
	}
	defer func() { _ = client.Close() }()

	// Call preserves every reply from an alias; Cmd requires exactly one.
	results, err := client.Call(ctx, "go-two")
	if err != nil {
		fmt.Println("call alias:", err)
		return
	}
	fmt.Println(
		len(results),
		string(bytes.TrimSpace(results[0].RawStdout)),
		string(bytes.TrimSpace(results[1].RawStdout)),
	)
	// Output: 2 one two
}

func ExampleServer_WaitFor() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-wait-for",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{SocketName: socket})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-poll",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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

	// tmux accepts keys before the shell runs them, so poll for output. Match the
	// whole line because a substring search would match the echoed command first.
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

// ExampleControlClient_NextNotification waits for a pane's output without
// reading the pane. tmux sends what a pane writes as it is written, so nothing
// polls, nothing forks a tmux process per round, and no screen is searched.
//
// The window runs the program directly instead of typing it into a shell, so
// there is no echoed command line to tell apart from the program's own output.
func ExampleControlClient_NextNotification() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-output-events",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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

// ExamplePane_CaptureToFile watches a pane on a connected handle. Every tmux
// command it sends prints nothing, so the loop reuses the control connection
// instead of starting a tmux process per round the way Pane.Capture does.
func ExamplePane_CaptureToFile() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-capture-to-file",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
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
	connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		fmt.Println("open connection:", err)
		return
	}
	defer func() { _ = connection.Close() }()
	pane, ok, err := connection.Session().ResolveActivePane(ctx)
	if err != nil || !ok {
		fmt.Println("resolve pane:", ok, err)
		return
	}

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

// ExampleSession_OpenControl binds ordinary model values to owned control-mode
// lanes on tmux 3.6 or later. The fallback keeps the example runnable across
// the package's older subprocess-only support range.
func ExampleSession_OpenControl() {
	ctx, cancel := context.WithTimeout(context.Background(), exampleWaitBudget)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketName: "libtmux-go-example-control-pool",
	})
	if err != nil {
		fmt.Println("new server:", err)
		return
	}
	defer killExampleServer(server)

	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "work"})
	if err != nil {
		fmt.Println("create session:", err)
		return
	}

	connection, err := session.OpenControl(ctx, tmux.ConnectionOptions{})
	if errors.Is(err, tmux.ErrVersionTooLow) {
		windows, fallbackErr := session.SearchWindows(ctx, nil)
		if fallbackErr != nil {
			fmt.Println("search windows:", fallbackErr)
			return
		}
		fmt.Println("windows:", len(windows))
		return
	}
	if err != nil {
		fmt.Println("open control connection:", err)
		return
	}
	defer func() { _ = connection.Close() }()

	windows, err := connection.Session().SearchWindows(ctx, nil)
	if err != nil {
		fmt.Println("search windows:", err)
		return
	}
	fmt.Println("windows:", len(windows))
	// Output: windows: 1
}
