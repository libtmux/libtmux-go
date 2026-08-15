package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/libtmux/libtmux-go/internal/tmuxcmd"
)

// libtmux:parity libtmux.server.Server.clients
// libtmux:parity libtmux.server.Server.panes
// libtmux:parity libtmux.server.Server.sessions
// libtmux:parity libtmux.server.Server.windows
func TestServerHierarchyListsShareLenientAndStrictPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		list func(Server) (int, bool, error)
	}{
		{
			name: "sessions",
			list: func(server Server) (int, bool, error) {
				values, err := server.Sessions(context.Background())
				return len(values), values != nil, err
			},
		},
		{
			name: "windows",
			list: func(server Server) (int, bool, error) {
				values, err := server.Windows(context.Background())
				return len(values), values != nil, err
			},
		},
		{
			name: "panes",
			list: func(server Server) (int, bool, error) {
				values, err := server.Panes(context.Background())
				return len(values), values != nil, err
			},
		},
		{
			name: "clients",
			list: func(server Server) (int, bool, error) {
				values, err := server.Clients(context.Background())
				return len(values), values != nil, err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := &versionQueueRunner{responses: []versionResponse{
				{result: tmuxcmd.Result{Stderr: []string{"no server"}, ExitCode: 1}},
				{result: tmuxcmd.Result{Stderr: []string{"no server"}, ExitCode: 1}},
			}}
			server := serverWithRunner(runner)
			length, nonNil, err := test.list(server)
			if err != nil || !nonNil || length != 0 {
				t.Fatalf("lenient list = (len %d, nonnil %t, %v), want (0, true, nil)", length, nonNil, err)
			}
			_, _, err = test.list(server.WithStrictErrors())
			if !errors.Is(err, ErrCommand) {
				t.Fatalf("strict list error = %v, want ErrCommand", err)
			}
		})
	}
}
