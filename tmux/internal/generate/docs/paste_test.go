package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// These gates read the Markdown, not the Go it was generated from. A reader
// copies what the page shows, so that is the text held to account.

// wholeBlock is one docs:file or docs:output block as a document shows it.
type wholeBlock struct {
	kind, source, body string
}

// wholeBlocks lists the Go files one document quotes with docs:file and the
// text files it quotes with docs:output, in the order they appear.
func wholeBlocks(content string) []wholeBlock {
	var (
		blocks []wholeBlock
		open   *wholeBlock
		inside bool
	)
	for line := range strings.SplitSeq(content, "\n") {
		switch {
		case open == nil:
			if match := markdownWhole.FindStringSubmatch(line); match != nil {
				open = &wholeBlock{kind: match[1], source: match[2]}
				inside = false
			}
		case markdownEnd.MatchString(line):
			blocks = append(blocks, *open)
			open = nil
		case strings.HasPrefix(line, "```"):
			inside = !inside
		case inside:
			open.body += line + "\n"
		}
	}
	return blocks
}

func repositoryRoot(t *testing.T) (relative, absolute string) {
	t.Helper()
	relative = filepath.Join("..", "..", "..", "..")
	absolute, err := filepath.Abs(relative)
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return relative, absolute
}

func readDocuments(t *testing.T, root string) map[string]string {
	t.Helper()
	documents, err := findMarkdown(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := make(map[string]string, len(documents))
	for _, document := range documents {
		content, err := os.ReadFile(document)
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(root, document)
		if err != nil {
			t.Fatal(err)
		}
		contents[filepath.ToSlash(relative)] = string(content)
	}
	return contents
}

// TestPublishedMarkdownIsCurrent fails when a generated block is not what its
// source would produce, which is what editing it by hand or changing the
// source without running go generate leaves behind.
func TestPublishedMarkdownIsCurrent(t *testing.T) {
	root, _ := repositoryRoot(t)
	regions, err := collectRegions(root)
	if err != nil {
		t.Fatal(err)
	}
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		content := documents[name]
		document := filepath.Join(root, filepath.FromSlash(name))
		updated, err := renderMarkdown(root, document, content, regions)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if updated != content {
			t.Errorf("%s is out of date with its sources; run go generate ./...", name)
		}
	}
}

// TestEveryRegionIsQuoted fails when a region no document quotes is left in
// the source. Renaming or removing a block on the Markdown side otherwise
// leaves its region compiling and unread, and the code it showed is published
// nowhere.
func TestEveryRegionIsQuoted(t *testing.T) {
	root, _ := repositoryRoot(t)
	regions, err := collectRegions(root)
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := publishedRegions(root)
	if err != nil {
		t.Fatal(err)
	}
	// The generator's own tests hold regions inside string fixtures.
	fixtures := filepath.Join(root, "tmux", "internal", "generate", "docs") + string(filepath.Separator)
	for _, name := range slices.Sorted(maps.Keys(regions)) {
		if slices.Contains(quoted, name) || strings.HasPrefix(regions[name].origin, fixtures) {
			continue
		}
		origin, err := filepath.Rel(root, regions[name].origin)
		if err != nil {
			origin = regions[name].origin
		}
		t.Errorf("region %q in %s is quoted by no document; quote it or remove its markers",
			name, filepath.ToSlash(origin))
	}
}

var (
	markdownChecked = regexp.MustCompile(`^<!-- docs:checked (\S+) -->$`)
	codeSpan        = regexp.MustCompile("`([^`]+)`")
)

// squeeze removes what gofmt may put between the tokens of one expression: a
// long literal is broken over lines in its source and written on one in a
// table.
func squeeze(text string) string {
	text = strings.Join(strings.Fields(text), "")
	text = strings.ReplaceAll(text, ",}", "}")
	return strings.ReplaceAll(text, ",)", ")")
}

