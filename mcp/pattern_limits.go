package mcp

import (
	"fmt"
	"time"
)

const (
	patternCountLimit         = 32
	patternBytesLimit         = 4_096
	patternTotalBytesLimit    = 16_384
	searchWorkBytesLimit      = 1_000_000
	searchPaneInspectionLimit = 200
	searchLineInspectionLimit = 20_000
	searchWorkTimeout         = 5 * time.Second
)

func validatePatternInputs(groups ...[]string) error {
	count := 0
	total := 0
	for _, patterns := range groups {
		for _, pattern := range patterns {
			count++
			if len(pattern) > patternBytesLimit {
				return fmt.Errorf("a pattern exceeds the %d-byte limit", patternBytesLimit)
			}
			total += len(pattern)
		}
	}
	if count > patternCountLimit {
		return fmt.Errorf("at most %d patterns may be supplied", patternCountLimit)
	}
	if total > patternTotalBytesLimit {
		return fmt.Errorf("patterns exceed the %d-byte combined limit", patternTotalBytesLimit)
	}
	return nil
}

type searchWorkBudget struct {
	panes int
	lines int
	bytes int
}

func (budget *searchWorkBudget) startPane() bool {
	if budget.panes >= searchPaneInspectionLimit {
		return false
	}
	budget.panes++
	return true
}

func (budget *searchWorkBudget) consumeLine(line string) bool {
	if budget.lines >= searchLineInspectionLimit {
		return false
	}
	cost := len(line) + 1
	if cost > searchWorkBytesLimit-budget.bytes {
		return false
	}
	budget.lines++
	budget.bytes += cost
	return true
}
