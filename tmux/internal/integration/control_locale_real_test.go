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

// A control client opens in an environment that names no UTF-8 locale.
//
// tmux rewrites control characters in format output for a client it does not
// believe is using UTF-8, and a client is not using UTF-8 when its environment
// names no UTF-8 locale. The registration poll separated its two fields with a
// tab, which came back as an underscore, so no line ever split, no client was
// ever recognised, and the open ran until its context ended. A caller whose
// context has no deadline waited forever.
//
// That is the environment a program is commonly started in by something other
// than a shell: an MCP client spawning a server, a cron entry, a service
// manager. A shell almost always sets LANG, which is why nothing caught this.
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
