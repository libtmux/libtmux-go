package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux"
)

func TestRunCommandUsesCallerContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := tmux.NewServer(tmux.ServerOptions{Binary: os.Args[0]})
	result := runCommand(ctx, server, "list-sessions")

	if result.ExitCode != -1 {
		t.Fatalf("runCommand() exit = %d, want -1", result.ExitCode)
	}
	if len(result.Stderr) != 1 || !strings.Contains(result.Stderr[0], context.Canceled.Error()) {
		t.Fatalf("runCommand() stderr = %#v, want context canceled", result.Stderr)
	}
}

func TestCommandFailureRedactsDiagnosticsAndPreservesClassification(t *testing.T) {
	t.Parallel()

	sentinel := "private-command-diagnostic"
	err := commandFailure("display-message", tmux.CommandResult{
		Command:  []string{"tmux", "-S", "/private/socket", sentinel},
		Stdout:   []string{sentinel + "-stdout"},
		Stderr:   []string{sentinel + "-stderr"},
		ExitCode: 37,
	})

	if err == nil {
		t.Fatal("commandFailure() error = nil, want failure")
	}
	if strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "/private/socket") {
		t.Fatalf("commandFailure() exposed diagnostics: %v", err)
	}
	if !errors.Is(err, tmux.ErrCommand) {
		t.Fatalf("commandFailure() errors.Is(ErrCommand) = false: %v", err)
	}
}

func TestCommandFailurePreservesContextClassification(t *testing.T) {
	t.Parallel()

	err := commandFailure("display-message", tmux.CommandResult{
		Stderr:   []string{context.DeadlineExceeded.Error()},
		ExitCode: -1,
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("commandFailure() errors.Is(DeadlineExceeded) = false: %v", err)
	}
	if got := err.Error(); got != "tmuxtest: display-message failed: deadline exceeded" {
		t.Fatalf("commandFailure() = %q, want stable status", got)
	}
}

func TestArtifactCleanupFailureDoesNotExposeOwnedPaths(t *testing.T) {
	t.Parallel()

	sentinel := "private-cleanup-path"
	record := &serverRecord{
		socketPath: filepath.Join(string(filepath.Separator), sentinel, "socket"),
		configFile: filepath.Join(string(filepath.Separator), sentinel, "config"),
		tempDir:    filepath.Join(string(filepath.Separator), sentinel, "other"),
	}
	err := removeServerArtifacts(record)

	if err == nil {
		t.Fatal("removeServerArtifacts() error = nil, want boundary failure")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("removeServerArtifacts() exposed owned path: %v", err)
	}
}

func TestHarnessFailureRetainsOnlyCanonicalSafeClassifications(t *testing.T) {
	t.Parallel()

	pathSentinel := "/private-path-sentinel"
	argvSentinel := "private-argv-sentinel"
	stdoutSentinel := "private-stdout-sentinel"
	stderrSentinel := "private-stderr-sentinel"
	environmentSentinel := "private-environment-sentinel"
	executableSentinel := "private-executable-sentinel"
	forbidden := []string{
		pathSentinel,
		argvSentinel,
		stdoutSentinel,
		stderrSentinel,
		environmentSentinel,
		executableSentinel,
	}
	exitCommand := exec.Command(
		"/bin/sh",
		"-c",
		"printf '%s' \"$FAILURE_STDERR\" >&2; exit 41",
	)
	exitCommand.Env = []string{"FAILURE_STDERR=" + stderrSentinel}
	_, exitCause := exitCommand.Output()
	if exitCause == nil {
		t.Fatal("exit helper error = nil, want process failure")
	}

	tests := []struct {
		name   string
		cause  error
		status string
		wantIs error
	}{
		{
			name:   "canceled",
			cause:  fmt.Errorf("%s: %w", environmentSentinel, context.Canceled),
			status: "canceled",
			wantIs: context.Canceled,
		},
		{
			name:   "deadline exceeded",
			cause:  fmt.Errorf("%s: %w", environmentSentinel, context.DeadlineExceeded),
			status: "deadline exceeded",
			wantIs: context.DeadlineExceeded,
		},
		{
			name:   "executable not found",
			cause:  &exec.Error{Name: executableSentinel, Err: exec.ErrNotFound},
			status: "not found",
			wantIs: exec.ErrNotFound,
		},
		{
			name: "path not found",
			cause: &os.PathError{
				Op:   "open",
				Path: pathSentinel,
				Err:  os.ErrNotExist,
			},
			status: "not found",
			wantIs: os.ErrNotExist,
		},
		{
			name: "permission denied",
			cause: &os.PathError{
				Op:   "remove",
				Path: pathSentinel,
				Err:  os.ErrPermission,
			},
			status: "permission denied",
			wantIs: os.ErrPermission,
		},
		{
			name: "tmux command",
			cause: &tmux.CommandError{
				Subcommand: argvSentinel,
				Result: tmux.CommandResult{
					Command:  []string{argvSentinel},
					Stdout:   []string{stdoutSentinel},
					Stderr:   []string{stderrSentinel},
					ExitCode: 37,
				},
			},
			status: "command failed",
			wantIs: tmux.ErrCommand,
		},
		{
			name:   "invalid state",
			cause:  fmt.Errorf("%s: %w", environmentSentinel, errInvalidHarnessState),
			status: "invalid state",
			wantIs: errInvalidHarnessState,
		},
		{
			name:   "process exited",
			cause:  exitCause,
			status: "process exited",
			wantIs: errHarnessProcessExited,
		},
		{
			name:   "generic",
			cause:  errors.New(environmentSentinel),
			status: "operation error",
			wantIs: errHarnessOperation,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failure := harnessFailure("test operation", test.cause)
			if got := failure.Error(); got != "tmuxtest: test operation failed: "+test.status {
				t.Fatalf("harnessFailure() = %q, want stable status", got)
			}
			joined := errors.Join(
				failure,
				harnessFailure("secondary operation", errInvalidHarnessState),
			)
			if test.wantIs != nil && !errors.Is(joined, test.wantIs) {
				t.Fatalf("harnessFailure() errors.Is(%v) = false", test.wantIs)
			}
			assertSafeReachableErrors(t, joined, forbidden)
		})
	}
}

