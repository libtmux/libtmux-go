package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func requireNoDetachOnDestroy(t *testing.T, server tmux.Server) {
	t.Helper()
	version, err := server.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := tmux.ParseVersion("3.6")
	if err != nil {
		t.Fatal(err)
	}
	if !version.AtLeast(minimum) {
		t.Skipf("per-client session-destruction survival requires tmux 3.6; installed %s", version)
	}
}

//libtmux:real-tmux
func TestNewSessionConnectionQualifiesColdDaemonBeforeAttach(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	realBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	invocations := filepath.Join(t.TempDir(), "invocations")
	proxy := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(proxy, []byte(`#!/bin/sh
transport=process
operation=other
for argument do
    if [ "$argument" = run-shell ]; then probe=1; fi
    if [ "$argument" = -C ] && [ -z "$probe" ]; then transport=control; fi
    if [ "$argument" = new-session ]; then operation=creation; fi
done
printf '%s %s\n' "$transport" "$operation" >> "$LIBTMUX_CONNECTION_INVOCATIONS"
exec "$LIBTMUX_CONNECTION_REAL_TMUX" "$@"
`), 0o700); err != nil {
		t.Fatalf("write tmux proxy: %v", err)
	}
	server, err := tmux.NewServer(tmux.ServerOptions{
		Binary:     proxy,
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		ProcessEnvironment: append(
			os.Environ(),
			"LIBTMUX_CONNECTION_INVOCATIONS="+invocations,
			"LIBTMUX_CONNECTION_REAL_TMUX="+realBinary,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})

	tmuxtest.RequireVersion(t.Context(), t, server, tmux.MinimumConnectionVersion)
	created, connection, err := server.NewSessionConnection(
		ctx,
		tmux.NewSessionRequest{Name: "bootstrap", Width: 80, Height: 24},
		tmux.ConnectionOptions{Lanes: 2},
	)
	if err != nil {
		t.Fatalf("NewSessionConnection() error = %v", err)
	}
	if created.ID() == "" || connection == nil {
		t.Fatalf("NewSessionConnection() = (%#v, %#v), want session and connection", created, connection)
	}
	if connection.Lanes() != 2 {
		t.Fatalf("Lanes() = %d, want 2", connection.Lanes())
	}
	if name, ok := connection.Session().Name(); !ok || name != "bootstrap" {
		t.Fatalf("connected session name = (%q, %t), want (bootstrap, true)", name, ok)
	}

	recorded, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatalf("read tmux proxy invocations: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	if slices.Contains(lines, "control creation") {
		t.Fatalf("tmux proxy invocations = %q, cold creation must precede control", lines)
	}
	if !slices.Contains(lines, "process creation") {
		t.Fatalf("tmux proxy invocations = %q, want ordinary process session creation", lines)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	refreshed, err := server.Session(ctx, created.ID())
	if err != nil {
		t.Fatalf("created session disappeared after Close(): %v", err)
	}
	if name, ok := refreshed.Name(); !ok || name != "bootstrap" {
		t.Fatalf("created session name after Close() = (%q, %t)", name, ok)
	}
}

//libtmux:real-tmux
func TestNewSessionConnectionRetainsGuardedCreator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	const attachedMarker = "@libtmux-bootstrap-attached"
	result, err := server.Cmd(
		ctx,
		"set-hook",
		"-g",
		"client-attached",
		"set-option -ag "+attachedMarker+" x",
	)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("install client-attached hook = (%#v, %v), want exit 0", result, err)
	}

	tmuxtest.RequireVersion(t.Context(), t, sessions[0].Server(), tmux.MinimumConnectionVersion)
	created, connection, err := sessions[0].Server().NewSessionConnection(
		ctx,
		tmux.NewSessionRequest{Name: "guarded-bootstrap"},
		tmux.ConnectionOptions{Lanes: 2},
	)
	if err != nil {
		t.Fatalf("NewSessionConnection() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if name, ok := created.Name(); !ok || name != "guarded-bootstrap" {
		t.Fatalf("created session name = (%q, %t), want guarded-bootstrap", name, ok)
	}
	marker, markerErr := server.Cmd(ctx, "show-options", "-gqv", attachedMarker)
	if markerErr != nil || !slices.Equal(marker.Stdout, []string{"x"}) {
		t.Fatalf(
			"client-attached marker = (%#v, %v), want only the second lane to attach",
			marker,
			markerErr,
		)
	}
}

//libtmux:real-tmux
func TestConnectionOwnsTerminalControlLanes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	tmuxtest.RequireVersion(t.Context(), t, sessions[0].Server(), tmux.MinimumConnectionVersion)
	connection, err := sessions[0].OpenControl(ctx, tmux.ConnectionOptions{Lanes: 2})
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	if connection.Lanes() != 2 {
		t.Fatalf("Lanes() = %d, want 2", connection.Lanes())
	}

	renamed, err := connection.Session().Rename(ctx, "connected")
	if err != nil {
		t.Fatalf("connected Rename() error = %v", err)
	}
	if _, err := connection.Server().ShowBufferBytes(ctx, nil); !errors.Is(
		err,
		tmux.ErrConnectionRequiresProcess,
	) {
		t.Fatalf("ShowBufferBytes() error = %v, want process refusal", err)
	}
	closeCtx, cancelClose := context.WithCancel(context.Background())
	cancelClose()
	if err := connection.CloseContext(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext(canceled) error = %v, want context canceled", err)
	}
	if _, err := renamed.Refresh(ctx); !errors.Is(err, tmux.ErrControlClosed) {
		t.Fatalf("bound Refresh() error = %v, want ErrControlClosed", err)
	}
	if err := connection.CloseContext(ctx); err != nil {
		t.Fatalf("resumed CloseContext() error = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() after shutdown error = %v", err)
	}

	result, err := server.Cmd(ctx, "display-message", "-p", "still-alive")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("unbound Cmd() = (%#v, %v), want live daemon", result, err)
	}
}

