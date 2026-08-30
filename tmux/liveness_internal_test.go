package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

type livenessResultRunner struct {
	stderr string
}

func (runner livenessResultRunner) Run(
	context.Context,
	tmuxcmd.Request,
) (tmuxcmd.Result, error) {
	return tmuxcmd.Result{Stderr: []string{runner.stderr}, ExitCode: 1}, nil
}

// Server shutdown races between connect failure and lost-connection output.
// ErrNoServer must classify both so liveness checks are timing-independent.
func TestErrNoServerCoversEveryWayTmuxSaysItIsGone(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		stderr string
		want   bool
	}{
		{name: "connection refused", stderr: "no server running on /tmp/s", want: true},
		{name: "socket absent", stderr: "error connecting to /tmp/s (No such file or directory)", want: true},
		{name: "socket unreadable", stderr: "error connecting to /tmp/s (Permission denied)", want: true},
		{name: "socket uncreatable", stderr: "error creating /tmp/s (Permission denied)", want: true},
		{name: "connection lost", stderr: "server exited unexpectedly", want: true},
		{name: "server shut down", stderr: "server exited", want: true},
		{name: "a missing target", stderr: "can't find window: @99", want: false},
		{name: "a client detaching", stderr: "detached (from session $0)", want: false},
		{name: "a lost terminal", stderr: "lost tty", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := serverWithRunner(livenessResultRunner{stderr: test.stderr})
			_, err := server.Sessions(context.Background())
			if err == nil {
				t.Fatal("Sessions() error = nil, want a reported failure")
			}
			if got := errors.Is(err, ErrNoServer); got != test.want {
				t.Fatalf("errors.Is(%q, ErrNoServer) = %t, want %t", test.stderr, got, test.want)
			}
		})
	}
}
