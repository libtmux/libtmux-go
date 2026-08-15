package tmuxcmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestRunnerReturnsNonzeroExitAndSplitOutputAsData(t *testing.T) {
	t.Parallel()

	result, err := (Runner{}).Run(context.Background(), helperRequest("lines"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", result.ExitCode)
	}
	if want := []string{"one", "", "three"}; !slices.Equal(result.Stdout, want) {
		t.Errorf("Stdout = %#v, want %#v", result.Stdout, want)
	}
	if want := []byte("one\n\nthree\n\n"); !slices.Equal(result.RawStdout, want) {
		t.Errorf("RawStdout = %q, want %q", result.RawStdout, want)
	}
	if want := []string{"warning", "second"}; !slices.Equal(result.Stderr, want) {
		t.Errorf("Stderr = %#v, want %#v", result.Stderr, want)
	}
	if got := result.Command[len(result.Command)-1]; got != "lines" {
		t.Errorf("Command last argument = %q, want lines", got)
	}
}

func TestRunnerPreservesRequestedBinaryInCommand(t *testing.T) {
	testDirectory := t.TempDir()
	requestedBinary := "tmux-runner-test"
	if err := os.Symlink(os.Args[0], filepath.Join(testDirectory, requestedBinary)); err != nil {
		t.Fatalf("create helper symlink: %v", err)
	}
	t.Setenv("PATH", testDirectory)
	request := helperRequest("lines")
	request.Binary = requestedBinary

	result, err := (Runner{}).Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Command[0]; got != requestedBinary {
		t.Fatalf("Command[0] = %q, want requested binary %q", got, requestedBinary)
	}
}

func TestRunnerRecordsResolvedDefaultBinaryInCommand(t *testing.T) {
	testDirectory := t.TempDir()
	if err := os.Symlink(os.Args[0], filepath.Join(testDirectory, "tmux")); err != nil {
		t.Fatalf("create helper symlink: %v", err)
	}
	t.Setenv("PATH", testDirectory)
	resolved, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("LookPath(tmux) error = %v", err)
	}
	request := helperRequest("lines")
	request.Binary = ""

	result, err := (Runner{}).Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Command[0]; got != resolved {
		t.Fatalf("Command[0] = %q, want resolved default binary %q", got, resolved)
	}
}

func TestRunnerKeepsStreamsSeparateWhenOperandIsHasSession(t *testing.T) {
	t.Parallel()

	request := helperRequest("stderr-failure")
	request.Arguments = append(request.Arguments, "has-session")
	result, err := (Runner{}).Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Stdout) != 0 {
		t.Fatalf("Stdout = %#v, want empty", result.Stdout)
	}
	if want := []string{"missing session"}; !slices.Equal(result.Stderr, want) {
		t.Fatalf("Stderr = %#v, want %#v", result.Stderr, want)
	}
}

func TestRunnerBackslashEscapesInvalidUTF8(t *testing.T) {
	t.Parallel()

	result, err := (Runner{}).Run(context.Background(), helperRequest("invalid-utf8"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := []string{`\xff`}; !slices.Equal(result.Stdout, want) {
		t.Errorf("Stdout = %#v, want %#v", result.Stdout, want)
	}
}

func TestRunnerReturnsContextDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	_, err := (Runner{}).Run(ctx, helperRequest("block"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline", err)
	}
}

func TestRunnerNaturalExitWinsCancellationRace(t *testing.T) {
	ready, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for helper readiness: %v", err)
	}
	t.Cleanup(func() { _ = ready.Close() })

	for _, exitCode := range []int{0, 7} {
		t.Run(strconv.Itoa(exitCode), func(t *testing.T) {
			const attempts = 250
			for attempt := range attempts {
				ctx, cancel := context.WithCancel(context.Background())
				request := helperRequest("ready-exit")
				request.Arguments = append(
					request.Arguments,
					ready.LocalAddr().String(),
					strconv.Itoa(exitCode),
				)
				type outcome struct {
					result Result
					err    error
				}
				completed := make(chan outcome, 1)
				go func() {
					result, runErr := (Runner{}).Run(ctx, request)
					completed <- outcome{result: result, err: runErr}
				}()

				if err := ready.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					cancel()
					t.Fatalf("set readiness deadline: %v", err)
				}
				var notification [1]byte
				if _, _, err := ready.ReadFrom(notification[:]); err != nil {
					cancel()
					t.Fatalf("attempt %d: await helper readiness: %v", attempt, err)
				}
				cancel()
				got := <-completed

				if got.err == nil {
					if got.result.ExitCode != exitCode {
						t.Fatalf(
							"attempt %d: natural ExitCode = %d, want %d",
							attempt,
							got.result.ExitCode,
							exitCode,
						)
					}
				} else if !errors.Is(got.err, context.Canceled) {
					t.Fatalf(
						"attempt %d: cancellation error = %v, want context canceled",
						attempt,
						got.err,
					)
				} else if got.result.ExitCode == exitCode {
					t.Fatalf(
						"attempt %d: natural exit %d error = %v, want nil",
						attempt,
						exitCode,
						got.err,
					)
				}
			}
		})
	}
}

