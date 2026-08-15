//go:build linux

package tmux_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tmux-python/libtmux/golang"
	"github.com/tmux-python/libtmux/golang/tmuxtest"
)

// libtmux:parity libtmux.server.Server.attach_session
// libtmux:parity libtmux.session.Session.attach
// libtmux:parity libtmux.session.Session.attach#parameter-branch:exit_:ec1dc0f87e55
// libtmux:parity libtmux.session.Session.attach#parameter-branch:flags_:68f4c159cd01
//
//libtmux:real-tmux
func TestAttachSessionAgainstRealTmuxPTY(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	probe, err := server.Cmd(ctx, "display-message", "-p", "#{pid}")
	if err != nil || probe.ExitCode != 0 || len(probe.Command) == 0 {
		t.Fatalf("resolve tmux binary = (%#v, %v)", probe, err)
	}

	tests := []struct {
		name     string
		mode     string
		explicit bool
	}{
		{name: "server pattern and explicit files", mode: "server", explicit: true},
		{name: "session stable ID and inherited files", mode: "session"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			startDirectory := t.TempDir()
			environment := attachHelperEnvironment(map[string]string{
				"LIBTMUX_ATTACH_HELPER":  "1",
				"LIBTMUX_ATTACH_BINARY":  probe.Command[0],
				"LIBTMUX_ATTACH_SOCKET":  server.SocketPath(),
				"LIBTMUX_ATTACH_CONFIG":  server.ConfigFile(),
				"LIBTMUX_ATTACH_SESSION": sessions[0].ID().String(),
				"LIBTMUX_ATTACH_MODE":    test.mode,
				"LIBTMUX_ATTACH_CWD":     startDirectory,
			})
			if test.explicit {
				environment = append(environment, "LIBTMUX_ATTACH_EXPLICIT=1")
			}
			process := tmuxtest.StartPTYProcess(
				ctx,
				t,
				os.Args[0],
				[]string{"-test.run=^TestAttachSessionRealHelper$"},
				environment,
			)
			waitForAttachClient(ctx, t, server, process, 1)

			if test.mode == "server" {
				result, err := server.Cmd(
					ctx, "display-message", "-p", "-t",
					sessions[0].ID().String(), "#{session_path}",
				)
				if err != nil || result.ExitCode != 0 ||
					!slices.Equal(result.Stdout, []string{startDirectory}) {
					t.Fatalf("attached session path = (%#v, %v), want %q", result, err, startDirectory)
				}
				result, err = server.Cmd(ctx, "list-clients", "-F", "#{client_flags}")
				if err != nil || result.ExitCode != 0 ||
					len(result.Stdout) != 1 || !strings.Contains(result.Stdout[0], "ignore-size") {
					t.Fatalf("attached client flags = (%#v, %v), want ignore-size", result, err)
				}
			}

			if _, err := process.Write(ctx, []byte{2, 'd'}); err != nil {
				t.Fatalf("write detach key: %v", err)
			}
			if err := process.Wait(ctx); err != nil {
				t.Fatalf("attach helper error = %v; output %q", err, process.Output())
			}
			waitForAttachClient(ctx, t, server, process, 0)
		})
	}
}

//libtmux:real-tmux
func TestAttachSessionCancellationAgainstRealTmuxPTY(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%#v, %v), want one session", sessions, err)
	}
	probe, err := server.Cmd(ctx, "display-message", "-p", "#{pid}")
	if err != nil || probe.ExitCode != 0 || len(probe.Command) == 0 {
		t.Fatalf("resolve tmux binary = (%#v, %v)", probe, err)
	}
	process := tmuxtest.StartPTYProcess(
		ctx,
		t,
		os.Args[0],
		[]string{"-test.run=^TestAttachSessionRealHelper$"},
		attachHelperEnvironment(map[string]string{
			"LIBTMUX_ATTACH_HELPER":  "1",
			"LIBTMUX_ATTACH_BINARY":  probe.Command[0],
			"LIBTMUX_ATTACH_SOCKET":  server.SocketPath(),
			"LIBTMUX_ATTACH_CONFIG":  server.ConfigFile(),
			"LIBTMUX_ATTACH_SESSION": sessions[0].ID().String(),
			"LIBTMUX_ATTACH_MODE":    "cancel",
		}),
	)
	waitForAttachClient(ctx, t, server, process, 1)
	if err := process.Wait(ctx); err != nil {
		t.Fatalf("cancellation helper error = %v; output %q", err, process.Output())
	}
	waitForAttachClient(ctx, t, server, process, 0)
}

func TestAttachSessionRealHelper(t *testing.T) {
	if os.Getenv("LIBTMUX_ATTACH_HELPER") != "1" {
		return
	}
	server := tmux.NewServer(tmux.ServerOptions{
		Binary:     os.Getenv("LIBTMUX_ATTACH_BINARY"),
		SocketPath: os.Getenv("LIBTMUX_ATTACH_SOCKET"),
		ConfigFile: os.Getenv("LIBTMUX_ATTACH_CONFIG"),
	}).WithStrictErrors()
	mode := os.Getenv("LIBTMUX_ATTACH_MODE")
	ctx := context.Background()
	if mode == "cancel" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second)
		defer cancel()
	}
	options := tmux.AttachSessionOptions{NoUpdateEnvironment: true}
	if mode == "server" {
		startDirectory := os.Getenv("LIBTMUX_ATTACH_CWD")
		options.StartDirectory = &startDirectory
		options.ClientFlags = []string{"ignore-size"}
		if os.Getenv("LIBTMUX_ATTACH_EXPLICIT") == "1" {
			options.Stdin = os.Stdin
			options.Stdout = os.Stdout
			options.Stderr = os.Stderr
		}
		err := server.AttachSession(ctx, tmux.AttachSessionRequest{
			Target: "wo*", AttachSessionOptions: options,
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode == "cancel" {
		err := server.AttachSession(ctx, tmux.AttachSessionRequest{Target: "work"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("AttachSession(cancel) error = %v (%#v), want context deadline", err, err)
		}
		return
	}
	sessions, err := server.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := tmux.SessionID(os.Getenv("LIBTMUX_ATTACH_SESSION"))
	for _, session := range sessions {
		if session.ID() == target {
			if err := session.Attach(ctx, options); err != nil {
				t.Fatalf("Session.Attach() error = %v (%#v)", err, err)
			}
			return
		}
	}
	t.Fatalf("session %s is unavailable", target)
}

func waitForAttachClient(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	process *tmuxtest.PTYProcess,
	want int,
) {
	t.Helper()
	for {
		clients, err := server.ListClients(ctx)
		if err == nil && len(clients) == want {
			return
		}
		select {
		case <-process.Done():
			t.Fatalf(
				"attach helper exited before client count %d: %v; output %q",
				want, process.Wait(ctx), process.Output(),
			)
		case <-ctx.Done():
			t.Fatalf("client count did not become %d: %v", want, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func attachHelperEnvironment(additions map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(additions)+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "TERM" || key == "TMUX" || key == "TMUX_PANE" ||
			strings.HasPrefix(key, "LIBTMUX_ATTACH_") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "TERM=xterm-256color")
	for key, value := range additions {
		environment = append(environment, key+"="+value)
	}
	return environment
}
