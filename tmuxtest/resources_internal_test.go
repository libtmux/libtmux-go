package tmuxtest

import (
	"errors"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go"
)

func TestTemporaryResourceCleanupFailurePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		kind      string
		result    tmux.CommandResult
		wantError bool
	}{
		{
			name:   "success",
			kind:   "session",
			result: tmux.CommandResult{ExitCode: 0},
		},
		{
			name: "missing resource",
			kind: "window",
			result: tmux.CommandResult{
				ExitCode: 1,
				Stderr:   []string{"can't find window: @9"},
			},
		},
		{
			name: "missing server",
			kind: "session",
			result: tmux.CommandResult{
				ExitCode: 1,
				Stderr:   []string{"no server running on /tmp/example"},
			},
		},
		{
			name: "stderr on success",
			kind: "window",
			result: tmux.CommandResult{
				ExitCode: 0,
				Stderr:   []string{"unexpected diagnostic"},
			},
			wantError: true,
		},
		{
			name: "other nonzero exit",
			kind: "session",
			result: tmux.CommandResult{
				ExitCode: 2,
				Stderr:   []string{"permission denied"},
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := temporaryResourceCleanupFailure(test.kind, test.result)
			if !test.wantError && err != nil {
				t.Fatalf("temporaryResourceCleanupFailure() error = %v, want nil", err)
			}
			if test.wantError && err == nil {
				t.Fatal("temporaryResourceCleanupFailure() error = nil, want failure")
			}
			if test.wantError && !errors.Is(err, tmux.ErrCommand) {
				t.Fatalf("temporaryResourceCleanupFailure() errors.Is(ErrCommand) = false: %v", err)
			}
			for _, diagnostic := range test.result.Stderr {
				if err != nil && strings.Contains(err.Error(), diagnostic) {
					t.Fatalf("temporaryResourceCleanupFailure() exposed stderr: %v", err)
				}
			}
		})
	}
}
