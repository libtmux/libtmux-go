package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// TestKillSessionAddressesADottedName: killSession used to anchor its target
// with tmux's "=" exact-match prefix, but tmux applies that prefix to the
// session part left after splitting the target on a period or colon, so
// "=my.proj" fails on exactly the name it looks like it should protect. A
// session carrying such a name only ever exists verbatim from tmux 3.7a; 3.7
// itself refuses to create one, and everything before it rewrites the period
// to an underscore. [tmux.Version.AtLeast] compares feature level, treating
// 3.7 and 3.7a alike, so it cannot gate this -- the actual creation result
// does, and the test skips on either outcome rather than assuming one.
//
//libtmux:real-tmux
func TestKillSessionAddressesADottedName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	target := tmuxtest.NewServer(ctx, t)

	result, err := target.Cmd(ctx, "new-session", "-d", "-s", "victim.name")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Skipf("this tmux refuses a dotted session name: %+v", result)
	}
	rows, err := target.Cmd(ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(rows.Stdout, "\n"), "victim.name") {
		t.Skipf("this tmux did not keep the dotted name verbatim: %q", rows.Stdout)
	}

	registry := &tools{runtime: newRuntime(ctx, target)}
	_, output, err := registry.killSession(ctx, nil, killSessionInput{
		SessionName: "victim.name", ConfirmSelf: true,
	})
	if err != nil || output.Killed != "victim.name" {
		t.Fatalf("killSession(%q) = (%+v, %v), want the session killed", "victim.name", output, err)
	}

	rows, err = target.Cmd(ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rows.Stdout, "\n"), "victim.name") {
		t.Fatalf("victim.name still lists as a session after killSession: %q", rows.Stdout)
	}
}
