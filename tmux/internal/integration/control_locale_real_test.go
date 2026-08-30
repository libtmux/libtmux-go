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

	// Everything the process needs except a locale, so what is left out is
	// only the thing under test.
	stripped := make([]string, 0, 2)
	for _, name := range []string{"PATH", "HOME"} {
		if value, ok := os.LookupEnv(name); ok {
			stripped = append(stripped, name+"="+value)
		}
	}
	for _, variable := range stripped {
		if strings.HasPrefix(variable, "LANG=") || strings.HasPrefix(variable, "LC_") {
			t.Fatalf("the stripped environment still names a locale: %q", variable)
		}
	}

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
