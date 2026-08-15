package integration

import (
	"os"
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
	} {
		if _, err := os.Stat(filepath.Join(root, directory, "README.md")); err != nil {
			t.Errorf("%s has no README.md", directory)
		}
	}
}

// TestEveryREADMELinkResolves gates documentation against rot.
//
// A link naming a file that moved still renders as a link. Nothing about
// reading the page reveals it, so the only way to find one is to check every
// target, which is what this does for the relative links -- the ones this
// repository controls and can therefore break by itself.
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
		directory := filepath.Dir(readme)
		relative, _ := filepath.Rel(root, readme)

		targets := make([]string, 0, 16)
		for _, match := range markdownLink.FindAllStringSubmatch(string(contents), -1) {
			targets = append(targets, match[1])
		}
		for _, match := range markdownReference.FindAllStringSubmatch(string(contents), -1) {
			targets = append(targets, match[1])
		}

		for _, target := range targets {
			// Only relative links are this repository's to keep working.
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") ||
				strings.HasPrefix(target, "mailto:") {
				continue
			}
			path, _, _ := strings.Cut(target, "#")
			if path == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(path))); err != nil {
				t.Errorf("%s links to %q, which does not exist", relative, target)
			}
		}
	}
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
