package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// planManifest is a small table with a middle release, a development lane and
// refused releases, so each rule of the plan has something to include or leave
// out.
const planManifest = `
# a comment
supported 3.3a
supported 3.4
supported 3.7  # trailing comment
supported 3.8

refused 3.2a
refused 3.3

development master

control 3.7

go 1.26
go 1.27
`

type planLane struct {
	Version  string `json:"tmux-version"`
	Name     string `json:"name"`
	Go       string `json:"go-version"`
	Expect   string `json:"expect"`
	Advisory bool   `json:"advisory"`
	Control  bool   `json:"control"`
	Tmate    string `json:"tmate"`
}

type plan struct {
	Scope    string
	Lanes    []planLane
	Rejected []string
	Go       []string
	OS       string
	Skip     string
	Jobs     string
}

func (p plan) versions() []string {
	versions := make([]string, 0, len(p.Lanes))
	for _, lane := range p.Lanes {
		versions = append(versions, lane.Version)
	}
	return versions
}

// runPlan runs scripts/ci-plan.sh for the event and inputs given as KEY=VALUE
// pairs. It returns the parsed outputs, or the script's stderr when it fails.
func runPlan(t *testing.T, manifest string, variables ...string) (plan, string, error) {
	t.Helper()
	root := repositoryRoot(t)
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "ci-matrix.txt")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(directory, "output")
	command := exec.Command("bash", filepath.Join(root, "scripts", "ci-plan.sh"))
	command.Dir = directory
	command.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"CI_MATRIX=" + manifestPath,
		"GITHUB_OUTPUT=" + outputPath,
	}, variables...)
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return plan{}, stderr.String(), err
	}
	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{}
	for line := range strings.Lines(string(written)) {
		key, value, _ := strings.Cut(strings.TrimRight(line, "\n"), "=")
		fields[key] = value
	}
	decoded := plan{
		Scope: fields["scope"], OS: fields["os"], Skip: fields["skip"], Jobs: fields["jobs"],
	}
	for key, target := range map[string]any{
		"tmux": &decoded.Lanes, "rejected": &decoded.Rejected, "go-versions": &decoded.Go,
	} {
		if err := json.Unmarshal([]byte(fields[key]), target); err != nil {
			t.Fatalf("%s output %q is not JSON: %v", key, fields[key], err)
		}
	}
	return decoded, stderr.String(), nil
}

func planOrFail(t *testing.T, manifest string, variables ...string) plan {
	t.Helper()
	got, stderr, err := runPlan(t, manifest, variables...)
	if err != nil {
		t.Fatalf("plan failed: %v\n%s", err, stderr)
	}
	return got
}

func TestPlanRunsOnlyTheEdgesOnPushAndPullRequest(t *testing.T) {
	t.Parallel()

	for name, variables := range map[string][]string{
		"push to a branch": {"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/heads/master"},
		"pull request":     {"GITHUB_EVENT_NAME=pull_request", "GITHUB_REF=refs/pull/7/merge"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := planOrFail(t, planManifest, variables...)
			if want := []string{"3.3a", "3.8"}; !slices.Equal(got.versions(), want) {
				t.Errorf("lanes = %v, want only the edges %v", got.versions(), want)
			}
			if len(got.Rejected) != 0 {
				t.Errorf("refused releases = %v, want none", got.Rejected)
			}
			if got.Scope != "edges" || got.Jobs != "2" || got.Skip != "tmux-rejected" {
				t.Errorf("scope = %q, jobs = %q, skip = %q", got.Scope, got.Jobs, got.Skip)
			}
			if want := []string{"1.26", "1.27"}; !slices.Equal(got.Go, want) {
				t.Errorf("Go toolchains = %v, want the manifest's %v", got.Go, want)
			}
			if got.OS != "ubuntu-latest" {
				t.Errorf("os = %q", got.OS)
			}
		})
	}
}

