//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"os/user"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.server.Server.server_access
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:allow:f3969308d5bd
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:deny:2eb790bb3e9a
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:list_access:f6e9907c043a
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:list_access:f6e9907c043a:2
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:read_only,write:14227e701e2d
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:read_only:e91b42a77c5e
// libtmux:parity libtmux.server.Server.server_access#parameter-branch:write:fe54a3f721d4
// libtmux:parity libtmux.server.Server.server_access#version-branch:tmux-version:d9801479e597
//
//libtmux:real-tmux
func TestServerAccessListAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	minimum, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := server.ServerAccess(ctx, tmux.ServerAccessRequest{List: true})
	if !version.AtLeast(minimum) {
		if !errors.Is(err, tmux.ErrVersionTooLow) || lines != nil {
			t.Fatalf("ServerAccess(list) = (%#v, %v), want tmux 3.3 gate", lines, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("ServerAccess(list) error = %v", err)
	}
	if len(lines) == 0 {
		t.Fatalf("ServerAccess(list) = %#v, want owner ACL entry", lines)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("ServerAccess(list) contains empty entry: %#v", lines)
		}
	}

	leadingDash := "-definitely-not-a-user"
	if _, err := server.ServerAccess(ctx, tmux.ServerAccessRequest{Allow: &leadingDash}); !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("ServerAccess(leading dash user) error = %v, want command failure", err)
	}

	other, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("lookup unprivileged user: %v", err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if other.Username == current.Username {
		t.Skip("nobody is the tmux server owner")
	}
	if _, err := server.ServerAccess(ctx, tmux.ServerAccessRequest{
		Allow:    &other.Username,
		ReadOnly: true,
	}); err != nil {
		t.Fatalf("ServerAccess(allow read-only) error = %v", err)
	}
	lines, err = server.ServerAccess(ctx, tmux.ServerAccessRequest{List: true})
	if err != nil {
		t.Fatalf("ServerAccess(list after allow) error = %v", err)
	}
	wantAccess := other.Username + " (R)"
	currentMinimum, parseErr := tmux.ParseVersion("3.8")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if version.AtLeast(currentMinimum) {
		wantAccess = other.Username + " (U,R)"
	}
	if !slices.Contains(lines, wantAccess) {
		t.Fatalf("ServerAccess(list after allow) = %#v, want %q", lines, wantAccess)
	}
	if _, err := server.ServerAccess(ctx, tmux.ServerAccessRequest{Deny: &other.Username}); err != nil {
		t.Fatalf("ServerAccess(deny) error = %v", err)
	}
}

// libtmux:parity libtmux.server.Server.refresh_client
// libtmux:parity libtmux.server.Server.refresh_client#parameter-branch:request_clipboard:f7531c059ac6
// libtmux:parity libtmux.server.Server.refresh_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.refresh_client#version-branch:tmux-version:157b9dba160f
// libtmux:parity libtmux.server.Server.refresh_client#warning:1fa7f6b92788
// libtmux:parity libtmux.server.Server.switch_client
// libtmux:parity libtmux.session.Session.switch_client
//
//libtmux:real-tmux
func TestRefreshAndSwitchClientsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	work := clientAdminSessionNamed(ctx, t, server, "work")
	beta, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "client-beta"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	control := tmuxtest.NewControlMode(context.Background(), t, server, work)
	target := control.ClientName()

	// RequestClipboard needs tmux 3.4. Below it the refresh is refused rather
	// than run without the flag, so the rest of this test refreshes with a
	// request this tmux can carry.
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	clipboardVersion, err := tmux.ParseVersion("3.4")
	if err != nil {
		t.Fatal(err)
	}
	refresh := tmux.RefreshClientRequest{
		TargetClient:     target,
		RequestClipboard: true,
	}
	if !version.AtLeast(clipboardVersion) {
		if err := server.RefreshClient(ctx, refresh); !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("RefreshClient(RequestClipboard) on tmux %s error = %v, want ErrVersionTooLow", version, err)
		}
		refresh.RequestClipboard = false
	}
	if err := server.RefreshClient(ctx, refresh); err != nil {
		t.Fatalf("RefreshClient() error = %v", err)
	}
	// The refusal below 3.4 exists because the flag ends the server there, so
	// the server outliving the request is the whole of what is being checked.
	if alive, err := server.IsAlive(ctx); err != nil || !alive {
		t.Fatalf("IsAlive() = (%t, %v) after a clipboard refresh, want the server still up",
			alive, err)
	}

	if err := server.SwitchClient(ctx, "client-beta"); err != nil {
		t.Fatalf("Server.SwitchClient() error = %v", err)
	}
	waitClientSession(ctx, t, server, target, beta.ID())

	if err := work.SwitchClient(ctx); err != nil {
		t.Fatalf("Session.SwitchClient() error = %v", err)
	}
	waitClientSession(ctx, t, server, target, work.ID())
}

