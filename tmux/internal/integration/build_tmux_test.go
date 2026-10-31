package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tmuxFixture is a repository shaped like tmux's: an autogen.sh that writes
// configure, which writes a Makefile whose install puts a tmux that prints the
// VERSION file of the commit it was built from. It has an annotated tag, a
// branch, and a commit that only the branch reaches.
type tmuxFixture struct {
	url                                   string
	tagCommit, branchCommit, masterCommit string
}

func newTmuxFixture(t *testing.T) tmuxFixture {
	t.Helper()
	directory := t.TempDir()
	home := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{
			"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
			"-c", "init.defaultBranch=master",
		}, args...)...)
		command.Dir = directory
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1"}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	write := func(name, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(version string) string {
		write("VERSION", version+"\n", 0o600)
		git("add", ".")
		git("commit", "--quiet", "--message", version)
		return git("rev-parse", "HEAD")
	}

	git("init", "--quiet")
	write("autogen.sh", `cat > configure <<'CONFIGURE'
for argument in "$@"; do
	case $argument in --prefix=*) prefix=${argument#--prefix=} ;; esac
done
printf 'PREFIX = %s\n' "$prefix" > Makefile
printf 'all:\n\t@true\ninstall:\n\tmkdir -p $(PREFIX)/bin\n' >> Makefile
printf '\tprintf "#!/bin/sh\\necho tmux %%s\\n" "$$(cat VERSION)" > $(PREFIX)/bin/tmux\n' >> Makefile
printf '\tchmod +x $(PREFIX)/bin/tmux\n' >> Makefile
CONFIGURE
chmod +x configure
`, 0o600)
	var fixture tmuxFixture
	fixture.tagCommit = commit("tagged")
	git("tag", "--annotate", "--message", "release", "9.9")
	git("branch", "feature/x")
	fixture.masterCommit = commit("master")
	git("switch", "--quiet", "feature/x")
	fixture.branchCommit = commit("branch")
	git("switch", "--quiet", "master")
	git("config", "uploadpack.allowAnySHA1InWant", "true")
	fixture.url = "file://" + directory
	return fixture
}

func runBuildTmux(t *testing.T, fixture tmuxFixture, arguments ...string) (string, string, error) {
	t.Helper()
	command := exec.Command("bash", append([]string{
		filepath.Join(repositoryRoot(t), "scripts", "build-tmux.sh"),
	}, arguments...)...)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
		"TMUX_REPOSITORY=" + fixture.url,
	}
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return strings.TrimSpace(stdout.String()), stderr.String(), err
}

func TestBuildTmuxResolvesEveryKindOfRef(t *testing.T) {
	t.Parallel()

	fixture := newTmuxFixture(t)
	for ref, want := range map[string]string{
		"9.9":                fixture.tagCommit,
		"master":             fixture.masterCommit,
		"feature/x":          fixture.branchCommit,
		fixture.branchCommit: fixture.branchCommit,
	} {
		got, stderr, err := runBuildTmux(t, fixture, "resolve", ref)
		if err != nil {
			t.Fatalf("resolve %s: %v\n%s", ref, err, stderr)
		}
		if got != want {
			t.Errorf("resolve %s = %s, want %s (an annotated tag resolves to its commit)", ref, got, want)
		}
	}
}

func TestBuildTmuxRefusesWhatItCannotResolve(t *testing.T) {
	t.Parallel()

	fixture := newTmuxFixture(t)
	for name, test := range map[string]struct {
		arguments []string
		want      string
	}{
		"a ref nothing names":   {[]string{"resolve", "9.8"}, "has no tag or branch named 9.8"},
		"a shell metacharacter": {[]string{"resolve", "9.9;id"}, "not a tmux ref"},
		"a leading dash":        {[]string{"resolve", "-abc"}, "not a tmux ref"},
		"an overlong ref":       {[]string{"resolve", strings.Repeat("a", 65)}, "not a tmux ref"},
		"no mode":               {nil, "usage"},
		"build without prefix":  {[]string{"build", "9.9"}, "usage"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, stderr, err := runBuildTmux(t, fixture, test.arguments...)
			if err == nil {
				t.Fatal("the script succeeded")
			}
			if !strings.Contains(stderr, test.want) {
				t.Errorf("stderr lacks %q:\n%s", test.want, stderr)
			}
		})
	}
}

func TestBuildTmuxBuildsTheCommitARefNames(t *testing.T) {
	t.Parallel()

	fixture := newTmuxFixture(t)
	for ref, want := range map[string]struct{ commit, reported string }{
		"9.9":                {fixture.tagCommit, "tmux tagged"},
		"feature/x":          {fixture.branchCommit, "tmux branch"},
		fixture.masterCommit: {fixture.masterCommit, "tmux master"},
	} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			prefix := filepath.Join(t.TempDir(), "tmux")
			commit, stderr, err := runBuildTmux(t, fixture, "build", ref, prefix)
			if err != nil {
				t.Fatalf("build %s: %v\n%s", ref, err, stderr)
			}
			if commit != want.commit {
				t.Errorf("build printed %s, want the commit %s", commit, want.commit)
			}
			output, err := exec.Command(filepath.Join(prefix, "bin", "tmux")).Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(output)); got != want.reported {
				t.Errorf("the build reports %q, want %q", got, want.reported)
			}
		})
	}
}
