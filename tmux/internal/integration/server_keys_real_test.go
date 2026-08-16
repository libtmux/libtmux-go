//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// libtmux:parity libtmux.server.Server.bind_key
// libtmux:parity libtmux.server.Server.bind_key#parameter-branch:key_table:d53f04d1b8ed
// libtmux:parity libtmux.server.Server.bind_key#parameter-branch:note:f3fa07adb3fd
// libtmux:parity libtmux.server.Server.bind_key#parameter-branch:repeat:f439cc0551df
// libtmux:parity libtmux.server.Server.list_keys
// libtmux:parity libtmux.server.Server.list_keys#parameter-branch:format_:b99004c71fa4
// libtmux:parity libtmux.server.Server.list_keys#parameter-branch:key_table:d53f04d1b8ed
// libtmux:parity libtmux.server.Server.list_keys#version-branch:tmux-version:157b9dba160f
// libtmux:parity libtmux.server.Server.list_keys#warning:afabcd354447
// libtmux:parity libtmux.server.Server.unbind_key
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:all_keys:74326c3756f4
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:key:c85749129f8e
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:key_table:d53f04d1b8ed
// libtmux:parity libtmux.server.Server.unbind_key#parameter-branch:quiet:8573bc8befe4
//
//libtmux:real-tmux
func TestBindListAndUnbindKeyAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	keyTable := "root"
	note := "libtmux-go-phase6"
	command := "display-message -p phase6-bound"
	if err := server.BindKey(ctx, tmux.BindKeyRequest{
		Key:      "F12",
		Command:  command,
		KeyTable: &keyTable,
		Note:     &note,
		Repeat:   true,
	}); err != nil {
		t.Fatalf("BindKey() error = %v", err)
	}

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version37, err := tmux.ParseVersion("3.7")
	if err != nil {
		t.Fatal(err)
	}
	// A format needs tmux 3.7. Below it the listing is refused rather than
	// returned in tmux's own shape, which is not the shape the caller asked
	// for and which nothing in the result would distinguish.
	format := "#{key_string}\t#{key_note}\t#{key_repeat}\t#{key_command}"
	formatted := tmux.ListKeysRequest{KeyTable: &keyTable, Format: &format}
	if !version.AtLeast(version37) {
		if _, err := server.ListKeys(ctx, formatted); !errors.Is(err, tmux.ErrVersionTooLow) {
			t.Fatalf("ListKeys(Format) on tmux %s error = %v, want ErrVersionTooLow", version, err)
		}
		lines, err := server.ListKeys(ctx, tmux.ListKeysRequest{KeyTable: &keyTable})
		if err != nil {
			t.Fatalf("ListKeys() error = %v", err)
		}
		if !containsAllLine(lines, "F12", "phase6-bound") {
			t.Fatalf("ListKeys() = %#v, want legacy F12 binding", lines)
		}
	} else {
		lines, err := server.ListKeys(ctx, formatted)
		if err != nil {
			t.Fatalf("ListKeys() error = %v", err)
		}
		want := "F12\tlibtmux-go-phase6\t1\t" + command
		if !slices.Contains(lines, want) {
			t.Fatalf("ListKeys() = %#v, want %q", lines, want)
		}
	}

	key := "F12"
	if err := server.UnbindKey(ctx, tmux.UnbindKeyRequest{
		Key:      &key,
		KeyTable: &keyTable,
	}); err != nil {
		t.Fatalf("UnbindKey() error = %v", err)
	}
	lines, err := server.ListKeys(ctx, tmux.ListKeysRequest{KeyTable: &keyTable})
	if err != nil {
		t.Fatalf("ListKeys() after unbind error = %v", err)
	}
	if containsAllLine(lines, "F12", "phase6-bound") {
		t.Fatalf("ListKeys() after unbind = %#v, binding remains", lines)
	}

	allTable := "phase6-all-bindings"
	for _, allKey := range []string{"F10", "F11"} {
		if err := server.BindKey(ctx, tmux.BindKeyRequest{
			Key:      allKey,
			Command:  command,
			KeyTable: &allTable,
		}); err != nil {
			t.Fatalf("BindKey(%s) error = %v", allKey, err)
		}
	}
	lines, err = server.ListKeys(ctx, tmux.ListKeysRequest{KeyTable: &allTable})
	if err != nil || !containsAllLine(lines, "F10", "phase6-bound") ||
		!containsAllLine(lines, "F11", "phase6-bound") {
		t.Fatalf("ListKeys(all table) = (%#v, %v), want both bindings", lines, err)
	}
	if err := server.UnbindKey(ctx, tmux.UnbindKeyRequest{
		KeyTable: &allTable,
		AllKeys:  true,
		Quiet:    true,
	}); err != nil {
		t.Fatalf("UnbindKey(all) error = %v", err)
	}
	// tmux drops a key table once its last binding goes, and then reports the
	// table as missing. That message is the same one a misspelled table name
	// produces, so it is returned rather than read as a table holding no
	// bindings: only the caller knows which of the two it meant.
	lines, err = server.ListKeys(ctx, tmux.ListKeysRequest{KeyTable: &allTable})
	if !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("ListKeys(all table) after unbind error = %v, want ErrCommand", err)
	}
	if lines != nil {
		t.Fatalf("ListKeys(all table) after unbind = %#v, want no rows beside an error", lines)
	}
	if err := server.UnbindKey(ctx, tmux.UnbindKeyRequest{
		KeyTable: &allTable,
		AllKeys:  true,
		Quiet:    true,
	}); err != nil {
		t.Fatalf("UnbindKey(all missing quiet table) error = %v", err)
	}
}

