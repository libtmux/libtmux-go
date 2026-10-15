// Command docs replaces marked Markdown blocks with matching Go source.
// A "<!-- docs:name -->" marker quotes a named region; "<!-- docs:file PATH -->"
// quotes a whole Go file, and "<!-- docs:output PATH -->" quotes a text file an
// example's test compares against what the program printed. PATH is slash
// separated and relative to the scanned root. Text outside marker pairs is
// unchanged.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	sourceRegionStart = regexp.MustCompile(`^\s*// docs:([a-z0-9-]+)(?:\s+given:(.*?))?\s*$`)
	sourceRegionEnd   = regexp.MustCompile(`^\s*// docs:end\s*$`)
	markdownStart     = regexp.MustCompile(`^<!-- docs:([a-z0-9-]+) -->$`)
	markdownEnd       = regexp.MustCompile(`^<!-- docs:end -->$`)
	// markdownWhole starts a block quoting a whole file: kind is "file" for Go
	// source and "output" for the text a program printed.
	markdownWhole = regexp.MustCompile(`^<!-- docs:(file|output) (\S+) -->$`)
)

// skipDirectories excludes generated fixtures and dependency caches.
var skipDirectories = map[string]bool{
	"testdata":     true,
	"node_modules": true,
}

// skipDirectory reports a directory below the root that holds no published
// source. The go tool ignores a name starting with "." or "_", so .git and a
// worktree checked out under .claude are skipped here as well; walking a
// nested checkout would find every region twice.
func skipDirectory(path, root string, entry fs.DirEntry) bool {
	if path == root {
		return false
	}
	name := entry.Name()
	return skipDirectories[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func main() {
	root := flag.String("root", ".", "repository root to scan")
	flag.Parse()

	if err := run(*root); err != nil {
		log.Fatalf("docs: %v", err)
	}
}

func run(root string) error {
	regions, err := collectRegions(root)
	if err != nil {
		return err
	}
	documents, err := findMarkdown(root)
	if err != nil {
		return err
	}

	rewritten := 0
	for _, document := range documents {
		changed, err := applyRegions(root, document, regions)
		if err != nil {
			return err
		}
		if changed {
			rewritten++
			relative, relErr := filepath.Rel(root, document)
			if relErr != nil {
				relative = document
			}
			fmt.Println("docs: rewrote", filepath.ToSlash(relative))
		}
	}
	if rewritten == 0 {
		fmt.Println("docs: every snippet already matched its source")
	}
	return nil
}

// region holds one named, dedented source block.
type region struct {
	origin string
	lines  []string
	// given names the bindings the block reads but does not create, as Go
	// declarations separated by semicolons:
	// "ctx context.Context; session tmux.Session". Published
	// text that leans on a binding it never introduces cannot be run by a
	// reader, so the block has to say what it assumes and the isolation test
	// holds it to exactly that list.
	given string
}

func collectRegions(root string) (map[string]region, error) {
	regions := map[string]region{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDirectory(path, root, entry) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		return collectFileRegions(path, regions)
	})
	return regions, err
}

func collectFileRegions(path string, regions map[string]region) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var (
		name    string
		given   string
		body    []string
		scanner = bufio.NewScanner(bytes.NewReader(content))
		line    int
	)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line++
		text := scanner.Text()
		// Check the end marker first because sourceRegionStart also accepts "end".
		if sourceRegionEnd.MatchString(text) {
			if name == "" {
				return fmt.Errorf("%s:%d: region ends without starting", path, line)
			}
			if existing, ok := regions[name]; ok {
				return fmt.Errorf("%s: region %q is already defined in %s", path, name, existing.origin)
			}
			regions[name] = region{origin: path, lines: dedent(body), given: given}
			name = ""
			continue
		}
		if match := sourceRegionStart.FindStringSubmatch(text); match != nil {
			if name != "" {
				return fmt.Errorf("%s:%d: region %q starts inside region %q", path, line, match[1], name)
			}
			name, given, body = match[1], strings.TrimSpace(match[2]), nil
			continue
		}
		if name != "" {
			body = append(body, text)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if name != "" {
		return fmt.Errorf("%s: region %q never ends", path, name)
	}
	return nil
}

// dedent removes shared leading tabs. Blank lines do not affect the measured
// indentation.
func dedent(lines []string) []string {
	shared := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		depth := len(line) - len(strings.TrimLeft(line, "\t"))
		if shared == -1 || depth < shared {
			shared = depth
		}
	}
	if shared <= 0 {
		return trimBlankEdges(lines)
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) >= shared {
			line = line[shared:]
		} else {
			line = strings.TrimLeft(line, "\t")
		}
		out = append(out, line)
	}
	return trimBlankEdges(out)
}