//libtmux:real-tmux
func TestConnectionSurvivesAttachedSessionDestruction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	requireNoDetachOnDestroy(t, server)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	attached := sessions[0]
	survivor, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "survivor"})
	if err != nil {
		t.Fatal(err)
	}
	tmuxtest.RequireVersion(t.Context(), t, attached.Server(), tmux.MinimumConnectionVersion)
	connection, err := attached.OpenControl(ctx, tmux.ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	if err := attached.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	refreshed, err := connection.Server().Session(ctx, survivor.ID())
	if err != nil {
		t.Fatalf("connection after attached session destruction: %v", err)
	}
	if name, ok := refreshed.Name(); !ok || name != "survivor" {
		t.Fatalf("surviving session name = (%q, %t), want (survivor, true)", name, ok)
	}
}

//libtmux:real-tmux
func TestNewSessionConnectionSurvivesCreatedSessionDestruction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDetachOnDestroy(t, server)
	tmuxtest.RequireVersion(t.Context(), t, server, tmux.MinimumConnectionVersion)
	created, connection, err := server.NewSessionConnection(
		ctx,
		tmux.NewSessionRequest{Name: "created"},
		tmux.ConnectionOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		_ = server.Kill(context.Background())
	})
	survivor, err := connection.Server().NewSession(
		ctx,
		tmux.NewSessionRequest{Name: "survivor"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := created.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	refreshed, err := connection.Server().Session(ctx, survivor.ID())
	if err != nil {
		t.Fatalf("creator connection after session destruction: %v", err)
	}
	if name, ok := refreshed.Name(); !ok || name != "survivor" {
		t.Fatalf("surviving session name = (%q, %t), want (survivor, true)", name, ok)
	}
	if connection.Session().ID() != created.ID() {
		t.Fatalf("connection session identity changed from %s to %s",
			created.ID(), connection.Session().ID())
	}
}

//libtmux:real-tmux
func TestConnectionAtomicallyRejectsDaemonReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	stale := sessions[0]
	result, err := server.Cmd(ctx, "kill-server")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("kill-server = (%#v, %v), want exit 0", result, err)
	}
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		alive, aliveErr := server.IsAlive(ctx)
		return !alive, aliveErr
	}); err != nil {
		t.Fatalf("wait for original daemon exit: %v", err)
	}
	replacement, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "replacement"})
	if err != nil {
		t.Fatalf("start replacement daemon: %v", err)
	}
	const attachedMarker = "@libtmux-stale-control-attached"
	result, err = server.Cmd(
		ctx,
		"set-hook",
		"-g",
		"client-attached",
		"set-option -g "+attachedMarker+" reached",
	)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("install replacement attach hook = (%#v, %v)", result, err)
	}

	tmuxtest.RequireVersion(t.Context(), t, stale.Server(), tmux.MinimumConnectionVersion)
	connection, err := stale.OpenControl(ctx, tmux.ConnectionOptions{})
	if connection != nil {
		_ = connection.Close()
	}
	if !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf("stale OpenControl() = (%#v, %v), want ErrDaemonReplaced", connection, err)
	}
	marker, markerErr := server.Cmd(ctx, "show-options", "-gqv", attachedMarker)
	if markerErr != nil || len(marker.Stdout) != 0 {
		t.Fatalf("replacement attach marker = (%#v, %v), want absent", marker, markerErr)
	}
	replacement, err = replacement.Refresh(ctx)
	if err != nil {
		t.Fatalf("refresh replacement: %v", err)
	}
	if name, ok := replacement.Name(); !ok || name != "replacement" {
		t.Fatalf("replacement name = (%q, %t), want (replacement, true)", name, ok)
	}
}

