//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

//libtmux:real-tmux
func TestLiteralArgumentsAndRawSeparatorsAgainstRealTmux(t *testing.T) {
	base := tmuxtest.NewServer(context.Background(), t)
	configuration := filepath.Join(t.TempDir(), "tmux.conf;")
	if err := os.WriteFile(configuration, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(filepath.Dir(base.SocketPath()), ";")
	if err := os.Symlink(base.SocketPath(), socket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove socket alias: %v", err)
		}
	})
	server := tmux.NewServer(tmux.ServerOptions{
		SocketPath: socket,
		ConfigFile: configuration,
	}).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	alive, err := server.IsAlive(ctx)
	if err != nil || !alive {
		result, commandErr := server.Cmd(ctx, "list-sessions")
		t.Fatalf(
			"literal connection IsAlive() = (%t, %v); list-sessions = (%#v, %v)",
			alive,
			err,
			result,
			commandErr,
		)
	}
	session, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "literal-target;"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if name, _ := session.Name(); name != "literal-target;" {
		t.Fatalf("session name = %q, want terminal semicolon", name)
	}

	if err := server.SetEnvironment(
		ctx,
		"LITERAL_SEPARATOR",
		"environment;",
		tmux.SetEnvironmentOptions{},
	); err != nil {
		t.Fatalf("SetEnvironment() error = %v", err)
	}
	value, ok, err := server.GetEnvironment(ctx, "LITERAL_SEPARATOR")
	if err != nil || !ok || value.Value != "environment;" {
		t.Fatalf("GetEnvironment() = (%#v, %t, %v)", value, ok, err)
	}

	globalSession := server.GlobalSessionScope()
	if err := globalSession.SetOption(
		ctx,
		"@literal_option",
		"option;",
		tmux.SetOptionOptions{},
	); err != nil {
		t.Fatalf("GlobalSessionScope.SetOption() error = %v", err)
	}
	option, ok, err := globalSession.RawOption(ctx, "@literal_option")
	if err != nil || !ok || option != "option;" {
		t.Fatalf("GlobalSessionScope.RawOption() = (%q, %t, %v)", option, ok, err)
	}

	if err := server.IfShell(ctx, tmux.IfShellRequest{
		ShellCommand: "true;",
		ThenCommand:  "set-option -g @literal_nested nested;",
	}); err != nil {
		t.Fatalf("IfShell() error = %v", err)
	}
	nested, ok, err := globalSession.RawOption(ctx, "@literal_nested")
	if err != nil || !ok || nested != "nested" {
		t.Fatalf("nested option = (%q, %t, %v)", nested, ok, err)
	}

	result, err := server.Cmd(
		ctx,
		"set-option", "-g", "@raw_first", "one",
		";",
		"set-option", "-g", "@raw_second", "two",
	)
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("raw command list = (%#v, %v)", result, err)
	}
	for name, want := range map[string]string{
		"@raw_first":  "one",
		"@raw_second": "two",
	} {
		got, present, rawErr := globalSession.RawOption(ctx, name)
		if rawErr != nil || !present || got != want {
			t.Fatalf("GlobalSessionScope.RawOption(%q) = (%q, %t, %v)", name, got, present, rawErr)
		}
	}
	if alive, err := base.IsAlive(ctx); err != nil || !alive {
		t.Fatalf("harness server IsAlive() = (%t, %v)", alive, err)
	}
}
