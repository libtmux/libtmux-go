//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxcmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestUnixProcessExitOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mode        string
		interrupted bool
		want        string
	}{
		{name: "natural success after cancel marker", mode: "exit-0", interrupted: true, want: "natural"},
		{name: "natural failure after cancel marker", mode: "exit-7", interrupted: true, want: "natural"},
		{name: "SIGTERM after cancel marker", mode: "signal-term", interrupted: true, want: "raw"},
		{name: "SIGKILL after cancel marker", mode: "signal-kill", interrupted: true, want: "canceled"},
		{name: "self SIGKILL without cancel marker", mode: "signal-kill", interrupted: false, want: "raw"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state := unixHelperProcessState(t, test.mode)
			if got := string(processExitOutcome(state, test.interrupted)); got != test.want {
				t.Fatalf("processExitOutcome(%s, %t) = %q, want %q", test.mode, test.interrupted, got, test.want)
			}
		})
	}
}

func TestUnixProcessResultExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    string
		outcome processOutcome
		want    int
	}{
		{name: "normal success", mode: "exit-0", outcome: processOutcomeNatural, want: 0},
		{name: "normal failure", mode: "exit-7", outcome: processOutcomeNatural, want: 7},
		{name: "SIGTERM data", mode: "signal-term", outcome: processOutcomeRaw, want: -int(syscall.SIGTERM)},
		{name: "self SIGKILL data", mode: "signal-kill", outcome: processOutcomeRaw, want: -int(syscall.SIGKILL)},
		{name: "cancellation SIGKILL", mode: "signal-kill", outcome: processOutcomeCanceled, want: -1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state := unixHelperProcessState(t, test.mode)
			if got := processResultExitCode(state, test.outcome); got != test.want {
				t.Fatalf("processResultExitCode(%s, %q) = %d, want %d", test.mode, test.outcome, got, test.want)
			}
		})
	}
	if got := processResultExitCode(nil, processOutcomeUnknown); got != -1 {
		t.Fatalf("processResultExitCode(nil, unknown) = %d, want -1", got)
	}
}

func TestRunnerReturnsNegativeUnixSignalExitCode(t *testing.T) {
	t.Parallel()

	result, err := (Runner{}).Run(context.Background(), unixOutcomeRequest("signal-term"))
	if err != nil {
		t.Fatalf("Run(self SIGTERM) error = %v", err)
	}
	if result.ExitCode != -int(syscall.SIGTERM) {
		t.Fatalf("Run(self SIGTERM) ExitCode = %d, want %d", result.ExitCode, -int(syscall.SIGTERM))
	}
}

func TestRunnerClassifiesConcurrentCancelAndSelfSIGTERM(t *testing.T) {
	ready, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for helper readiness: %v", err)
	}
	t.Cleanup(func() { _ = ready.Close() })

	const attempts = 32
	for attempt := range attempts {
		ctx, cancel := context.WithCancel(context.Background())
		request := unixOutcomeRequest("ready-signal-term", ready.LocalAddr().String())
		type completedRun struct {
			result Result
			err    error
		}
		completed := make(chan completedRun, 1)
		go func() {
			result, runErr := (Runner{}).Run(ctx, request)
			completed <- completedRun{result: result, err: runErr}
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
		var got completedRun
		select {
		case got = <-completed:
		case <-time.After(2 * time.Second):
			t.Fatalf("attempt %d: canceled helper did not finish", attempt)
		}

		switch {
		case got.err == nil:
			if got.result.ExitCode != -int(syscall.SIGTERM) {
				t.Fatalf("attempt %d: natural signal ExitCode = %d, want %d", attempt, got.result.ExitCode, -int(syscall.SIGTERM))
			}
		case errors.Is(got.err, context.Canceled):
			if got.result.ExitCode != -1 {
				t.Fatalf("attempt %d: canceled ExitCode = %d, want -1", attempt, got.result.ExitCode)
			}
		default:
			t.Fatalf("attempt %d: Run() error = %v", attempt, got.err)
		}
	}
}

func unixHelperProcessState(t *testing.T, mode string) *os.ProcessState {
	t.Helper()

	cmd := exec.Command(
		os.Args[0],
		"-test.run=^TestUnixProcessOutcomeHelperProcess$",
		"--",
		mode,
	)
	err := cmd.Run()
	if mode == "exit-0" {
		if err != nil {
			t.Fatalf("helper %s error = %v", mode, err)
		}
	} else {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			t.Fatalf("helper %s error = %v, want *exec.ExitError", mode, err)
		}
	}
	if cmd.ProcessState == nil {
		t.Fatalf("helper %s ProcessState = nil", mode)
	}
	return cmd.ProcessState
}

func unixOutcomeRequest(mode string, arguments ...string) Request {
	return Request{
		Binary: os.Args[0],
		Arguments: append([]string{
			"-test.run=^TestUnixProcessOutcomeHelperProcess$", "--", mode,
		}, arguments...),
	}
}

func TestUnixProcessOutcomeHelperProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator == -1 {
		return
	}
	if separator+1 >= len(os.Args) {
		t.Fatal("helper mode is missing")
	}
	mode := os.Args[separator+1]
	switch mode {
	case "exit-0":
		os.Exit(0)
	case "exit-7":
		os.Exit(7)
	case "ready-signal-term":
		if separator+2 >= len(os.Args) {
			t.Fatal("ready-signal-term helper address is missing")
		}
		connection, err := net.Dial("udp", os.Args[separator+2])
		if err != nil {
			t.Fatalf("notify readiness: %v", err)
		}
		if _, err := connection.Write([]byte{1}); err != nil {
			t.Fatalf("notify readiness: %v", err)
		}
		_ = connection.Close()
	}
	var signal syscall.Signal
	switch mode {
	case "signal-term":
		signal = syscall.SIGTERM
	case "ready-signal-term":
		signal = syscall.SIGTERM
	case "signal-kill":
		signal = syscall.SIGKILL
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	if err := syscall.Kill(os.Getpid(), signal); err != nil {
		t.Fatalf("signal self: %v", err)
	}
	time.Sleep(time.Minute)
	_, _ = fmt.Fprintln(os.Stderr, "signal did not terminate helper")
	os.Exit(2)
}