//libtmux:real-tmux
func TestNewSessionConnectionAtomicallyRejectsDaemonReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	stale := sessions[0].Server()
	result, err := server.Cmd(ctx, "kill-server")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("kill-server = (%#v, %v), want exit 0", result, err)
	}
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		alive, aliveErr := server.IsAlive(ctx)
		return !alive, aliveErr
	}); err != nil {
		t.Fatalf("wait for original daemon exit: %v", err)
	}
	if _, err := server.NewSession(
		ctx,
		tmux.NewSessionRequest{Name: "replacement"},
	); err != nil {
		t.Fatalf("start replacement daemon: %v", err)
	}

	tmuxtest.RequireVersion(t.Context(), t, stale, tmux.MinimumConnectionVersion)
	created, connection, err := stale.NewSessionConnection(
		ctx,
		tmux.NewSessionRequest{},
		tmux.ConnectionOptions{},
	)
	if connection != nil {
		_ = connection.Close()
	}
	if created.ID() != "" || !errors.Is(err, tmux.ErrDaemonReplaced) {
		t.Fatalf(
			"stale NewSessionConnection() = (%#v, %#v, %v), want zero, nil, ErrDaemonReplaced",
			created,
			connection,
			err,
		)
	}
	replacementSessions, sessionsErr := server.Sessions(ctx)
	if sessionsErr != nil || len(replacementSessions) != 1 {
		t.Fatalf(
			"replacement sessions = (%d, %v), want only the existing replacement",
			len(replacementSessions),
			sessionsErr,
		)
	}
}

// openOwnedConnection opens a connection on the server's first session and
// closes it when the test ends.
func openOwnedConnection(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	options tmux.ConnectionOptions,
) *tmux.Connection {
	t.Helper()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	tmuxtest.RequireVersion(t.Context(), t, sessions[0].Server(), tmux.MinimumConnectionVersion)
	connection, err := sessions[0].OpenControl(ctx, options)
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

// awaitClientBeyond returns once the server counts more than lanes attached
// clients, which a blocking call on a connection adds for its own duration.
// tmux announces no client attaching, so it asks, backing off between reads,
// and fails the test when the client never appears: the caller's assertions
// mean nothing without it.
func awaitClientBeyond(ctx context.Context, t *testing.T, server tmux.Server, lanes int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		clients, err := server.Clients(ctx)
		return err == nil && len(clients) > lanes, nil
	}); err != nil {
		t.Fatalf("the blocking call never attached a client of its own: %v", err)
	}
}