func assertSafeReachableErrors(t *testing.T, root error, forbidden []string) {
	t.Helper()
	for _, reachable := range reachableErrors(root) {
		exposed := reachable.Error() + "\n" + fmt.Sprintf("%#v", reachable)
		for _, sentinel := range forbidden {
			if strings.Contains(exposed, sentinel) {
				t.Fatalf("reachable %T retained sensitive value %q", reachable, sentinel)
			}
		}
	}

	if pathError, ok := errors.AsType[*os.PathError](root); ok {
		t.Fatalf("reachable error retained *os.PathError: %#v", pathError)
	}
	if executableError, ok := errors.AsType[*exec.Error](root); ok {
		t.Fatalf("reachable error retained *exec.Error: %#v", executableError)
	}
	if exitError, ok := errors.AsType[*exec.ExitError](root); ok {
		t.Fatalf("reachable error retained *exec.ExitError: %#v", exitError)
	}
	if commandError, ok := errors.AsType[*tmux.CommandError](root); ok {
		t.Fatalf("reachable error retained *tmux.CommandError: %#v", commandError)
	}
}

func reachableErrors(root error) []error {
	pending := []error{root}
	reachable := make([]error, 0, 4)
	for len(pending) != 0 {
		current := pending[0]
		pending = pending[1:]
		reachable = append(reachable, current)
		// A direct assertion is required to enumerate errors.Join children.
		if multiple, ok := current.(interface{ Unwrap() []error }); ok {
			pending = append(pending, multiple.Unwrap()...)
			continue
		}
		if next := errors.Unwrap(current); next != nil {
			pending = append(pending, next)
		}
	}
	return reachable
}

func TestCaptureServerOptionsCopiesNestedInitialSession(t *testing.T) {
	width := 80
	height := 24
	environment := map[string]string{"COPY_PROBE": "before"}
	initial := tmux.NewSessionRequest{
		Name:        "before",
		Width:       width,
		Height:      height,
		Environment: environment,
	}

	captured := captureServerOptions(ServerOptions{InitialSession: &initial})
	initial.Name = "after"
	environment["COPY_PROBE"] = "after"

	if captured.InitialSession == nil {
		t.Fatal("captured InitialSession is nil")
	}
	if got := captured.InitialSession.Name; got != "before" {
		t.Fatalf("captured Name = %q, want before", got)
	}
	if got := captured.InitialSession.Width; got != 80 {
		t.Fatalf("captured Width = %d, want 80", got)
	}
	if got := captured.InitialSession.Height; got != 24 {
		t.Fatalf("captured Height = %d, want 24", got)
	}
	if got := captured.InitialSession.Environment["COPY_PROBE"]; got != "before" {
		t.Fatalf("captured environment = %q, want before", got)
	}
}

func TestCaptureNewWindowRequestCopiesReferencedValues(t *testing.T) {
	name := "before"
	index := 3
	environment := map[string]string{"COPY_PROBE": "before"}
	request := tmux.NewWindowRequest{
		Name:        &name,
		Index:       &index,
		Environment: environment,
	}

	captured := captureNewWindowRequest(request)
	name = "after"
	index = 9
	environment["COPY_PROBE"] = "after"

	if got := *captured.Name; got != "before" {
		t.Fatalf("captured Name = %q, want before", got)
	}
	if got := *captured.Index; got != 3 {
		t.Fatalf("captured Index = %d, want 3", got)
	}
	if got := captured.Environment["COPY_PROBE"]; got != "before" {
		t.Fatalf("captured environment = %q, want before", got)
	}
}
