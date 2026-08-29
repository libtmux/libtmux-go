package integration

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func requireTerminalConnection(t *testing.T, server tmux.Server) {
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
		t.Skipf("terminal connections require tmux 3.6; installed %s", version)
	}
}

//libtmux:real-tmux
func TestNewSessionConnectionKeepsTheCreatingControlProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mutex sync.Mutex
	var requests [][]string
	runner := tmux.CommandRunnerFunc(func(
		ctx context.Context,
		request tmux.CommandRequest,
	) (tmux.CommandResult, error) {
		mutex.Lock()
		requests = append(requests, append([]string(nil), request.Arguments...))
		mutex.Unlock()
		return tmux.SubprocessRunner().Run(ctx, request)
	})
	server, err := tmux.NewServer(tmux.ServerOptions{
		SocketPath: filepath.Join(t.TempDir(), "tmux.sock"),
		Runner:     runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireTerminalConnection(t, server)
	t.Cleanup(func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer killCancel()
		_ = server.Kill(killCtx)
	})

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

	mutex.Lock()
	captured := append([][]string(nil), requests...)
	mutex.Unlock()
	for _, arguments := range captured {
		for _, argument := range arguments {
			if argument == "new-session" {
				t.Fatalf("session creation escaped to a subprocess: %q", arguments)
			}
		}
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
	requireTerminalConnection(t, server)

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
	requireTerminalConnection(t, server)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
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
	requireTerminalConnection(t, server)

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one", len(sessions), err)
	}
	attached := sessions[0]
	survivor, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "survivor"})
	if err != nil {
		t.Fatal(err)
	}
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
	requireTerminalConnection(t, server)
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
	requireTerminalConnection(t, server)

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
	requireTerminalConnection(t, server)

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
