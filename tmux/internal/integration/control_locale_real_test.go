package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// The registration poll needs a printable separator because tmux rewrites
// control characters for clients without a UTF-8 locale.
//
//libtmux:real-tmux
func TestControlClientOpensWithoutAUTF8Locale(t *testing.T) {
	t.Parallel()

	stripped := environmentWithoutALocale(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initial := tmux.NewSessionRequest{Name: "no-locale"}
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		ProcessEnvironment: stripped,
		InitialSession:     &initial,
	})

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one session", len(sessions), err)
	}

	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl without a locale: %v", err)
	}
	defer func() { _ = client.Close() }()

	if client.ClientName() == "" {
		t.Error("the control client opened without learning its own name")
	}
}

// environmentWithoutALocale is everything a tmux process needs except a
// locale, so what is left out is only the thing under test. tmux also treats
// TMUX being set as proof of a UTF-8 terminal, which is why it is not here.
func environmentWithoutALocale(t *testing.T) []string {
	t.Helper()

	stripped := make([]string, 0, 2)
	for _, name := range []string{"PATH", "HOME"} {
		if value, ok := os.LookupEnv(name); ok {
			stripped = append(stripped, name+"="+value)
		}
	}
	for _, variable := range stripped {
		if strings.HasPrefix(variable, "LANG=") || strings.HasPrefix(variable, "LC_") ||
			strings.HasPrefix(variable, "TMUX=") {
			t.Fatalf("the stripped environment still names a locale: %q", variable)
		}
	}
	return stripped
}

// tmux replaces every non-ASCII byte it writes to a command or control client
// whose locale does not name UTF-8, so without asking for UTF-8 a session
// named café reads back as caf_ with no error at all.
//
//libtmux:real-tmux
func TestTextSurvivesAServerWithoutAUTF8Locale(t *testing.T) {
	t.Parallel()

	const name = "café"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initial := tmux.NewSessionRequest{Name: name}
	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{
		ProcessEnvironment: environmentWithoutALocale(t),
		InitialSession:     &initial,
	})

	sessions, err := server.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("Sessions() = (%d, %v), want one session", len(sessions), err)
	}
	if got, ok := sessions[0].Name(); !ok || got != name {
		t.Errorf("Name() = %q, want %q", got, name)
	}

	client, err := server.OpenControl(ctx, sessions[0])
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	result, err := client.Cmd(ctx, "display-message", "-p", "#{session_name}")
	if err != nil {
		t.Fatalf("Cmd() error = %v", err)
	}
	if got := strings.TrimRight(string(result.RawStdout), "\n"); got != name {
		t.Errorf("control display-message = %q, want %q", got, name)
	}
}