// libtmux:parity libtmux.server.Server.detach_all_clients
// libtmux:parity libtmux.server.Server.detach_all_clients#parameter-branch:keep_client:169ea5ceb8fe
// libtmux:parity libtmux.server.Server.detach_all_clients#parameter-branch:shell_command:bd963b0a10bc
// libtmux:parity libtmux.server.Server.detach_client
// libtmux:parity libtmux.server.Server.detach_client#parameter-branch:shell_command:bd963b0a10bc
// libtmux:parity libtmux.server.Server.detach_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.lock_client
// libtmux:parity libtmux.server.Server.lock_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.lock_server
// libtmux:parity libtmux.server.Server.suspend_client
// libtmux:parity libtmux.server.Server.suspend_client#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.session.Session.detach_client
// libtmux:parity libtmux.session.Session.detach_client#parameter-branch:shell_command:bd963b0a10bc
// libtmux:parity libtmux.session.Session.lock_session
//
//libtmux:real-tmux
func TestDetachClientsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	work := clientAdminSessionNamed(ctx, t, server, "work")

	keep := tmuxtest.NewControlMode(context.Background(), t, server, work)
	detachOne := tmuxtest.NewControlMode(context.Background(), t, server, work)
	detachTarget := detachOne.ClientName()
	if err := server.DetachClient(ctx, tmux.DetachClientRequest{
		TargetClient: detachTarget,
	}); err != nil {
		t.Fatalf("DetachClient() error = %v", err)
	}
	waitControlExit(ctx, t, detachOne)
	waitClientNames(ctx, t, server, []tmux.ClientName{keep.ClientName()})

	detachOthers := tmuxtest.NewControlMode(context.Background(), t, server, work)
	keepTarget := keep.ClientName()
	if err := server.DetachAllClients(ctx, tmux.DetachAllClientsRequest{
		KeepClient: keepTarget,
	}); err != nil {
		t.Fatalf("DetachAllClients() error = %v", err)
	}
	waitControlExit(ctx, t, detachOthers)
	waitClientNames(ctx, t, server, []tmux.ClientName{keep.ClientName()})

	detachSession := tmuxtest.NewControlMode(context.Background(), t, server, work)
	if err := work.DetachClients(ctx, nil); err != nil {
		t.Fatalf("Session.DetachClients() error = %v", err)
	}
	waitControlExit(ctx, t, keep)
	waitControlExit(ctx, t, detachSession)
	waitClientNames(ctx, t, server, nil)
}

//libtmux:real-tmux
func TestLockAndSuspendOnlyIsolatedControlClients(t *testing.T) {
	tests := []struct {
		name      string
		operation func(context.Context, tmux.Server, tmux.Session, tmux.ClientName) error
	}{
		{
			name: "lock server",
			operation: func(ctx context.Context, server tmux.Server, _ tmux.Session, _ tmux.ClientName) error {
				return server.LockServer(ctx)
			},
		},
		{
			name: "lock client",
			operation: func(ctx context.Context, server tmux.Server, _ tmux.Session, client tmux.ClientName) error {
				return server.LockClient(ctx, &client)
			},
		},
		{
			name: "suspend client",
			operation: func(ctx context.Context, server tmux.Server, _ tmux.Session, client tmux.ClientName) error {
				return server.SuspendClient(ctx, &client)
			},
		},
		{
			name: "lock session",
			operation: func(ctx context.Context, _ tmux.Server, session tmux.Session, _ tmux.ClientName) error {
				return session.Lock(ctx)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := tmuxtest.NewServer(context.Background(), t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			session := clientAdminSessionNamed(ctx, t, server, "work")
			control := tmuxtest.NewControlMode(context.Background(), t, server, session)
			if err := test.operation(ctx, server, session, control.ClientName()); err != nil {
				t.Fatalf("operation error = %v", err)
			}
			if err := control.Close(); err != nil {
				t.Fatalf("close isolated control client: %v", err)
			}
		})
	}
}

func clientAdminSessionNamed(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	name string,
) tmux.Session {
	t.Helper()
	sessions, err := server.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	for _, session := range sessions {
		if sessionName, ok := session.Name(); ok && sessionName == name {
			return session
		}
	}
	t.Fatalf("session %q not found in %#v", name, sessions)
	return tmux.Session{}
}

func waitClientSession(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	client tmux.ClientName,
	want tmux.SessionID,
) {
	t.Helper()
	for {
		result, err := server.Cmd(ctx, "list-clients", "-F", "#{client_name}\t#{session_id}")
		if err != nil {
			t.Fatalf("list client sessions: %v", err)
		}
		if result.ExitCode == 0 {
			for _, line := range result.Stdout {
				name, sessionID, ok := strings.Cut(line, "\t")
				if ok && name == client.String() && sessionID == want.String() {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for client %q on session %q: %v", client, want, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitClientNames(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	want []tmux.ClientName,
) {
	t.Helper()
	wantStrings := make([]string, len(want))
	for index := range want {
		wantStrings[index] = want[index].String()
	}
	slices.Sort(wantStrings)
	for {
		result, err := server.Cmd(ctx, "list-clients", "-F", "#{client_name}")
		if err != nil {
			t.Fatalf("list client names: %v", err)
		}
		got := slices.Clone(result.Stdout)
		slices.Sort(got)
		if result.ExitCode == 0 && slices.Equal(got, wantStrings) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for clients %#v; last result %#v: %v", wantStrings, result, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitControlExit(ctx context.Context, t *testing.T, control *tmuxtest.ControlMode) {
	t.Helper()
	if err := control.Wait(ctx); err != nil {
		t.Fatalf("wait for detached control client: %v", err)
	}
}