// uncheckedSpans returns the code spans in content's docs:checked blocks that
// are not text from the Go file each block names. A table of expressions has
// no room for whole programs, so its cells are held to one instead: every
// span is something that program compiles and runs.
func uncheckedSpans(root, content string) (spans []string, checked int, err error) {
	var source string
	open := false
	for line := range strings.SplitSeq(content, "\n") {
		if match := markdownChecked.FindStringSubmatch(line); match != nil && !open {
			if !filepath.IsLocal(filepath.FromSlash(match[1])) {
				return nil, 0, fmt.Errorf("path %q must stay inside the repository", match[1])
			}
			file, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(match[1])))
			if readErr != nil {
				return nil, 0, readErr
			}
			source, open = squeeze(string(file)), true
			continue
		}
		if !open {
			continue
		}
		if markdownEnd.MatchString(line) {
			open = false
			continue
		}
		for _, span := range codeSpan.FindAllStringSubmatch(line, -1) {
			checked++
			if !strings.Contains(source, squeeze(span[1])) {
				spans = append(spans, span[1])
			}
		}
	}
	if open {
		return nil, 0, errors.New("a docs:checked block is never closed")
	}
	return spans, checked, nil
}

// TestCheckedSpansAppearInTheirSource holds a docs:checked block to quoting
// only text from the program it names.
func TestCheckedSpansAppearInTheirSource(t *testing.T) {
	root, _ := repositoryRoot(t)
	total := 0
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		spans, checked, err := uncheckedSpans(root, documents[name])
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		total += checked
		for _, span := range spans {
			t.Errorf("%s shows `%s`, which is not in the program its block names",
				name, span)
		}
	}
	if total == 0 {
		t.Fatal("no docs:checked spans found; the scan is broken, not the docs")
	}
}

func TestUncheckedSpansAreFound(t *testing.T) {
	root := t.TempDir()
	source := "package main\n\nvar filter = tmux.PaneFilter{\n\tActive: new(true),\n\tIndex:  new(1),\n}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	document := "`outside` is not checked.\n\n<!-- docs:checked main.go -->\n" +
		"| `tmux.PaneFilter{Active: new(true), Index: new(1)}` | `new(2)` |\n" +
		"<!-- docs:end -->\n\n`after` is not checked.\n"

	spans, checked, err := uncheckedSpans(root, document)
	if err != nil {
		t.Fatal(err)
	}
	if checked != 2 || len(spans) != 1 || spans[0] != "new(2)" {
		t.Errorf("uncheckedSpans() = %q of %d, want only new(2) of 2", spans, checked)
	}
	if _, _, err := uncheckedSpans(root, "<!-- docs:checked main.go -->\n`x`\n"); err == nil {
		t.Error("uncheckedSpans() error = nil for a block that never closes")
	}
}

// hostileConfig is a tmux configuration unlike the one these programs were
// written against. A pasted program runs with it as the user's own, so output
// that depends on what the reader has configured fails here rather than on
// the reader's machine.
const hostileConfig = "set -g base-index 5\n" +
	"set -g pane-base-index 5\n" +
	"set-hook -g after-new-session 'new-window -d'\n"

// TestPastedProgramsBuildAndRun takes every quoted program back out of the
// Markdown, puts it alone in an empty module, builds it, runs it, and holds
// what it prints to the output block published beside it.
func TestPastedProgramsBuildAndRun(t *testing.T) {
	root, absRoot := repositoryRoot(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("a pasted program is only proven by running it, and no tmux is on PATH: %v", err)
	}

	seen := map[string]bool{}
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		blocks := wholeBlocks(documents[name])
		for _, program := range blocks {
			if program.kind != "file" {
				continue
			}
			printed, hasOutput := "", false
			for _, other := range blocks {
				if other.kind == "output" && path.Dir(other.source) == path.Dir(program.source) {
					printed, hasOutput = other.body, true
				}
			}
			isTest := strings.HasSuffix(program.source, "_test.go")
			if !isTest && !hasOutput {
				t.Errorf("%s quotes %s without the output it prints; add a docs:output block",
					name, program.source)
				continue
			}
			if seen[program.source+"\x00"+program.body+"\x00"+printed] {
				continue
			}
			seen[program.source+"\x00"+program.body+"\x00"+printed] = true
			t.Run(program.source, func(t *testing.T) {
				t.Parallel()
				pasteAndRun(t, absRoot, program, printed, isTest)
			})
		}
	}
	if len(seen) == 0 {
		t.Fatal("no docs:file blocks found; the scan is broken, not the docs")
	}
}

