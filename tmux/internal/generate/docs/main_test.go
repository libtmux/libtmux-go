package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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