func TestPlanRunsTheWholeTableForATagAndForFullScope(t *testing.T) {
	t.Parallel()

	for name, variables := range map[string][]string{
		"tag push":      {"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/tags/v0.1.1"},
		"dispatch full": {"GITHUB_EVENT_NAME=workflow_dispatch", "INPUT_SCOPE=full"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := planOrFail(t, planManifest, variables...)
			if want := []string{"3.3a", "3.4", "3.7", "3.8", "master"}; !slices.Equal(got.versions(), want) {
				t.Errorf("lanes = %v, want %v", got.versions(), want)
			}
			if want := []string{"3.2a", "3.3"}; !slices.Equal(got.Rejected, want) {
				t.Errorf("refused releases = %v, want %v", got.Rejected, want)
			}
			if got.Scope != "full" || got.Jobs != "7" || got.Skip != "" {
				t.Errorf("scope = %q, jobs = %q, skip = %q", got.Scope, got.Jobs, got.Skip)
			}
			master := got.Lanes[len(got.Lanes)-1]
			if !master.Advisory || master.Expect != "tmux next-*" {
				t.Errorf("master lane = %+v, want advisory and next-X.Y", master)
			}
			if got.Lanes[0].Advisory {
				t.Errorf("a release lane is advisory: %+v", got.Lanes[0])
			}
		})
	}
}

func TestPlanMarksTheLanesThatHaveTheControlClient(t *testing.T) {
	t.Parallel()

	got := planOrFail(t, planManifest, "GITHUB_EVENT_NAME=workflow_dispatch", "INPUT_SCOPE=full")
	want := map[string]bool{"3.3a": false, "3.4": false, "3.7": true, "3.8": true, "master": true}
	for _, lane := range got.Lanes {
		if lane.Control != want[lane.Version] {
			t.Errorf("lane %s control = %v, want %v", lane.Version, lane.Control, want[lane.Version])
		}
	}
}

func TestPlanIgnoresDispatchInputsOffADispatch(t *testing.T) {
	t.Parallel()

	got := planOrFail(t, planManifest,
		"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/heads/master",
		"INPUT_SCOPE=full", "INPUT_TMUX_REF=3.4", "INPUT_TMATE=before-tests",
		"INPUT_GO_VERSION=1.99", "INPUT_OS=ubuntu-22.04")
	if want := []string{"3.3a", "3.8"}; !slices.Equal(got.versions(), want) {
		t.Errorf("lanes = %v, want only the edges %v", got.versions(), want)
	}
	for _, lane := range got.Lanes {
		if lane.Tmate != "off" || lane.Go != "stable" {
			t.Errorf("a push took an input: %+v", lane)
		}
	}
	if got.OS != "ubuntu-latest" || !slices.Equal(got.Go, []string{"1.26", "1.27"}) {
		t.Errorf("a push took an input: os = %q, Go = %v", got.OS, got.Go)
	}
}

func TestPlanCustomScopeRunsTheNamedRefs(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	got := planOrFail(t, planManifest,
		"GITHUB_EVENT_NAME=workflow_dispatch", "INPUT_SCOPE=custom",
		"INPUT_TMUX_REF= 3.7 , master,feature/x,"+commit+",3.7,3.3,3.9")
	if want := []string{"3.7", "master", "feature/x", commit, "3.9"}; !slices.Equal(got.versions(), want) {
		t.Errorf("lanes = %v, want %v", got.versions(), want)
	}
	if want := []string{"3.3"}; !slices.Equal(got.Rejected, want) {
		t.Errorf("refused releases = %v, want the named one %v", got.Rejected, want)
	}
	byVersion := map[string]planLane{}
	for _, lane := range got.Lanes {
		byVersion[lane.Version] = lane
	}
	for version, want := range map[string]string{
		"3.7": "tmux 3.7", "master": "tmux next-*", "feature/x": "tmux *", commit: "tmux *", "3.9": "tmux 3.9",
	} {
		if byVersion[version].Expect != want {
			t.Errorf("lane %s expects %q, want %q", version, byVersion[version].Expect, want)
		}
	}
	if name := byVersion[commit].Name; name != commit[:12] {
		t.Errorf("a commit lane is named %q, want its first 12 digits", name)
	}
	if !byVersion["3.9"].Control || !byVersion[commit].Control || !byVersion["feature/x"].Control {
		t.Error("a newer release, branch or commit lacks the control client flag")
	}
	if got.Scope != "custom" || got.Jobs != "6" {
		t.Errorf("scope = %q, jobs = %q", got.Scope, got.Jobs)
	}
}

