package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
	markdownChecked = regexp.MustCompile(`^<!-- docs:checked (\S+(?: \S+)*) -->$`)
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

// afterLabel returns what line holds after "label: ", comparing the label
// without regard to spacing, so a heading need not copy the spacing of the
// line a program printed.
func afterLabel(line, label string) (string, bool) {
	want := squeeze(label) + ":"
	seen := 0
	for index, char := range line {
		if seen == len(want) {
			return strings.TrimSpace(line[index:]), true
		}
		if char == ' ' || char == '\t' {
			continue
		}
		if seen >= len(want) || string(char) != want[seen:seen+len(string(char))] {
			return "", false
		}
		seen += len(string(char))
	}
	return "", false
}

// headedByCode matches a heading that opens with a code span, linked or not.
var headedByCode = regexp.MustCompile("^#+ \\[?`([^`]+)`")

// uncheckedSpans returns what is wrong with content's docs:checked blocks. A
// page that leads with expressions has no room for a whole program per
// expression, so each block is held to one instead:
//
//   - every code span is text from a file the block names, which makes it
//     something that program compiles and runs, or something it printed;
//   - a section headed by a code span states, in a span of its own, what a
//     named file prints after "<that span>: ", when a file prints such a line.
//
// The second rule is what ties a result to its expression: without it a stale
// result passes for as long as the same text appears anywhere in the output.
func uncheckedSpans(root, content string) (problems []string, checked int, err error) {
	var (
		open     bool
		source   string
		printed  []string
		heading  string
		expected string
		stated   bool
	)
	endSection := func() {
		if heading != "" && expected != "" && !stated {
			problems = append(problems,
				"the section headed `"+heading+"` does not state what it gave, `"+expected+"`")
		}
		heading, expected, stated = "", "", false
	}
	for line := range strings.SplitSeq(content, "\n") {
		if match := markdownChecked.FindStringSubmatch(line); match != nil && !open {
			var named strings.Builder
			printed, open = nil, true
			for name := range strings.FieldsSeq(match[1]) {
				if !filepath.IsLocal(filepath.FromSlash(name)) {
					return nil, 0, fmt.Errorf("path %q must stay inside the repository", name)
				}
				file, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
				if readErr != nil {
					return nil, 0, readErr
				}
				named.WriteString(squeeze(string(file)) + "\n")
				printed = append(printed, strings.Split(string(file), "\n")...)
			}
			source = named.String()
			continue
		}
		if !open {
			continue
		}
		if markdownEnd.MatchString(line) {
			endSection()
			open = false
			continue
		}
		if match := headedByCode.FindStringSubmatch(line); match != nil {
			endSection()
			heading = match[1]
			for _, candidate := range printed {
				if result, ok := afterLabel(candidate, heading); ok {
					expected = result
				}
			}
		}
		isHeading := headedByCode.MatchString(line)
		for index, span := range codeSpan.FindAllStringSubmatch(line, -1) {
			checked++
			if !strings.Contains(source, squeeze(span[1])) {
				problems = append(problems, "`"+span[1]+"` is not in the files its block names")
			}
			// The heading's own span is the expression, not its result.
			if isHeading && index == 0 {
				continue
			}
			if heading != "" && squeeze(span[1]) == squeeze(expected) {
				stated = true
			}
		}
	}
	if open {
		return nil, 0, errors.New("a docs:checked block is never closed")
	}
	return problems, checked, nil
}

