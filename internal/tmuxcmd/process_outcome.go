package tmuxcmd

type processOutcome string

const (
	processOutcomeUnknown  processOutcome = "unknown"
	processOutcomeNatural  processOutcome = "natural"
	processOutcomeRaw      processOutcome = "raw"
	processOutcomeCanceled processOutcome = "canceled"
)

func processExitOutcomeWithTerminationCode(
	exitCode int,
	terminationExitCode int,
	interrupted bool,
) processOutcome {
	if interrupted && exitCode == terminationExitCode {
		return processOutcomeCanceled
	}
	if exitCode >= 0 {
		return processOutcomeNatural
	}
	return processOutcomeUnknown
}