func TestPlanCustomScopeCanNameOnlyARefusedRelease(t *testing.T) {
	t.Parallel()

	got := planOrFail(t, planManifest,
		"GITHUB_EVENT_NAME=workflow_dispatch", "INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.2a")
	if len(got.Lanes) != 0 || !slices.Equal(got.Rejected, []string{"3.2a"}) {
		t.Errorf("lanes = %v, refused = %v", got.versions(), got.Rejected)
	}
	if got.Skip != "tmux-matrix" {
		t.Errorf("skip = %q, want the empty compatible job named", got.Skip)
	}
}

func TestPlanStackInputsReachTheLanesAndTheModuleJobs(t *testing.T) {
	t.Parallel()

	got := planOrFail(t, planManifest,
		"GITHUB_EVENT_NAME=workflow_dispatch", "INPUT_SCOPE=custom",
		"INPUT_TMUX_REF=3.4,3.8", "INPUT_GO_VERSION=1.26, stable", "INPUT_OS=ubuntu-24.04")
	if want := []string{"1.26", "stable"}; !slices.Equal(got.Go, want) {
		t.Errorf("Go toolchains = %v, want %v", got.Go, want)
	}
	if got.OS != "ubuntu-24.04" {
		t.Errorf("os = %q", got.OS)
	}
	var names []string
	for _, lane := range got.Lanes {
		names = append(names, lane.Name)
	}
	if want := []string{"3.4, go 1.26", "3.4", "3.8, go 1.26", "3.8"}; !slices.Equal(names, want) {
		t.Errorf("lane names = %v, want one per tmux and Go pair %v", names, want)
	}
}

func TestPlanGivesTmateToExactlyOneLane(t *testing.T) {
	t.Parallel()

	got := planOrFail(t, planManifest,
		"GITHUB_EVENT_NAME=workflow_dispatch", "INPUT_SCOPE=custom",
		"INPUT_TMUX_REF=3.8", "INPUT_TMATE=on-failure")
	if len(got.Lanes) != 1 || got.Lanes[0].Tmate != "on-failure" {
		t.Errorf("lanes = %+v, want the one lane to carry tmate", got.Lanes)
	}

	for name, variables := range map[string][]string{
		"two refs":        {"INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.7,3.8", "INPUT_TMATE=before-tests"},
		"two toolchains":  {"INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.8", "INPUT_GO_VERSION=1.26,1.27", "INPUT_TMATE=on-failure"},
		"the edges":       {"INPUT_SCOPE=edges", "INPUT_TMATE=on-failure"},
		"the whole table": {"INPUT_SCOPE=full", "INPUT_TMATE=on-failure"},
		"a refused lane":  {"INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.8,3.3", "INPUT_TMATE=on-failure"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, stderr, err := runPlan(t, planManifest,
				append([]string{"GITHUB_EVENT_NAME=workflow_dispatch"}, variables...)...)
			if err == nil {
				t.Fatal("tmate on more than one lane was planned")
			}
			if !strings.Contains(stderr, "tmate needs exactly one tmux lane") {
				t.Errorf("stderr does not explain the refusal:\n%s", stderr)
			}
		})
	}
}