func pasteAndRun(t *testing.T, absRoot string, program wholeBlock, printed string, isTest bool) {
	t.Helper()
	dir := t.TempDir()
	name := "main.go"
	if isTest {
		name = path.Base(program.source)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(program.body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeModule(dir, absRoot); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(dir, "pasted")
	build := []string{"build", "-o", binary, "."}
	if isTest {
		build = []string{"test", "-c", "-o", binary, "."}
	}
	compile := exec.CommandContext(ctx, "go", build...)
	compile.Dir = dir
	compile.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod")
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("the pasted file does not build:\n%s", output)
	}

	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "tmux"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{
		filepath.Join(home, ".tmux.conf"),
		filepath.Join(home, ".config", "tmux", "tmux.conf"),
	} {
		if err := os.WriteFile(config, []byte(hostileConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// t.TempDir names the subtest, and a socket path may hold only 103 bytes.
	sockets, err := os.MkdirTemp("", "p") //nolint:usetesting // see above
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killServersAndRemove(sockets) })

	var arguments []string
	if isTest {
		arguments = []string{"-test.count=1"}
	}
	run := exec.CommandContext(ctx, binary, arguments...)
	run.Dir = dir
	run.Env = append(os.Environ(),
		"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"TMUX_TMPDIR="+sockets, "TMUX=", "TMUX_PANE=")
	var stdout, stderr bytes.Buffer
	run.Stdout, run.Stderr = &stdout, &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("the pasted file fails when run: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if !isTest && stdout.String() != printed {
		t.Errorf("the pasted program printed:\n%s\nthe page says it prints:\n%s",
			stdout.String(), printed)
	}
}

// killServersAndRemove ends whatever tmux a pasted program left behind before
// its socket directory goes.
func killServersAndRemove(sockets string) {
	_ = filepath.WalkDir(sockets, func(candidate string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&fs.ModeSocket == 0 {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", candidate, "kill-server").Run()
		return nil
	})
	_ = os.RemoveAll(sockets)
}

// TestPublishedOutputIsComparedByATest holds every docs:output block to a
// file some test compares with what the code printed. Without that, the block
// is handwritten text with a generator in front of it.
func TestPublishedOutputIsComparedByATest(t *testing.T) {
	root, _ := repositoryRoot(t)
	checked := map[string]bool{}
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		for _, block := range wholeBlocks(documents[name]) {
			if block.kind != "output" || checked[block.source] {
				continue
			}
			checked[block.source] = true
			if !comparedByATest(t, root, block.source) {
				t.Errorf("%s publishes %s, which no test beside it compares with real output",
					name, block.source)
			}
		}
	}
	if len(checked) == 0 {
		t.Fatal("no docs:output blocks found; the scan is broken, not the docs")
	}
}

// TestEveryExamplePagePublishesItsOutput holds each program under examples/ to
// showing, on its own page, the output its test compares. A page that states
// the output in a block of its own has nothing keeping the two alike.
func TestEveryExamplePagePublishesItsOutput(t *testing.T) {
	root, _ := repositoryRoot(t)
	programs, err := filepath.Glob(filepath.Join(root, "examples", "*", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) < 10 {
		t.Fatalf("found %d example programs, want at least 10; the scan is broken", len(programs))
	}
	for _, program := range programs {
		name := filepath.Base(filepath.Dir(program))
		page, err := os.ReadFile(filepath.Join(filepath.Dir(program), "README.md"))
		if err != nil {
			t.Errorf("examples/%s has no page: %v", name, err)
			continue
		}
		marker := "<!-- docs:output examples/" + name + "/output.txt -->"
		if !strings.Contains(string(page), marker) {
			t.Errorf("examples/%s/README.md does not publish the program's output; add\n\t%s",
				name, marker)
		}
	}
}

// comparedByATest reports whether a test file beside output names it. A file
// under testdata belongs to the tests one directory up. An example's
// output.txt is also compared by exampletest.Program and exampletest.Server,
// which name the file themselves.
func comparedByATest(t *testing.T, root, output string) bool {
	t.Helper()
	directory, base := path.Dir(output), path.Base(output)
	mentions := []string{`"` + base + `"`}
	if path.Base(directory) == "testdata" {
		mentions = []string{`"testdata/` + base + `"`, `"testdata", "` + base + `"`}
		directory = path.Dir(directory)
	} else if base == "output.txt" {
		mentions = append(mentions, "exampletest.Program(", "exampletest.Server(")
	}
	tests, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(directory), "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		content, err := os.ReadFile(test)
		if err != nil {
			t.Fatal(err)
		}
		for _, mention := range mentions {
			if strings.Contains(string(content), mention) {
				return true
			}
		}
	}
	return false
}
