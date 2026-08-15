//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package tmuxcmd

import (
	"os"
	"syscall"
)

func processExitOutcome(state *os.ProcessState, interrupted bool) processOutcome {
	if state == nil {
		return processOutcomeUnknown
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		if state.ExitCode() >= 0 {
			return processOutcomeNatural
		}
		if interrupted {
			return processOutcomeCanceled
		}
		return processOutcomeRaw
	}
	if !status.Signaled() {
		return processOutcomeNatural
	}
	// Process.Kill uses SIGKILL on these systems. A SIGKILL observed after a
	// successful cancel is therefore attributed to the context; a process that
	// killed itself with SIGKILL in the same race is indistinguishable.
	if interrupted && status.Signal() == syscall.SIGKILL {
		return processOutcomeCanceled
	}
	return processOutcomeRaw
}

func processResultExitCode(state *os.ProcessState, outcome processOutcome) int {
	if state == nil || outcome == processOutcomeCanceled {
		return -1
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return -int(status.Signal())
	}
	return state.ExitCode()
}