// TestCheckedSpansAppearInTheirSource holds a docs:checked block to quoting
// only text from the program it names.
func TestCheckedSpansAppearInTheirSource(t *testing.T) {
	root, _ := repositoryRoot(t)
	total := 0
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		problems, checked, err := uncheckedSpans(root, documents[name])
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		total += checked
		for _, problem := range problems {
			t.Errorf("%s: %s", name, problem)
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
	printed := "tmux.PaneFilter{Active: new(true), Index: new(1)}: editor.0 logs.1\nother: logs.0\n"
	if err := os.WriteFile(filepath.Join(root, "output.txt"), []byte(printed), 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		open    = "`outside` is not checked.\n\n<!-- docs:checked main.go output.txt -->\n"
		heading = "### `tmux.PaneFilter{Active: new(true), Index: new(1)}`: a title\n\n"
		closing = "<!-- docs:end -->\n\n`after` is not checked.\n"
	)
	for _, test := range []struct {
		name, body string
		want       []string
	}{
		{"states its result", "It selects `editor.0 logs.1`.\n", nil},
		{
			"a span from nowhere", "It selects `editor.0 logs.1`, not `new(2)`.\n",
			[]string{"`new(2)` is not in the files its block names"},
		},
		{
			"another line's result", "It selects `logs.0`.\n",
			[]string{"the section headed `tmux.PaneFilter{Active: new(true), Index: new(1)}` " +
				"does not state what it gave, `editor.0 logs.1`"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, _, err := uncheckedSpans(root, open+heading+test.body+closing)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(problems, test.want) {
				t.Errorf("uncheckedSpans() = %q, want %q", problems, test.want)
			}
		})
	}
	if _, _, err := uncheckedSpans(root, "<!-- docs:checked main.go -->\n`x`\n"); err == nil {
		t.Error("uncheckedSpans() error = nil for a block that never closes")
	}
}

var referenceLink = regexp.MustCompile(
	`https://libtmux\.org/en/go/latest/(?:workspace/|mcp/)?reference/([a-z0-9-]+)/`)

// referenceSlugs returns the page names the reference site gives this
// module's exported API: the package, then the identifier, then a method or
// field of it, lowercased and joined by hyphens.
func referenceSlugs(t *testing.T, root string) map[string]bool {
	t.Helper()
	slugs := map[string]bool{}
	for _, directory := range []string{
		"tmux", "tmuxq", filepath.Join("tmux", "tmuxtest"), "workspace", "mcp",
	} {
		sources, err := filepath.Glob(filepath.Join(root, directory, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		fileSet := token.NewFileSet()
		for _, source := range sources {
			if strings.HasSuffix(source, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fileSet, source, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			add := func(parts ...string) {
				slugs[strings.ToLower(file.Name.Name+"-"+strings.Join(parts, "-"))] = true
			}
			for _, declaration := range file.Decls {
				switch declaration := declaration.(type) {
				case *ast.FuncDecl:
					if !declaration.Name.IsExported() {
						continue
					}
					if declaration.Recv == nil {
						add(declaration.Name.Name)
						continue
					}
					receiver := declaration.Recv.List[0].Type
					if star, ok := receiver.(*ast.StarExpr); ok {
						receiver = star.X
					}
					if index, ok := receiver.(*ast.IndexExpr); ok {
						receiver = index.X
					}
					if named, ok := receiver.(*ast.Ident); ok && named.IsExported() {
						add(named.Name, declaration.Name.Name)
					}
				case *ast.GenDecl:
					for _, spec := range declaration.Specs {
						switch spec := spec.(type) {
						case *ast.ValueSpec:
							for _, value := range spec.Names {
								if value.IsExported() {
									add(value.Name)
								}
							}
						case *ast.TypeSpec:
							if !spec.Name.IsExported() {
								continue
							}
							add(spec.Name.Name)
							structure, ok := spec.Type.(*ast.StructType)
							if !ok {
								continue
							}
							for _, field := range structure.Fields.List {
								for _, fieldName := range field.Names {
									if fieldName.IsExported() {
										add(spec.Name.Name, fieldName.Name)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return slugs
}

// TestReferenceLinksNameExportedAPI holds every link to the reference site to
// an identifier this module exports. A renamed function leaves its old link
// rendering as a link, and only following it shows the page is gone; this
// finds it without the network.
func TestReferenceLinksNameExportedAPI(t *testing.T) {
	root, _ := repositoryRoot(t)
	slugs := referenceSlugs(t, root)
	if !slugs["tmux-server"] || !slugs["tmuxq-matching"] || !slugs["tmux-panefilter-active"] {
		t.Fatal("the scan of exported API is broken: Server, Matching, or PaneFilter.Active is missing")
	}
	checked := 0
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		for _, match := range referenceLink.FindAllStringSubmatch(documents[name], -1) {
			checked++
			if !slugs[match[1]] {
				t.Errorf("%s links to reference/%s/, which names nothing this module exports",
					name, match[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no reference links found; the scan is broken, not the docs")
	}
}

var (
	linkLabelUse        = regexp.MustCompile(`\]\[([a-z0-9_-]+)\]`)
	linkLabelDefinition = regexp.MustCompile(`(?m)^\[([a-z0-9_-]+)\]: `)
)

// TestEveryLinkLabelIsDefined holds a README's reference-style links to their
// definitions. A label with none renders as its own brackets rather than as a
// link, and a definition nothing uses is a link that was removed halfway.
func TestEveryLinkLabelIsDefined(t *testing.T) {
	root, _ := repositoryRoot(t)
	documents := readDocuments(t, root)
	for _, name := range slices.Sorted(maps.Keys(documents)) {
		if path.Base(name) != "README.md" {
			continue
		}
		used, defined := map[string]bool{}, map[string]bool{}
		for _, match := range linkLabelUse.FindAllStringSubmatch(documents[name], -1) {
			used[match[1]] = true
		}
		for _, match := range linkLabelDefinition.FindAllStringSubmatch(documents[name], -1) {
			defined[match[1]] = true
		}
		for _, label := range slices.Sorted(maps.Keys(used)) {
			if !defined[label] {
				t.Errorf("%s uses the link label [%s] and never defines it", name, label)
			}
		}
		for _, label := range slices.Sorted(maps.Keys(defined)) {
			if !used[label] && !strings.Contains(documents[name], "["+label+"]") {
				t.Errorf("%s defines the link label [%s] and never uses it", name, label)
			}
		}
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
// what it prints to the output block published beside it. A program in a
// module with dependencies of its own is run by that module's suite instead,
// and is held here to being one file.
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
			if hasItsOwnDependencies(program.source) {
				// Pasting it would need that module's dependencies from the
				// network. Its own suite runs it against the published
				// output; what is left to hold here is that the quoted file
				// is the whole program.
				for _, other := range programFiles(t, root, path.Dir(program.source)) {
					if other != path.Base(program.source) {
						t.Errorf("%s quotes %s as a whole program, but %s is part of it too",
							name, program.source, other)
					}
				}
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

// programFiles names the Go files a directory compiles into its program.
func programFiles(t *testing.T, root, directory string) []string {
	t.Helper()
	sources, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(directory), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, source := range sources {
		if !strings.HasSuffix(source, "_test.go") {
			names = append(names, filepath.Base(source))
		}
	}
	return names
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
	// t.TempDir names the subtest, and a socket path may hold only 103 bytes,
	// of which macOS's per-user TMPDIR alone takes about sixty, so the sockets
	// go under /tmp where there is one.
	base := ""
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		base = "/tmp"
	}
	sockets, err := os.MkdirTemp(base, "p") //nolint:usetesting // see above
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
