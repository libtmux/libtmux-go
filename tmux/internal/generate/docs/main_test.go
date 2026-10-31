package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func write(t *testing.T, root, relative, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const sourceWithRegions = `package main

func run() error {
	// docs:query
	live := tmux.TmuxFilter("#{==:#{session_name},work}")
	sessions, err := server.SearchSessions(ctx, &live)
	// docs:end
	_ = sessions
	return err
}
`

func TestRunFillsMarkedBlockFromSource(t *testing.T) {
	root := t.TempDir()
	write(t, root, "example/main.go", sourceWithRegions)
	readme := write(t, root, "README.md",
		"# Title\n\nBefore.\n\n<!-- docs:query -->\n<!-- docs:end -->\n\nAfter.\n")

	if err := run(root); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Title\n\nBefore.\n\n<!-- docs:query -->\n\n```go\n" +
		"live := tmux.TmuxFilter(\"#{==:#{session_name},work}\")\n" +
		"sessions, err := server.SearchSessions(ctx, &live)\n" +
		"```\n\n<!-- docs:end -->\n\nAfter.\n"
	if string(got) != want {
		t.Errorf("README =\n%q\nwant\n%q", got, want)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	root := t.TempDir()
	write(t, root, "example/main.go", sourceWithRegions)
	readme := write(t, root, "README.md", "<!-- docs:query -->\n<!-- docs:end -->\n")

	if err := run(root); err != nil {
		t.Fatalf("first run() error = %v", err)
	}
	first, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(root); err != nil {
		t.Fatalf("second run() error = %v", err)
	}
	second, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("second run changed the file:\n%q\nthen\n%q", first, second)
	}
}

func TestRunNoticesEditedSource(t *testing.T) {
	root := t.TempDir()
	source := write(t, root, "example/main.go", sourceWithRegions)
	readme := write(t, root, "README.md", "<!-- docs:query -->\n<!-- docs:end -->\n")
	if err := run(root); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	before, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}

	edited := strings.Replace(sourceWithRegions, "session_name", "window_name", 1)
	if err := os.WriteFile(source, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(root); err != nil {
		t.Fatalf("run() after edit error = %v", err)
	}
	after, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}

	if string(before) == string(after) {
		t.Fatal("editing the source left the markdown unchanged, so nothing catches drift")
	}
	if !strings.Contains(string(after), "window_name") {
		t.Errorf("README = %q, want it to carry the edited source", after)
	}
}

func TestRunRejectsMarkdownNamingNoRegion(t *testing.T) {
	root := t.TempDir()
	write(t, root, "README.md", "<!-- docs:absent -->\n<!-- docs:end -->\n")

	err := run(root)
	if err == nil {
		t.Fatal("run() error = nil, want a report of the missing region")
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("run() error = %v, want it to name the missing region", err)
	}
}

func TestRunRejectsDuplicateRegionNames(t *testing.T) {
	root := t.TempDir()
	write(t, root, "one/main.go", sourceWithRegions)
	write(t, root, "two/main.go", sourceWithRegions)

	err := run(root)
	if err == nil {
		t.Fatal("run() error = nil, want the duplicate reported")
	}
	if !strings.Contains(err.Error(), "already defined") {
		t.Errorf("run() error = %v, want it to report a duplicate", err)
	}
}

func TestRunLeavesUnmarkedMarkdownAlone(t *testing.T) {
	root := t.TempDir()
	write(t, root, "example/main.go", sourceWithRegions)
	untouched := "# Title\n\n```go\nhand := written{}\n```\n"
	readme := write(t, root, "README.md", untouched)

	if err := run(root); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != untouched {
		t.Errorf("README = %q, want an unmarked block left as written", got)
	}
}

const wholeProgram = `// Command hello prints a greeting.
package main

import "fmt"

func main() {
	// docs:greeting
	fmt.Println("hello")
	// docs:end
}
`

func TestRunQuotesAWholeFileWithoutItsRegionMarkers(t *testing.T) {
	root := t.TempDir()
	write(t, root, "example/main.go", wholeProgram)
	readme := write(t, root, "README.md",
		"<!-- docs:file example/main.go -->\n<!-- docs:end -->\n")

	if err := run(root); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	want := "<!-- docs:file example/main.go -->\n\n```go\n" +
		"// Command hello prints a greeting.\npackage main\n\nimport \"fmt\"\n\n" +
		"func main() {\n\tfmt.Println(\"hello\")\n}\n" +
		"```\n\n<!-- docs:end -->\n"
	if string(got) != want {
		t.Errorf("README =\n%q\nwant\n%q", got, want)
	}
}

