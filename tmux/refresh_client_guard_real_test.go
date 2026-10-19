package tmux_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// tmux 3.3a exits the server when refresh-client -l reaches a client that has a
// terminal. Literal refresh-client with -l is refused there before it is sent,
// in every spelling tmux resolves to the command and inside a command list, and
// the attached client and the server survive.
//
//libtmux:real-tmux
func TestRawRefreshClientClipboardQueryIsRefusedWhereItKillsTheServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server := tmuxtest.NewServerWithOptions(ctx, t, tmuxtest.ServerOptions{})
	version, err := server.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version.String() != "3.3a" {
		t.Skipf("refresh-client -l only kills tmux 3.3a, not %s", version)
	}
	if _, err := server.NewSession(ctx, tmux.NewSessionRequest{Name: "anchor"}); err != nil {
		t.Fatal(err)
	}

	environment := []string{"TERM=xterm-256color"}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TMUX" && key != "TMUX_PANE" && key != "TERM" {
			environment = append(environment, entry)
		}
	}
	args := []string{"-S", server.SocketPath(), "-f", server.ConfigFile(), "attach-session", "-t", "anchor"}
	process := tmuxtest.StartPTYProcess(ctx, t, server.Executable(), args, environment)
	var client string
	if err := tmuxtest.WaitFor(ctx, 10*time.Millisecond, func(ctx context.Context) (bool, error) {
		select {
		case <-process.Done():
			t.Fatalf("client exited before registration: %q", process.Output())
		default:
		}
		result, err := server.Cmd(ctx, "list-clients", "-F", "#{client_name}")
		if err != nil || len(result.Stdout) != 1 {
			return false, err
		}
		client = result.Stdout[0]
		return true, nil
	}); err != nil {
		t.Fatalf("the client never attached: %v", err)
	}

	// Every spelling tmux resolves to refresh-client: its alias and each prefix
	// no other command shares, read from the running tmux rather than assumed.
	listed, err := server.Cmd(ctx, "list-commands", "-F", "#{command_list_name} #{command_list_alias}")
	if err != nil || listed.ExitCode != 0 {
		t.Fatalf("list-commands = (%+v, %v)", listed, err)
	}
	var names, aliases []string
	for _, line := range listed.Stdout {
		name, alias, _ := strings.Cut(line, " ")
		names = append(names, name)
		aliases = append(aliases, alias)
	}
	var spellings, others []string
	for length := 1; length <= len("refresh-client"); length++ {
		prefix := "refresh-client"[:length]
		matches := 0
		for _, name := range names {
			if strings.HasPrefix(name, prefix) {
				matches++
			}
		}
		if slices.Contains(aliases, prefix) && prefix != "refresh" {
			continue
		}
		if matches == 1 || prefix == "refresh" {
			spellings = append(spellings, prefix)
		} else {
			others = append(others, prefix)
		}
	}
	if !slices.Contains(spellings, "refresh") || !slices.Contains(spellings, "ref") || len(others) == 0 {
		t.Fatalf("spellings = %q, ambiguous = %q, want refresh and ref resolvable and a shorter prefix ambiguous", spellings, others)
	}

	alive := func(label string) {
		t.Helper()
		if result, err := server.Cmd(ctx, "list-clients", "-F", "#{client_name}"); err != nil ||
			result.ExitCode != 0 || !slices.Equal(result.Stdout, []string{client}) {
			t.Fatalf("%s: the server or its client is gone: (%+v, %v)", label, result, err)
		}
	}
	for _, verb := range spellings {
		for _, operands := range [][]string{
			{"-l", "-t", client},
			{"-t", client, "-l"},
			{"-lt", client},
			{"-l", "-t" + client},
			{"-Sl", "-t", client},
		} {
			command := append([]string{verb}, operands...)
			if _, err := server.Cmd(ctx, command...); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Errorf("%q = %v, want a refusal", command, err)
			}
			alive("alone")
			list := append([]string{"list-windows", ";"}, command...)
			if _, err := server.Cmd(ctx, list...); !errors.Is(err, tmux.ErrVersionTooLow) {
				t.Errorf("%q = %v, want a refusal", list, err)
			}
			alive("in a list")
		}
	}
	// A prefix tmux itself rejects as ambiguous is not this command, and forms
	// without -l are sent.
	for _, verb := range others {
		_, err := server.Cmd(ctx, verb, "-l", "-t", client)
		if errors.Is(err, tmux.ErrVersionTooLow) {
			t.Errorf("%q is ambiguous in tmux and must not be taken for refresh-client", verb)
		}
		alive("ambiguous prefix")
	}
	for _, command := range [][]string{
		{"refresh-client", "-t", client},
		{"refresh", "-S", "-t", client},
		{"refresh-client", "-t", client, "-f", "l"},
	} {
		if result, err := server.Cmd(ctx, command...); err != nil || result.ExitCode != 0 {
			t.Errorf("%q = (%+v, %v), want it sent and accepted", command, result, err)
		}
	}
	alive("supported forms")

	// The typed request withholds the flag below 3.4 instead of sending it.
	_ = server.RefreshClient(ctx, tmux.RefreshClientRequest{
		TargetClient: tmux.ClientName(client), RequestClipboard: true,
	})
	alive("typed request")
}
