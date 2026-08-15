//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris && !windows

package tmuxcmd

import "os"

func processExitOutcome(state *os.ProcessState, interrupted bool) processOutcome {
	if interrupted {
		return processOutcomeCanceled
	}
	if state != nil {
		return processOutcomeNatural
	}
	return processOutcomeUnknown
}

func processResultExitCode(state *os.ProcessState, outcome processOutcome) int {
	if state == nil || outcome == processOutcomeCanceled {
		return -1
	}
	return state.ExitCode()
}
