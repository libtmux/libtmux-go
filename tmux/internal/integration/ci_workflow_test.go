package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var pinnedTmate = regexp.MustCompile(`uses: mxschmitt/action-tmate@[0-9a-f]{40}\b`)

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

// jobSection returns the text of one job of a workflow, from its heading to the
// next job's.
func jobSection(t *testing.T, workflow, job string) string {
	t.Helper()
	headings := jobHeading.FindAllStringSubmatchIndex(workflow, -1)
	for i, heading := range headings {
		if workflow[heading[2]:heading[3]] != job {
			continue
		}
		if i+1 < len(headings) {
			return workflow[heading[0]:headings[i+1][0]]
		}
		return workflow[heading[0]:]
	}
	t.Fatalf("the workflow has no %s job", job)
	return ""
}

// GitHub reads a bare `off` as false. A default that is not one of the options
// refuses every dispatch that sets nothing, before a run starts.
func TestTheTmateInputQuotesOffInTheOptionsAndTheDefault(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github", "workflows", "tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{"options: ['off', before-tests, on-failure]", "default: 'off'"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tmate input must contain %q", want)
		}
	}
}

func TestTmateIsPinnedLimitedAndConfinedToTheTmuxMatrix(t *testing.T) {
	t.Parallel()

	workflows := filepath.Join(repositoryRoot(t), ".github", "workflows")
	content, err := os.ReadFile(filepath.Join(workflows, "tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	const action = "uses: mxschmitt/action-tmate@"
	uses := strings.Count(text, action)
	if uses == 0 {
		t.Fatal("the tests workflow offers no tmate session")
	}
	if pinned := len(pinnedTmate.FindAllString(text, -1)); pinned != uses {
		t.Errorf("%d tmate steps and %d are pinned by a full commit", uses, pinned)
	}
	if limited := strings.Count(text, "limit-access-to-actor: true"); limited != uses {
		t.Errorf("%d tmate steps and %d limit access to the actor", uses, limited)
	}
	section := jobSection(t, text, "tmux-matrix")
	if got := strings.Count(section, action); got != uses {
		t.Errorf("%d tmate steps are in the tmux-matrix job and %d in the workflow", got, uses)
	}
	if !strings.Contains(section, "timeout-minutes:") {
		t.Error("the job that opens a tmate session has no timeout-minutes cap")
	}
	others, err := filepath.Glob(filepath.Join(workflows, "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range others {
		if filepath.Base(other) == "tests.yml" {
			continue
		}
		body, err := os.ReadFile(other)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "action-tmate") {
			t.Errorf("%s opens a tmate session", filepath.Base(other))
		}
	}
}