//libtmux:real-tmux
func TestListKeysSingleBindingCompatibilityAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	keyTable := "phase6-single-binding"
	command := "display-message -p phase6-single"
	if err := server.BindKey(ctx, tmux.BindKeyRequest{
		Key: "F11", Command: command, KeyTable: &keyTable,
	}); err != nil {
		t.Fatalf("BindKey() error = %v", err)
	}
	lines, err := server.ListKeys(ctx, tmux.ListKeysRequest{KeyTable: &keyTable})
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	switch version.String() {
	case "3.7", "3.7a", "3.7b":
		if lines == nil || len(lines) != 0 {
			t.Fatalf("ListKeys() on tmux %s = %#v, want nonnil compatibility empty", version, lines)
		}
	default:
		if !containsAllLine(lines, "F11", "phase6-single") {
			t.Fatalf("ListKeys() on tmux %s = %#v, want single binding", version, lines)
		}
	}
}

//libtmux:real-tmux
func TestBindEmptyNoOpCommandAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	version33, err := tmux.ParseVersion("3.3")
	if err != nil {
		t.Fatal(err)
	}
	keyTable := "phase6-no-op"
	err = server.BindKey(ctx, tmux.BindKeyRequest{Key: "F10", KeyTable: &keyTable})
	if !version.AtLeast(version33) {
		if err == nil {
			t.Fatalf("BindKey(empty command) on tmux %s error = nil, want upstream failure", version)
		}
		return
	}
	if err != nil {
		t.Fatalf("BindKey(empty command) on tmux %s error = %v", version, err)
	}
	lines, err := server.ListKeys(ctx, tmux.ListKeysRequest{})
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	if !containsAllLine(lines, keyTable, "F10") {
		t.Fatalf("ListKeys() = %#v, want no-op binding", lines)
	}
}

// libtmux:parity libtmux.server.Server.list_clients
// libtmux:parity libtmux.server.Server.list_commands
// libtmux:parity libtmux.server.Server.list_commands#parameter-branch:command_name:a5ecee67e253
// libtmux:parity libtmux.server.Server.show_messages
// libtmux:parity libtmux.server.Server.show_messages#parameter-branch:jobs:674b7b889698
// libtmux:parity libtmux.server.Server.show_messages#parameter-branch:target_client:9bd26a6f1edf
// libtmux:parity libtmux.server.Server.show_messages#parameter-branch:terminals:d731ae9a0080
//
//libtmux:real-tmux
func TestServerListingsAndMessagesAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	commandName := "send-keys"
	commands, err := server.ListCommands(
		ctx,
		tmux.ListCommandsRequest{CommandName: &commandName},
	)
	if err != nil {
		t.Fatalf("ListCommands() error = %v", err)
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "send-keys") {
		t.Fatalf("ListCommands(send-keys) = %#v, want one matching command", commands)
	}

	session := onlyServerKeySession(ctx, t, server)
	control := tmuxtest.NewControlMode(ctx, t, server, session)
	clients, err := server.ListClients(ctx)
	if err != nil {
		t.Fatalf("ListClients() error = %v", err)
	}
	if !containsAllLine(clients, control.ClientName().String()) {
		t.Fatalf("ListClients() = %#v, want %q", clients, control.ClientName())
	}

	targetClient := control.ClientName()
	if _, err := server.ShowMessages(ctx, tmux.ShowMessagesRequest{
		TargetClient: targetClient,
	}); err != nil {
		t.Fatalf("ShowMessages(target client) error = %v", err)
	}
	runRealServerCommand(ctx, t, server, "run-shell", "-b", "sleep 30")
	if _, err := server.ShowMessages(ctx, tmux.ShowMessagesRequest{
		TargetClient: targetClient,
		Terminals:    true,
	}); err != nil {
		t.Fatalf("ShowMessages(terminals) error = %v", err)
	}
	summary, err := server.ShowMessages(ctx, tmux.ShowMessagesRequest{
		TargetClient: targetClient,
		Terminals:    true,
		Jobs:         true,
	})
	if err != nil {
		t.Fatalf("ShowMessages(terminals and jobs) error = %v", err)
	}
	if !containsPrefixLine(summary, "Job ") {
		t.Fatalf("ShowMessages(terminals and jobs) = %#v, want job summary", summary)
	}
}

func onlyServerKeySession(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
) tmux.Session {
	t.Helper()
	sessions, err := server.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len(Sessions()) = %d, want one", len(sessions))
	}
	return sessions[0]
}

func runRealServerCommand(
	ctx context.Context,
	t *testing.T,
	server tmux.Server,
	arguments ...string,
) {
	t.Helper()
	result, err := server.Cmd(ctx, arguments...)
	if err != nil {
		t.Fatalf("tmux %v error = %v", arguments, err)
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("tmux %v result = %#v, want success", arguments, result)
	}
}

func containsAllLine(lines []string, fragments ...string) bool {
	for _, line := range lines {
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(line, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func containsPrefixLine(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
