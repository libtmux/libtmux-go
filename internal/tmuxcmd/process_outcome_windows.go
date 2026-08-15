//go:build windows

package tmuxcmd

import "os"

func processExitOutcome(state *os.ProcessState, interrupted bool) processOutcome {
	const terminationExitCode = 1
	exitCode := -1
	if state != nil {
		exitCode = state.ExitCode()
	}
	// Process.Kill calls TerminateProcess with exit code 1. That code combined
	// with a successful cancel marker is attributed to the context; a process
	// that naturally returns 1 in the same race is indistinguishable.
	return processExitOutcomeWithTerminationCode(exitCode, terminationExitCode, interrupted)
}

func processResultExitCode(state *os.ProcessState, outcome processOutcome) int {
	if state == nil || outcome == processOutcomeCanceled {
		return -1
	}
	return state.ExitCode()
}
