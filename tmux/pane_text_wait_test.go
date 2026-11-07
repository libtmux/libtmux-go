package tmux

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/internal/tmuxcmd"
)

func TestPaneEndedErrorMatchesItsSentinels(t *testing.T) {
	cause := &CommandError{Subcommand: "capture-pane", targetNotFound: true}
	for _, testCase := range []struct {
		name    string
		err     *PaneEndedError
		is      []error
		isNot   []error
		unwraps error
	}{
		{
			name:  "exited",
			err:   &PaneEndedError{Reason: PaneExited},
			is:    []error{ErrPaneEnded},
			isNot: []error{ErrNotFound, ErrNoServer},
		},
		{
			name:    "gone with a failed read",
			err:     &PaneEndedError{Reason: PaneGone, cause: cause},
			is:      []error{ErrPaneEnded, ErrNotFound, ErrCommand},
			isNot:   []error{ErrNoServer},
			unwraps: cause,
		},
		{
			name:  "gone from the report",
			err:   &PaneEndedError{Reason: PaneGone},
			is:    []error{ErrPaneEnded, ErrNotFound},
			isNot: []error{ErrNoServer},
		},
		{
			name:  "server gone",
			err:   &PaneEndedError{Reason: ServerGone},
			is:    []error{ErrPaneEnded, ErrNoServer},
			isNot: []error{ErrNotFound},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, target := range testCase.is {
				if !errors.Is(testCase.err, target) {
					t.Errorf("errors.Is(%v, %v) = false, want true", testCase.err, target)
				}
			}
			for _, target := range testCase.isNot {
				if errors.Is(testCase.err, target) {
					t.Errorf("errors.Is(%v, %v) = true, want false", testCase.err, target)
				}
			}
			if testCase.unwraps != nil && !errors.Is(testCase.err, testCase.unwraps) {
				t.Errorf("error does not unwrap to the failed read")
			}
		})
	}
}

func TestTrimWaitScreenDropsLineEndSpacesAndTrailingBlankLines(t *testing.T) {
	got := trimWaitScreen("one  \n  two \n\n \n")
	if want := "one\n  two"; got != want {
		t.Fatalf("trimWaitScreen() = %q, want %q", got, want)
	}
}

// pollRunner answers the wait's poll with a fixed reply and the liveness
// check with another, and counts the checks.
type pollRunner struct {
	poll     tmuxcmd.Result
	liveness tmuxcmd.Result
	checks   int
}

func (r *pollRunner) Run(_ context.Context, request tmuxcmd.Request) (tmuxcmd.Result, error) {
	if slices.Contains(request.Arguments, "list-sessions") {
		r.checks++
		return r.liveness, nil
	}
	return r.poll, nil
}

// A poll that meets kill-server can answer with exit 0 and no marker report,
// with or without "no current target". Only asking the server tells a dead
// one from a live one that answered oddly.
func TestReadScreenAsksTheServerAboutAnUnclassifiedReply(t *testing.T) {
	t.Parallel()

	gone := tmuxcmd.Result{ExitCode: 1, Stderr: []string{"no server running on /tmp/s"}}
	alive := tmuxcmd.Result{Stdout: []string{"0: 1 windows"}, RawStdout: []byte("0: 1 windows\n")}
	for _, reply := range []struct {
		name string
		poll tmuxcmd.Result
	}{
		{name: "empty", poll: tmuxcmd.Result{}},
		{name: "no current target", poll: tmuxcmd.Result{Stderr: []string{"no current target"}}},
		{name: "something else", poll: tmuxcmd.Result{ExitCode: 1, Stderr: []string{"unexpected"}}},
	} {
		t.Run(reply.name+"/server gone", func(t *testing.T) {
			t.Parallel()

			runner := &pollRunner{poll: reply.poll, liveness: gone}
			pane := Pane{server: serverWithRunner(runner), paneID: "%0"}
			_, err := pane.readScreen(context.Background())
			var ended *PaneEndedError
			if !errors.As(err, &ended) || ended.Reason != ServerGone || !errors.Is(err, ErrNoServer) {
				t.Fatalf("readScreen() error = %v, want PaneEndedError ServerGone matching ErrNoServer", err)
			}
			if runner.checks == 0 {
				t.Fatal("the server was never asked whether it is alive")
			}
		})
		t.Run(reply.name+"/server alive", func(t *testing.T) {
			t.Parallel()

			runner := &pollRunner{poll: reply.poll, liveness: alive}
			pane := Pane{server: serverWithRunner(runner), paneID: "%0"}
			_, err := pane.readScreen(context.Background())
			var ended *PaneEndedError
			if err == nil || errors.As(err, &ended) || errors.Is(err, ErrNoServer) {
				t.Fatalf("readScreen() error = %v, want the unclassified-reply error", err)
			}
			if runner.checks != 1 {
				t.Fatalf("liveness checks = %d, want 1", runner.checks)
			}
		})
	}
}