//libtmux:real-tmux
func TestConnectionServesCommandsAfterACanceledWaitFor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})

	waitCtx, stopWaiting := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stopWaiting()
	err := connection.Server().WaitFor(waitCtx, tmux.WaitForRequest{Channel: "nobody-signals"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitFor() error = %v, want deadline exceeded", err)
	}

	nextCtx, stopNext := context.WithTimeout(ctx, 5*time.Second)
	defer stopNext()
	if _, err := connection.Server().Sessions(nextCtx); err != nil {
		t.Fatalf("Sessions() after a canceled WaitFor error = %v, want the lane usable", err)
	}
}

// A foreground run-shell or if-shell holds its client until the shell command
// finishes, and tmux cannot cancel it once sent. Canceling one must leave the
// connection's only lane free for the next command.
//
//libtmux:real-tmux
func TestConnectionServesCommandsAfterACanceledShellCommand(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(context.Context, tmux.Server, string) error
	}{
		{"RunShell", func(ctx context.Context, server tmux.Server, command string) error {
			_, err := server.RunShell(ctx, tmux.RunShellRequest{Command: command})
			return err
		}},
		{"IfShell", func(ctx context.Context, server tmux.Server, command string) error {
			return server.IfShell(ctx, tmux.IfShellRequest{
				ShellCommand: command, ThenCommand: "display-message done",
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			server := tmuxtest.NewServer(ctx, t)
			connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})

			// The shell command runs for as long as a file exists. tmux keeps
			// running it after the call is canceled and will not exit while it
			// runs, so the file sits in the test's own directory: removing
			// that directory, which happens before the harness cleans up, is
			// what lets the command go.
			held := filepath.Join(t.TempDir(), "held")
			if err := os.WriteFile(held, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := "while [ -e '" + held + "' ]; do sleep 0.05; done"

			shellCtx, stopShell := context.WithTimeout(ctx, 200*time.Millisecond)
			defer stopShell()
			err := test.run(shellCtx, connection.Server(), command)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s() error = %v, want deadline exceeded", test.name, err)
			}

			nextCtx, stopNext := context.WithTimeout(ctx, 3*time.Second)
			defer stopNext()
			if _, err := connection.Server().Sessions(nextCtx); err != nil {
				t.Fatalf("Sessions() after a canceled %s error = %v, want the lane usable",
					test.name, err)
			}
		})
	}
}

//libtmux:real-tmux
func TestConnectionCloseEndsAParkedWaitFor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})

	waited := make(chan error, 1)
	go func() {
		waited <- connection.Server().WaitFor(ctx, tmux.WaitForRequest{Channel: "nobody-signals"})
	}()
	awaitClientBeyond(ctx, t, server, 1)
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-waited:
		if !errors.Is(err, tmux.ErrControlClosed) {
			t.Fatalf("WaitFor() error = %v, want ErrControlClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitFor() still parked after Close()")
	}
}

//libtmux:real-tmux
func TestSessionRunWorksOnAConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})
	session := connection.Session()
	before, err := connection.Server().Windows(ctx)
	if err != nil {
		t.Fatal(err)
	}

	result, err := session.Run(ctx, "echo hello; exit 3", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Run() on a connection error = %v", err)
	}
	if result.Status != 3 || !slices.Equal(result.Lines, []string{"hello"}) {
		t.Fatalf("Run() = status %d, lines %q, want status 3, [hello]", result.Status, result.Lines)
	}
	after, err := connection.Server().Windows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("windows after Run = %d, want the %d it started with", len(after), len(before))
	}
}

//libtmux:real-tmux
func TestRunningKillWorksWhileWaitIsParkedOnOneLane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{Lanes: 1})

	running, err := connection.Session().Start(ctx, "sleep 30", tmux.RunOptions{})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waited := make(chan error, 1)
	go func() {
		_, waitErr := running.Wait(ctx)
		waited <- waitErr
	}()
	// The failure this guards is Kill queueing behind a parked wait.
	awaitClientBeyond(ctx, t, server, 1)

	killCtx, stopKill := context.WithTimeout(ctx, 2*time.Second)
	defer stopKill()
	if err := running.Kill(killCtx); err != nil {
		t.Fatalf("Kill() while Wait is parked error = %v", err)
	}
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Wait() after Kill error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Wait() did not return after Kill")
	}
}

