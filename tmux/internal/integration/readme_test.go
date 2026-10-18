package integration

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// markdownLink matches an inline markdown link's target.
var markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// markdownReference matches a link definition, as in "[label]: target".
var markdownReference = regexp.MustCompile(`(?m)^\[[^\]]+\]:\s+(\S+)`)

// TestEveryPackageDirectoryHasAREADME gates the first thing anyone browsing
// this repository sees.
//
// A directory a reader can click into and find nothing tells them the project
// stopped caring somewhere. Each of these is a package or a module, so each is
// somewhere a reader can arrive first.
func TestEveryPackageDirectoryHasAREADME(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, directory := range []string{
		".", "tmux", "tmux/tmuxtest", "tmuxq",
		"examples", "workspace", "mcp", "benchmarks",
		"internal/tools", "internal/tools/mcp-swap",
	} {
		if _, err := os.Stat(filepath.Join(root, directory, "README.md")); err != nil {
			t.Errorf("%s has no README.md", directory)
		}
	}
}

// repositoryURL matches an absolute link to a path on this repository's
// default branch. Registries such as pkg.go.dev do not rewrite relative links,
// so a README links its directories this way, and the path is still ours to
// keep working.
var repositoryURL = regexp.MustCompile(
	`^https://github\.com/libtmux/libtmux-go/(?:tree|blob)/master/([^#?]*)`)

// brokenLinks returns each link target in prose that names a path that does
// not exist: a relative one, resolved from directory, or an absolute link to
// this repository's default branch, resolved from root.
func brokenLinks(root, directory, prose string) []string {
	targets := make([]string, 0, 16)
	for _, match := range markdownLink.FindAllStringSubmatch(prose, -1) {
		targets = append(targets, match[1])
	}
	for _, match := range markdownReference.FindAllStringSubmatch(prose, -1) {
		targets = append(targets, match[1])
	}

	var broken []string
	for _, target := range targets {
		var path string
		if match := repositoryURL.FindStringSubmatch(target); match != nil {
			path = filepath.Join(root, filepath.FromSlash(match[1]))
		} else {
			// No other absolute link is this repository's to keep working.
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") ||
				strings.HasPrefix(target, "mailto:") {
				continue
			}
			relative, _, _ := strings.Cut(target, "#")
			if relative == "" {
				continue
			}
			path = filepath.Join(directory, filepath.FromSlash(relative))
		}
		if _, err := os.Stat(path); err != nil {
			broken = append(broken, target)
		}
	}
	return broken
}

// TestEveryREADMELinkResolves gates documentation against rot.
//
// A link naming a file that moved still renders as a link. Nothing about
// reading the page reveals it, so the only way to find one is to check every
// target, which is what this does for the links this repository controls and
// can therefore break by itself: the relative ones, and the absolute ones to
// its own default branch.
func TestEveryREADMELinkResolves(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	readmes := findREADMEs(t, root)
	if len(readmes) < 8 {
		t.Fatalf("found %d README files, want at least 8", len(readmes))
	}

	for _, readme := range readmes {
		contents, err := os.ReadFile(readme)
		if err != nil {
			t.Fatal(err)
		}
		relative, _ := filepath.Rel(root, readme)
		for _, target := range brokenLinks(root, filepath.Dir(readme), outsideFences(string(contents))) {
			t.Errorf("%s links to %q, which does not exist", relative, target)
		}
	}
}

// TestBrokenLinksAreFound pins the check above to the failures it exists to
// catch, with the links that must pass beside them, so a change that stops it
// looking cannot go unnoticed.
func TestBrokenLinksAreFound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "NOTES.md"), []byte("notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const prefix = "https://github.com/libtmux/libtmux-go/"
	for _, test := range []struct {
		name, prose string
		want        []string
	}{
		{"a relative link that exists", "[x](sub/)\n", nil},
		{"a relative link that moved", "[x](gone/)\n", []string{"gone/"}},
		{"an absolute directory that exists", "[x](" + prefix + "tree/master/pkg/sub)\n", nil},
		{"an absolute file that exists, with an anchor", "[x](" + prefix + "blob/master/pkg/NOTES.md#top)\n", nil},
		{
			"an absolute directory that moved", "[x](" + prefix + "tree/master/pkg/gone)\n",
			[]string{prefix + "tree/master/pkg/gone"},
		},
		{
			"an absolute file that moved", "[x]: " + prefix + "blob/master/pkg/GONE.md\n",
			[]string{prefix + "blob/master/pkg/GONE.md"},
		},
		{"another repository", "[x](https://github.com/libtmux/other/tree/master/gone)\n", nil},
		{"another branch", "[x](" + prefix + "tree/next/gone)\n", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := brokenLinks(root, filepath.Join(root, "pkg"), test.prose)
			if len(got) != len(test.want) || (len(got) > 0 && got[0] != test.want[0]) {
				t.Errorf("brokenLinks(%q) = %q, want %q", test.prose, got, test.want)
			}
		})
	}
}

// TestPackageListsLinkEachPackage holds the READMEs that list this
// repository's packages to a heading for each one that links its directory by
// absolute URL, which registries such as pkg.go.dev follow where they do not
// rewrite a relative link.
func TestPackageListsLinkEachPackage(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, readme := range []string{"README.md", "tmux/README.md"} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(readme)))
		if err != nil {
			t.Fatal(err)
		}
		for _, directory := range []string{"tmux", "tmuxq", "tmux/tmuxtest", "workspace", "mcp"} {
			heading := "\n### [`" + path.Base(directory) + "`](https://github.com/libtmux/libtmux-go/tree/master/" +
				directory + ")"
			if !strings.Contains(string(contents), heading) {
				t.Errorf("%s has no heading %q", readme, strings.TrimPrefix(heading, "\n"))
			}
		}
	}
}

// outsideFences returns contents without its fenced blocks. Code is not
// Markdown: a generic function's signature, such as ask[T any](records []T),
// reads as a link to anything that only looks for brackets and parentheses.
func outsideFences(contents string) string {
	var prose strings.Builder
	fenced := false
	for line := range strings.SplitSeq(contents, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			prose.WriteString(line)
			prose.WriteByte('\n')
		}
	}
	return prose.String()
}

// findREADMEs returns every README in the repository, skipping what the go
// command would skip and what nobody wrote.
func findREADMEs(t *testing.T, root string) []string {
	t.Helper()

	var readmes []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root &&
				(strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "README.md" {
			readmes = append(readmes, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return readmes
}