func TestClassifyRunErrorLetsCompletedSuccessWinCancellationRace(t *testing.T) {
	t.Parallel()

	if err := classifyRunError(context.Canceled, context.Canceled, processOutcomeNatural); err != nil {
		t.Fatalf("classifyRunError(natural success, context.Canceled) = %v, want nil", err)
	}
}

func TestClassifyRunErrorLetsNaturalNonzeroExitWinCancellationRace(t *testing.T) {
	t.Parallel()

	exitError := &exec.ExitError{}
	if err := classifyRunError(exitError, context.Canceled, processOutcomeNatural); err != nil {
		t.Fatalf("classifyRunError(natural exit, context.Canceled) = %v, want nil", err)
	}
}

func TestClassifyRunErrorReturnsContextWhenCancellationKilledProcess(t *testing.T) {
	t.Parallel()

	exitError := &exec.ExitError{}
	err := classifyRunError(exitError, context.DeadlineExceeded, processOutcomeCanceled)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("classifyRunError(interrupted exit) = %v, want context deadline", err)
	}
}

func TestClassifyRunErrorPreservesWaitDelayFailure(t *testing.T) {
	t.Parallel()

	err := classifyRunError(exec.ErrWaitDelay, nil, processOutcomeNatural)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("classifyRunError(ErrWaitDelay) = %v, want ErrWaitDelay", err)
	}
}

func TestClassifyRunErrorPreservesUnattributedSignalAfterCancellation(t *testing.T) {
	t.Parallel()

	exitError := &exec.ExitError{}
	if err := classifyRunError(exitError, context.Canceled, processOutcomeRaw); err != nil {
		t.Fatalf("classifyRunError(raw signal) = %v, want nil", err)
	}
}

func TestProcessExitOutcomeWithTerminationCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		exitCode    int
		interrupted bool
		want        processOutcome
	}{
		{name: "marked termination", exitCode: 1, interrupted: true, want: processOutcomeCanceled},
		{name: "natural success race", exitCode: 0, interrupted: true, want: processOutcomeNatural},
		{name: "natural failure race", exitCode: 7, interrupted: true, want: processOutcomeNatural},
		{name: "unmarked matching exit", exitCode: 1, interrupted: false, want: processOutcomeNatural},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := processExitOutcomeWithTerminationCode(test.exitCode, 1, test.interrupted)
			if got != test.want {
				t.Fatalf("process outcome = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRunnerBoundsCancellationWhenDescendantHoldsOutputPipe(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	result, err := (Runner{}).Run(ctx, helperRequest("orphan-output-pipe"))
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline", err)
	}
	if len(result.Stdout) > 0 {
		pid, parseErr := strconv.Atoi(result.Stdout[0])
		if parseErr == nil {
			process, findErr := os.FindProcess(pid)
			if findErr == nil {
				_ = process.Kill()
			}
		}
	}
	if elapsed > 750*time.Millisecond {
		t.Fatalf("Run() elapsed = %v, want bounded cancellation", elapsed)
	}
}

func TestRunnerReturnsProcessStartFailure(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing-tmux")
	result, err := (Runner{}).Run(context.Background(), Request{Binary: missing})
	if err == nil {
		t.Fatal("Run() error = nil, want process-start failure")
	}
	if result.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 for process-start failure", result.ExitCode)
	}
	if want := []string{missing}; !slices.Equal(result.Command, want) {
		t.Errorf("Command = %#v, want %#v", result.Command, want)
	}
}

func helperRequest(mode string) Request {
	return Request{
		Binary:    os.Args[0],
		Arguments: []string{"-test.run=^TestRunnerHelperProcess$", "--", mode},
	}
}

func TestRunnerHelperProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator == -1 {
		return
	}
	if separator+1 >= len(os.Args) {
		t.Fatal("helper mode is missing")
	}

	switch os.Args[separator+1] {
	case "lines":
		_, _ = fmt.Fprint(os.Stdout, "one\n\nthree\n\n")
		_, _ = fmt.Fprint(os.Stderr, "warning\n\nsecond\n")
		os.Exit(7)
	case "stderr-failure":
		_, _ = fmt.Fprintln(os.Stderr, "missing session")
		os.Exit(1)
	case "invalid-utf8":
		_, _ = os.Stdout.Write([]byte{0xff, '\n'})
		os.Exit(0)
	case "block":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "ready-exit":
		if separator+3 >= len(os.Args) {
			t.Fatal("ready-exit helper arguments are missing")
		}
		connection, err := net.Dial("udp", os.Args[separator+2])
		if err != nil {
			t.Fatalf("notify readiness: %v", err)
		}
		if _, err := connection.Write([]byte{1}); err != nil {
			t.Fatalf("notify readiness: %v", err)
		}
		exitCode, err := strconv.Atoi(os.Args[separator+3])
		if err != nil {
			t.Fatalf("parse exit code: %v", err)
		}
		os.Exit(exitCode)
	case "orphan-output-pipe":
		child := exec.Command(
			os.Args[0],
			"-test.run=^TestRunnerHelperProcess$",
			"--",
			"hold-output-pipe",
		)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "hold-output-pipe":
		time.Sleep(2 * time.Second)
		os.Exit(0)
	default:
		t.Fatalf("unknown helper mode %q", os.Args[separator+1])
	}
}