// tmux ends a waiting client with success when the server exits, so a bound
// server must learn from outside its own lanes, which die with the server,
// that the server is gone.
//
//libtmux:real-tmux
func TestConnectionBlockingCommandReportsAServerKilledUnderIt(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(context.Context, tmux.Server, string) error
	}{
		{"WaitFor", func(ctx context.Context, server tmux.Server, _ string) error {
			return server.WaitFor(ctx, tmux.WaitForRequest{Channel: "libtmux-killed"})
		}},
		{"RunShell", func(ctx context.Context, server tmux.Server, command string) error {
			_, err := server.RunShell(ctx, tmux.RunShellRequest{Command: command})
			return err
		}},
		{"IfShell", func(ctx context.Context, server tmux.Server, command string) error {
			return server.IfShell(ctx, tmux.IfShellRequest{
				ShellCommand: command, ThenCommand: "display-message x",
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			server := tmuxtest.NewServer(ctx, t)
			connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})

			// The shell command outlives the server, so it runs until the
			// file goes, as in TestConnectionServesCommandsAfterACanceledShellCommand.
			held := filepath.Join(t.TempDir(), "held")
			if err := os.WriteFile(held, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := "while [ -e '" + held + "' ]; do sleep 0.05; done"

			done := make(chan error, 1)
			go func() { done <- test.run(ctx, connection.Server(), command) }()
			awaitClientBeyond(ctx, t, server, 1)
			if _, err := server.Cmd(ctx, "kill-server"); err != nil {
				t.Fatalf("kill-server error = %v", err)
			}
			if err := os.Remove(held); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !errors.Is(err, tmux.ErrNoServer) {
					t.Fatalf("%s with the server killed under it = %v, want ErrNoServer", test.name, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s still parked after the server was killed", test.name)
			}
		})
	}
}

// A connection closed under a parked call says nothing about the server, which
// is healthy, so the call must not read as a server that exited.
//
//libtmux:real-tmux
func TestConnectionClosedUnderABlockingCommandIsNotAGoneServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})

	done := make(chan error, 1)
	go func() {
		done <- connection.Server().WaitFor(ctx, tmux.WaitForRequest{Channel: "libtmux-closed"})
	}()
	awaitClientBeyond(ctx, t, server, 1)
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, tmux.ErrControlClosed) || errors.Is(err, tmux.ErrNoServer) {
			t.Fatalf("WaitFor() after Close() = %v, want ErrControlClosed and not ErrNoServer", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitFor() still parked after Close()")
	}
}

// Once the connection's own session is gone, a blocking call must still
// return at its deadline with that deadline as the error, and must attach to
// the session its lanes moved to rather than try the dead one first.
//
//libtmux:real-tmux
func TestConnectionBlockingCommandAfterItsSessionDied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	requireNoDetachOnDestroy(t, server)
	original := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "survivor"}); err != nil {
		t.Fatal(err)
	}
	if err := original.Session().Kill(ctx); err != nil {
		t.Fatal(err)
	}

	const deadline = 100 * time.Millisecond
	held, stop := context.WithTimeout(ctx, deadline)
	defer stop()
	started := time.Now()
	err := original.Server().WaitFor(held, tmux.WaitForRequest{Channel: "libtmux-orphaned"})
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitFor() = %v, want the deadline", err)
	}
	if elapsed > deadline+100*time.Millisecond {
		t.Fatalf("WaitFor() returned after %v, want about %v", elapsed, deadline)
	}

	// The next open must not pay for the dead session again.
	started = time.Now()
	if _, err := original.Server().RunShell(ctx, tmux.RunShellRequest{Command: "true"}); err != nil {
		t.Fatalf("RunShell() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("RunShell() took %v, want an open that skips the dead session", elapsed)
	}
}