func TestPlanRefusesWhatItCannotPlan(t *testing.T) {
	t.Parallel()

	dispatch := func(variables ...string) []string {
		return append([]string{"GITHUB_EVENT_NAME=workflow_dispatch"}, variables...)
	}
	for name, test := range map[string]struct {
		variables []string
		want      string
	}{
		"an event with no plan":   {[]string{"GITHUB_EVENT_NAME=schedule"}, "no plan for the schedule event"},
		"no event":                {nil, "GITHUB_EVENT_NAME"},
		"an unknown scope":        {dispatch("INPUT_SCOPE=everything"), "scope must be"},
		"custom with no refs":     {dispatch("INPUT_SCOPE=custom"), "tmux-ref is empty"},
		"custom with blank refs":  {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF=  "), "tmux-ref is empty"},
		"an empty item":           {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.8,,3.7"), "empty item"},
		"a trailing comma":        {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.8,"), "empty item"},
		"a shell metacharacter":   {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.8;id"), "invalid item"},
		"a command substitution":  {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF=$(id)"), "invalid item"},
		"a space inside an item":  {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.8 3.7"), "invalid item"},
		"an item too long":        {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF="+strings.Repeat("a", 65)), "invalid item"},
		"a leading dash":          {dispatch("INPUT_SCOPE=custom", "INPUT_TMUX_REF=-abc"), "invalid item"},
		"an unknown runner":       {dispatch("INPUT_OS=macos-latest"), "os must be"},
		"an unknown tmate mode":   {dispatch("INPUT_TMATE=always"), "tmate must be"},
		"a bad toolchain":         {dispatch("INPUT_GO_VERSION=1.26;id"), "invalid item"},
		"an empty toolchain item": {dispatch("INPUT_GO_VERSION=1.26,,1.27"), "empty item"},
		"a toolchain with a dash": {dispatch("INPUT_GO_VERSION=-1.26"), "invalid item"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, stderr, err := runPlan(t, planManifest, test.variables...)
			if err == nil {
				t.Fatalf("planned %+v", got)
			}
			if !strings.Contains(stderr, test.want) {
				t.Errorf("stderr lacks %q:\n%s", test.want, stderr)
			}
		})
	}
}

func TestPlanAlwaysPlansAJob(t *testing.T) {
	t.Parallel()

	dispatch := "GITHUB_EVENT_NAME=workflow_dispatch"
	for name, variables := range map[string][]string{
		"push":          {"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/heads/master"},
		"tag":           {"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/tags/v1"},
		"pull request":  {"GITHUB_EVENT_NAME=pull_request"},
		"dispatch":      {dispatch},
		"edges":         {dispatch, "INPUT_SCOPE=edges"},
		"full":          {dispatch, "INPUT_SCOPE=full"},
		"custom":        {dispatch, "INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.4"},
		"a refused one": {dispatch, "INPUT_SCOPE=custom", "INPUT_TMUX_REF=3.3"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := planOrFail(t, planManifest, variables...)
			if got.Jobs == "0" || len(got.Lanes)+len(got.Rejected) == 0 {
				t.Errorf("the plan has no tmux job: %+v", got)
			}
		})
	}
}

func TestPlanAcceptsTheLongestRefAndACommit(t *testing.T) {
	t.Parallel()

	longest := strings.Repeat("a", 64)
	got := planOrFail(t, planManifest,
		"GITHUB_EVENT_NAME=workflow_dispatch", "INPUT_SCOPE=custom", "INPUT_TMUX_REF="+longest)
	if !slices.Equal(got.versions(), []string{longest}) {
		t.Errorf("lanes = %v", got.versions())
	}
}

func TestPlanRefusesAManifestItCannotTrust(t *testing.T) {
	t.Parallel()

	push := []string{"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/heads/master"}
	valid := "supported 3.3a\nsupported 3.8\ncontrol 3.7\ngo 1.26\n"
	for name, test := range map[string]struct{ manifest, want string }{
		"no supported release":  {"control 3.7\ngo 1.26\n", "no supported tmux release"},
		"no toolchain":          {"supported 3.3a\ncontrol 3.7\n", "no Go toolchain"},
		"no control release":    {"supported 3.3a\ngo 1.26\n", "no control release"},
		"two control releases":  {valid + "control 3.8\n", "two control releases"},
		"an unknown kind":       {valid + "preview 3.9\n", "unknown kind"},
		"a repeated release":    {valid + "refused 3.3a\n", "repeats tmux 3.3a"},
		"a repeated toolchain":  {valid + "go 1.26\n", "repeats go 1.26"},
		"a line without value":  {valid + "refused\n", "malformed"},
		"a line with extra":     {valid + "refused 3.2a 3.1c\n", "malformed"},
		"a release with a pipe": {valid + "refused 3.2a|id\n", "invalid tmux ref"},
		"a toolchain with a $":  {valid + "go $HOME\n", "invalid toolchain"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, stderr, err := runPlan(t, test.manifest, push...)
			if err == nil {
				t.Fatalf("planned %+v", got)
			}
			if !strings.Contains(stderr, test.want) {
				t.Errorf("stderr lacks %q:\n%s", test.want, stderr)
			}
		})
	}
}

func TestPlanForASingleSupportedReleaseHasOneEdge(t *testing.T) {
	t.Parallel()

	got := planOrFail(t, "supported 3.8\ncontrol 3.7\ngo 1.26\n",
		"GITHUB_EVENT_NAME=pull_request")
	if !slices.Equal(got.versions(), []string{"3.8"}) {
		t.Errorf("lanes = %v, want the one release once", got.versions())
	}
}

func TestPlanOfTheRealManifestRunsItsFirstAndLastRelease(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	supported := ciManifestEntries(t, root, "supported")
	manifest, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ciManifestPath)))
	if err != nil {
		t.Fatal(err)
	}
	got := planOrFail(t, string(manifest), "GITHUB_EVENT_NAME=pull_request")
	want := []string{supported[0], supported[len(supported)-1]}
	if !slices.Equal(got.versions(), want) {
		t.Errorf("lanes = %v, want the first and last of %v", got.versions(), supported)
	}
}

func runVerify(t *testing.T, needs, allow string) (string, error) {
	t.Helper()
	command := exec.Command("bash", filepath.Join(repositoryRoot(t), "scripts", "ci-plan.sh"), "verify")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "NEEDS_JSON=" + needs, "ALLOW_SKIPPED=" + allow}
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestVerifyPassesOnlyWhenEveryPlannedJobPassed(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		needs, allow string
		wantErr      string
	}{
		"all succeeded": {
			needs: `{"plan":{"result":"success"},"test":{"result":"success"}}`,
		},
		"a planned-out job skipped": {
			needs: `{"plan":{"result":"success"},"tmux-rejected":{"result":"skipped"}}`,
			allow: "tmux-rejected",
		},
		"a job failed": {
			needs:   `{"plan":{"result":"success"},"test":{"result":"failure"}}`,
			wantErr: "test: failure",
		},
		"a job was cancelled": {
			needs:   `{"plan":{"result":"success"},"test":{"result":"cancelled"}}`,
			wantErr: "test: cancelled",
		},
		"a job the plan kept in scope was skipped": {
			needs:   `{"plan":{"result":"success"},"tmux-matrix":{"result":"skipped"}}`,
			allow:   "tmux-rejected",
			wantErr: "tmux-matrix: skipped",
		},
		"the plan failed and skipped the rest": {
			needs:   `{"plan":{"result":"failure"},"tmux-matrix":{"result":"skipped"}}`,
			wantErr: "plan: failure",
		},
		"a job with no result": {
			needs:   `{"plan":{"result":"success"},"test":{}}`,
			wantErr: "test: null",
		},
		"no jobs at all": {
			needs:   `{}`,
			wantErr: "holds no jobs",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			output, err := runVerify(t, test.needs, test.allow)
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("verify failed: %v\n%s", err, output)
			case test.wantErr != "" && err == nil:
				t.Fatalf("verify passed:\n%s", output)
			case test.wantErr != "" && !strings.Contains(output, test.wantErr):
				t.Errorf("output lacks %q:\n%s", test.wantErr, output)
			}
		})
	}
}