func TestRunQuotesAnOutputFileAsText(t *testing.T) {
	root := t.TempDir()
	write(t, root, "example/output.txt", "hello\nexit status: 0\n")
	readme := write(t, root, "README.md",
		"It prints:\n\n<!-- docs:output example/output.txt -->\nstale\n<!-- docs:end -->\n")

	if err := run(root); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	got, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	want := "It prints:\n\n<!-- docs:output example/output.txt -->\n\n```text\n" +
		"hello\nexit status: 0\n```\n\n<!-- docs:end -->\n"
	if string(got) != want {
		t.Errorf("README =\n%q\nwant\n%q", got, want)
	}
}

func TestRunRejectsAWholeFileItCannotQuote(t *testing.T) {
	for _, test := range []struct {
		name, path, want string
	}{
		{"a parent directory", "../outside.go", "must stay inside"},
		{"an absolute path", "/etc/hostname", "must stay inside"},
		{"a file that does not exist", "example/absent.go", "absent.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "README.md", "<!-- docs:file "+test.path+" -->\n<!-- docs:end -->\n")

			err := run(root)
			if err == nil {
				t.Fatal("run() error = nil, want the path refused")
			}
			if !strings.Contains(err.Error(), test.want) ||
				!strings.Contains(err.Error(), "README.md:1") {
				t.Errorf("run() error = %v, want it to name README.md:1 and %q", err, test.want)
			}
		})
	}
}

func TestRunRejectsAWholeFileBlockThatNeverCloses(t *testing.T) {
	root := t.TempDir()
	write(t, root, "example/main.go", wholeProgram)
	write(t, root, "README.md", "<!-- docs:file example/main.go -->\n")

	err := run(root)
	if err == nil || !strings.Contains(err.Error(), "never closed") {
		t.Fatalf("run() error = %v, want the open block reported", err)
	}
}

func TestDedentRemovesSharedIndentationOnly(t *testing.T) {
	got := dedent([]string{"\t\tfirst", "", "\t\t\tnested", "\t\tlast"})
	want := []string{"first", "", "\tnested", "last"}
	if len(got) != len(want) {
		t.Fatalf("dedent() = %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("dedent()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestGivenCommentKeepsAShortListOnOneLine(t *testing.T) {
	got := givenComment("ctx context.Context; session tmux.Session")
	want := []string{"// Given: ctx context.Context; session tmux.Session"}
	if !slices.Equal(got, want) {
		t.Errorf("givenComment() = %q, want %q", got, want)
	}
}

func TestGivenCommentWrapsALongListWithoutSplittingADeclaration(t *testing.T) {
	given := "ctx context.Context; server tmux.Server; pane tmux.Pane; " +
		"payload *strings.Reader; archive string"
	got := givenComment(given)
	if len(got) < 2 {
		t.Fatalf("givenComment() = %q, want more than one line", got)
	}
	for _, line := range got {
		if len(line) > givenLineWidth {
			t.Errorf("givenComment() line %q is %d columns, want at most %d",
				line, len(line), givenLineWidth)
		}
		if !strings.HasPrefix(line, "// ") {
			t.Errorf("givenComment() line %q does not start a comment", line)
		}
	}
	for declaration := range strings.SplitSeq(given, "; ") {
		if !strings.Contains(strings.Join(got, " "), declaration) {
			t.Errorf("givenComment() = %q, want it to contain %q whole", got, declaration)
		}
	}
}

// A checkout nested under a hidden directory, such as a worktree under
// .claude, holds a copy of every region. The walk skips it as the go tool
// does, rather than finding each region twice.
func TestRegionsSkipHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	write(t, root, "example/main.go", sourceWithRegions)
	write(t, root, ".claude/worktrees/agent/example/main.go", sourceWithRegions)
	write(t, root, "_scratch/main.go", sourceWithRegions)
	write(t, root, ".claude/worktrees/agent/README.md", "# Copy\n")

	regions, err := collectRegions(root)
	if err != nil {
		t.Fatalf("collectRegions() error = %v, want the nested copies skipped", err)
	}
	if _, ok := regions["query"]; !ok {
		t.Fatalf("collectRegions() = %v, want the query region", regions)
	}
	documents, err := findMarkdown(root)
	if err != nil || len(documents) != 0 {
		t.Fatalf("findMarkdown() = (%v, %v), want no documents", documents, err)
	}
}