// tmux hands a control client a foreground run-shell's output as bare lines
// between its empty reply frame and the next command's, so the output must be
// read as the result and never as protocol. Lines that imitate guards are
// the case that matters: they must come back as text and leave the lane
// usable.
//
//libtmux:real-tmux
func TestConnectionRunShellReturnsItsOutput(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		want    []string
	}{
		{"one line", "echo hi", []string{"hi"}},
		{"several lines", "printf 'one\\ntwo\\nthree\\n'", []string{"one", "two", "three"}},
		{"no output", "true", nil},
		{
			"imitated frames",
			"printf '%%begin 1 2 1\\nbody\\n%%end 1 2 1\\n%%error 1 2 1\\n%%end 1 2 3\\n'",
			[]string{"%begin 1 2 1", "body", "%end 1 2 1", "%error 1 2 1", "%end 1 2 3"},
		},
		{"lone closing guard", "printf '%%end 1 2 3\\n'", []string{"%end 1 2 3"}},
		// Whether tmux keeps a blank line depends on its version, so the unbound
		// server answers for it.
		{"blank line", "printf 'a\\n\\nb\\n'", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			server := tmuxtest.NewServer(ctx, t)
			connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})

			got, err := connection.Server().RunShell(ctx, tmux.RunShellRequest{Command: test.command})
			if err != nil {
				t.Fatalf("RunShell(%q) on a connection error = %v", test.command, err)
			}
			unbound, err := server.RunShell(ctx, tmux.RunShellRequest{Command: test.command})
			if err != nil {
				t.Fatalf("RunShell(%q) on the unbound server error = %v", test.command, err)
			}
			// tmux 3.3 and 3.4 hand a foreground run-shell's output to no
			// client, bound or not, so only the two agreeing is asked there.
			version, err := server.Version(ctx)
			if err != nil {
				t.Fatal(err)
			}
			from33, _ := tmux.ParseVersion("3.3")
			from35, _ := tmux.ParseVersion("3.5")
			printsOutput := !version.AtLeast(from33) || version.AtLeast(from35)
			if !slices.Equal(got, unbound) ||
				(printsOutput && test.want != nil && !slices.Equal(got, test.want)) {
				t.Fatalf("RunShell(%q) on a connection = %q, want %q (unbound %q)",
					test.command, got, test.want, unbound)
			}
			if _, err := connection.Server().Sessions(ctx); err != nil {
				t.Fatalf("Sessions() after RunShell error = %v, want the lane usable", err)
			}
		})
	}
}

// A branch that fails fails like it does on an unbound server, instead of
// breaking the control stream.
//
//libtmux:real-tmux
func TestConnectionIfShellReportsAFailedBranch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := tmuxtest.NewServer(ctx, t)
	connection := openOwnedConnection(ctx, t, server, tmux.ConnectionOptions{})
	request := tmux.IfShellRequest{ShellCommand: "true", ThenCommand: "no-such-command"}

	if err := server.IfShell(ctx, request); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("unbound IfShell() error = %v, want ErrCommand", err)
	}
	if err := connection.Server().IfShell(ctx, request); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("connected IfShell() error = %v, want ErrCommand", err)
	}
	// A branch that prints answers in a frame of its own, ahead of the fence.
	printing := tmux.IfShellRequest{ShellCommand: "true", ThenCommand: "display-message -p printed"}
	if err := connection.Server().IfShell(ctx, printing); err != nil {
		t.Fatalf("connected IfShell() with a printing branch error = %v", err)
	}
	// run-shell -C runs the command's output as tmux commands, and a bad one
	// fails the same way.
	bad := tmux.RunShellRequest{Command: "echo no-such-command", AsTmuxCommand: true}
	if _, err := connection.Server().RunShell(ctx, bad); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("connected RunShell(AsTmuxCommand) error = %v, want ErrCommand", err)
	}
	if _, err := connection.Server().Sessions(ctx); err != nil {
		t.Fatalf("Sessions() after IfShell error = %v, want the lane usable", err)
	}
}
