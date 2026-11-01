package integration

import (
	"fmt"
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

// rawApt matches a line that runs apt or apt-get itself, with or without sudo,
// flags or VAR=value prefixes, where a command starts. A call through
// scripts/apt-get.sh does not match: the name must end at a space or at the end
// of the line.
var rawApt = regexp.MustCompile("(?:^|[;&|(!`]|\\b(?:then|do|else)\\s)\\s*(?:(?:sudo|-\\S+|\\w+=\\S*)\\s+)*apt(?:-get)?(?:\\s|$)")

var (
	runKey      = regexp.MustCompile(`^(\s*)(-\s+)?run:\s*(.*)$`)
	blockHeader = regexp.MustCompile(`^[|>][-+0-9]*$`)
	stepName    = regexp.MustCompile(`^\s*-\s+name:\s*(.*)$`)
	topLevelKey = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):`)
	jobKey      = regexp.MustCompile(`^  ([A-Za-z_][A-Za-z0-9_-]*):\s*$`)
)

// rawAptCalls returns one problem for each line of a run script in a workflow or
// composite action that calls apt or apt-get itself. path only names the file in
// the problem. Shell comment lines are skipped.
//
// The core module has no YAML dependency, so this reads the run values the way
// YAML lays them out: a run key, then its value on the same line or in the
// more-indented lines under it.
func rawAptCalls(path, workflow string) []string {
	var problems []string
	var job, step string
	inJobs := false
	consumed := -1
	lines := strings.Split(workflow, "\n")
	for i, line := range lines {
		if i <= consumed {
			continue
		}
		line = strings.TrimRight(line, "\r")
		if key := topLevelKey.FindStringSubmatch(line); key != nil {
			inJobs = key[1] == "jobs"
			job = ""
		} else if heading := jobKey.FindStringSubmatch(line); heading != nil && inJobs {
			job, step = heading[1], ""
		}
		if named := stepName.FindStringSubmatch(line); named != nil {
			step = strings.Trim(strings.TrimSpace(named[1]), `"'`)
			continue
		}
		run := runKey.FindStringSubmatch(line)
		if run == nil {
			continue
		}
		keyIndent := len(run[1]) + len(run[2])
		script := []string{strings.Trim(strings.TrimSpace(run[3]), `"'`)}
		for next := i + 1; next < len(lines); next++ {
			text := strings.TrimRight(lines[next], "\r")
			if strings.TrimSpace(text) != "" && len(text)-len(strings.TrimLeft(text, " ")) <= keyIndent {
				break
			}
			script = append(script, text)
			consumed = next
		}
		if blockHeader.MatchString(script[0]) {
			script[0] = ""
		}
		scope := fmt.Sprintf("step %q", step)
		if job != "" {
			scope = "job " + job + ", " + scope
		}
		for offset, text := range script {
			if strings.HasPrefix(strings.TrimSpace(text), "#") || !rawApt.MatchString(text) {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s:%d (%s) runs %q directly; run it through scripts/apt-get.sh",
				path, i+offset+1, scope, strings.TrimSpace(text)))
		}
	}
	return problems
}

// The scripts/apt-get.sh wrapper bounds apt-get and rotates a hung mirror, but
// only for the calls that go through it.
func TestWorkflowsCallAptOnlyThroughTheWrapper(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	var files []string
	for _, pattern := range []string{
		filepath.Join(".github", "workflows", "*.yml"),
		filepath.Join(".github", "actions", "*", "action.yml"),
	} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no workflow or composite action found under .github")
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range rawAptCalls(filepath.ToSlash(relative), string(content)) {
			t.Error(problem)
		}
	}
}

func TestRawAptCheckNamesTheCallAndTheFix(t *testing.T) {
	t.Parallel()

	const workflow = `name: synthetic
on: push
jobs:
  setup:
    runs-on: ubuntu-24.04
    steps:
      - name: Install tmux
        run: sudo apt-get update
      - name: Install libraries
        run: |
          # sudo apt-get install --yes ignored
          sudo DEBIAN_FRONTEND=noninteractive apt install --yes tmux
      - name: Through the wrapper
        run: ./scripts/apt-get.sh update
`
	wants := [][]string{
		{
			"synthetic.yml:8", "job setup", `step "Install tmux"`,
			`runs "sudo apt-get update" directly`, "run it through scripts/apt-get.sh",
		},
		{
			"synthetic.yml:12", "job setup", `step "Install libraries"`,
			`runs "sudo DEBIAN_FRONTEND=noninteractive apt install --yes tmux" directly`,
			"run it through scripts/apt-get.sh",
		},
	}
	problems := rawAptCalls("synthetic.yml", workflow)
	if len(problems) != len(wants) {
		t.Fatalf("got %d problems and want %d: %q", len(problems), len(wants), problems)
	}
	for i, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(problems[i], fragment) {
				t.Errorf("problem %d lacks %q: %s", i, fragment, problems[i])
			}
		}
	}
}

func TestRawAptCheckAcceptsTheWrapper(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"workflow.yml": `jobs:
  setup:
    steps:
      - name: Install tmux
        run: ./scripts/apt-get.sh update && ./scripts/apt-get.sh install --yes tmux
      - name: Install libraries
        run: |
          ./scripts/apt-get.sh update
          # sudo apt-get is not called here
          ./scripts/apt-get.sh install --yes libevent-dev \
            libutempter-dev
`,
		"action.yml": `runs:
  using: composite
  steps:
    - name: Build tmux
      shell: bash
      run: |
        "$GITHUB_WORKSPACE/scripts/apt-get.sh" update
        "$GITHUB_WORKSPACE/scripts/apt-get.sh" install --yes bison
`,
	} {
		if problems := rawAptCalls(name, text); len(problems) != 0 {
			t.Errorf("%s: the wrapper was refused: %q", name, problems)
		}
	}
}
