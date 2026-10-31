package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var flowSequenceOfVersions = regexp.MustCompile(`(?m)^\s+(?:tmux-version|rejected-version|go-version):\s*\[`)

func TestTestsWorkflowTakesItsMatricesFromThePlan(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github", "workflows", "tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	if match := flowSequenceOfVersions.FindString(text); match != "" {
		t.Errorf("the tests workflow lists versions inline (%q); they belong in %s", strings.TrimSpace(match), ciManifestPath)
	}
	for _, want := range []string{
		"fromJSON(needs.plan.outputs.tmux)",
		"fromJSON(needs.plan.outputs.rejected)",
		"fromJSON(needs.plan.outputs.go-versions)",
		"runs-on: ${{ needs.plan.outputs.os }}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the tests workflow does not use the plan: no %s", want)
		}
	}
	for line := range strings.Lines(text) {
		if strings.Contains(line, "${{ inputs.") && !strings.HasPrefix(strings.TrimSpace(line), "INPUT_") {
			t.Errorf("an input reaches the workflow outside an INPUT_ env entry: %s", strings.TrimSpace(line))
		}
	}
}