func trimBlankEdges(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func findMarkdown(root string) ([]string, error) {
	var documents []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDirectory(path, root, entry) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			documents = append(documents, path)
		}
		return nil
	})
	slices.Sort(documents)
	return documents, err
}

func applyRegions(root, path string, regions map[string]region) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated, err := renderMarkdown(root, path, string(content), regions)
	if err != nil {
		return false, err
	}
	if updated == string(content) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(updated), 0o644)
}

// renderMarkdown returns content with every marked block regenerated. It
// writes nothing, so a test can ask whether a document is current.
func renderMarkdown(root, path, content string, regions map[string]region) (string, error) {
	lines := strings.Split(content, "\n")

	var (
		out     []string
		name    string
		whole   []string // kind and path of an open whole-file block
		started int
	)
	for index, line := range lines {
		if name == "" && whole == nil {
			out = append(out, line)
			if match := markdownWhole.FindStringSubmatch(line); match != nil {
				whole, started = match[1:], index+1
			} else if match := markdownStart.FindStringSubmatch(line); match != nil &&
				!markdownEnd.MatchString(line) {
				name, started = match[1], index+1
			}
			continue
		}
		if !markdownEnd.MatchString(line) {
			continue
		}
		var block []string
		if whole != nil {
			quoted, err := quoteWhole(root, whole[0], whole[1])
			if err != nil {
				return "", fmt.Errorf("%s:%d: %w", path, started, err)
			}
			block = quoted
			whole = nil
		} else {
			source, ok := regions[name]
			if !ok {
				return "", fmt.Errorf("%s:%d: no region named %q in any Go file", path, started, name)
			}
			block = append(block, "```go")
			if source.given != "" {
				block = append(block, givenComment(source.given)...)
			}
			block = append(block, source.lines...)
			block = append(block, "```")
			name = ""
		}
		out = append(out, "")
		out = append(out, block...)
		out = append(out, "")
		out = append(out, line)
	}
	if name != "" {
		return "", fmt.Errorf("%s:%d: block %q is never closed", path, started, name)
	}
	if whole != nil {
		return "", fmt.Errorf("%s:%d: block %q is never closed", path, started, whole[1])
	}

	return strings.Join(out, "\n"), nil
}

// givenLineWidth is the repository's Markdown wrap column, applied here so a
// long given-list does not publish as one unwrapped comment line.
const givenLineWidth = 80

// givenComment renders a region's given-list as one or more "// " comment
// lines, breaking only between declarations so each stays whole.
func givenComment(given string) []string {
	declarations := strings.Split(given, "; ")
	lines := []string{"// Given:"}
	for index, declaration := range declarations {
		word := declaration
		if index < len(declarations)-1 {
			word += ";"
		}
		if lines[len(lines)-1] != "//" && len(lines[len(lines)-1])+1+len(word) > givenLineWidth {
			lines = append(lines, "//")
		}
		lines[len(lines)-1] += " " + word
	}
	return lines
}

// quoteWhole returns the fenced block for a whole-file marker. A Go file is
// quoted byte for byte except that its own region markers are dropped, so what
// a reader pastes is what compiles; an output file is quoted as plain text.
func quoteWhole(root, kind, relative string) ([]string, error) {
	if filepath.IsAbs(relative) || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return nil, fmt.Errorf("path %q must stay inside the repository", relative)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return nil, fmt.Errorf("docs:%s: %w", kind, err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	fence := "```text"
	if kind == "file" {
		fence = "```go"
		kept := lines[:0:0]
		for _, line := range lines {
			if sourceRegionEnd.MatchString(line) || sourceRegionStart.MatchString(line) {
				continue
			}
			kept = append(kept, line)
		}
		lines = kept
	}
	return append(append([]string{fence}, lines...), "```"), nil
}
