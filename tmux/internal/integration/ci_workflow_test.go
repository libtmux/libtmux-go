package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var jobHeading = regexp.MustCompile(`(?m)^  ([a-z][a-z-]*):\s*$`)

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

func TestRequiredJobWaitsForEveryOtherJob(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github", "workflows", "tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	_, jobs, found := strings.Cut(text, "\njobs:\n")
	if !found {
		t.Fatal("the tests workflow has no jobs")
	}
	var others []string
	for _, match := range jobHeading.FindAllStringSubmatch(jobs, -1) {
		if match[1] != "required" {
			others = append(others, match[1])
		}
	}
	needs := regexp.MustCompile(`(?m)^    needs: \[([^\]]+)\]`)
	_, section, found := strings.Cut(jobs, "\n  required:\n")
	if !found {
		t.Fatal("the tests workflow has no required job")
	}
	listed := needs.FindStringSubmatch(section)
	if listed == nil {
		t.Fatal("the required job lists no needs")
	}
	var waits []string
	for entry := range strings.SplitSeq(listed[1], ",") {
		waits = append(waits, strings.TrimSpace(entry))
	}
	slices.Sort(others)
	slices.Sort(waits)
	if !slices.Equal(others, waits) {
		t.Errorf("required waits for %v and the workflow has %v", waits, others)
	}
}
