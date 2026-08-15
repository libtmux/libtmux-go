// Command docs keeps the Go in the repository's markdown identical to Go that
// compiles.
//
// A README that carries hand-copied code drifts from the package it describes,
// and nothing catches it: the code is checked by a compiler, a linter, a race
// detector and eight tmux releases, while the copy of it in the README is
// checked by nobody. This removes the copy. A region of a real file is named
// where it is written:
//
//	// docs:filter-pushdown
//	filter := tmux.PaneFilter{Active: tmux.Ptr(true)}
//	// docs:end
//
// and a markdown file asks for it by that name:
//
//	<!-- docs:filter-pushdown -->
//	<!-- docs:end -->
//
// Everything between those two lines is replaced by a fenced block holding
// exactly the region's lines. The result is checked in, and regenerating it
// must leave the tree unchanged, which is the same gate the other generators in
// this directory are held to.
//
// Markdown outside a marker pair is never touched, so a block that no program
// backs stays hand-written and says so by not being marked.
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
	"sort"
	"strings"
)

var (
	sourceRegionStart = regexp.MustCompile(`^\s*// docs:([a-z0-9-]+)\s*$`)
	sourceRegionEnd   = regexp.MustCompile(`^\s*// docs:end\s*$`)
	markdownStart     = regexp.MustCompile(`^<!-- docs:([a-z0-9-]+) -->$`)
	markdownEnd       = regexp.MustCompile(`^<!-- docs:end -->$`)
)

// skipDirectories are not searched. Generated fixtures and dependency caches
// hold Go that is not this repository's to quote.
var skipDirectories = map[string]bool{
	".git":         true,
	"testdata":     true,
	"node_modules": true,
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
		changed, err := applyRegions(document, regions)
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

// region is the body of one named block, with the indentation it was written
// at removed so it reads as top-level code in a fenced block.
type region struct {
	origin string
	lines  []string
}

func collectRegions(root string) (map[string]region, error) {
	regions := map[string]region{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDirectories[entry.Name()] {
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
		body    []string
		scanner = bufio.NewScanner(bytes.NewReader(content))
		line    int
	)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line++
		text := scanner.Text()
		// The end marker is tested first: "end" is also a name the start
		// pattern accepts, so testing that first would read every region as
		// opening a second one.
		if sourceRegionEnd.MatchString(text) {
			if name == "" {
				return fmt.Errorf("%s:%d: region ends without starting", path, line)
			}
			if existing, ok := regions[name]; ok {
				return fmt.Errorf("%s: region %q is already defined in %s", path, name, existing.origin)
			}
			regions[name] = region{origin: path, lines: dedent(body)}
			name = ""
			continue
		}
		if match := sourceRegionStart.FindStringSubmatch(text); match != nil {
			if name != "" {
				return fmt.Errorf("%s:%d: region %q starts inside region %q", path, line, match[1], name)
			}
			name, body = match[1], nil
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

// dedent removes the shared leading tabs, so a region written inside a function
// reads as code rather than as an indented quotation. Blank lines carry no
// indentation and are ignored when measuring it.
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
			if skipDirectories[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			documents = append(documents, path)
		}
		return nil
	})
	sort.Strings(documents)
	return documents, err
}

// applyRegions rewrites every marked block in one markdown file, reporting
// whether anything changed.
func applyRegions(path string, regions map[string]region) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lines := strings.Split(string(content), "\n")

	var (
		out     []string
		name    string
		started int
	)
	for index, line := range lines {
		if name == "" {
			out = append(out, line)
			// As in the Go scanner, an end marker outside a block is not the
			// start of one named "end".
			if match := markdownStart.FindStringSubmatch(line); match != nil &&
				!markdownEnd.MatchString(line) {
				name, started = match[1], index+1
			}
			continue
		}
		if !markdownEnd.MatchString(line) {
			// The previous body is dropped; it is about to be written again
			// from the source of truth.
			continue
		}
		source, ok := regions[name]
		if !ok {
			return false, fmt.Errorf("%s:%d: no region named %q in any Go file", path, started, name)
		}
		out = append(out, "")
		out = append(out, "```go")
		out = append(out, source.lines...)
		out = append(out, "```")
		out = append(out, "")
		out = append(out, line)
		name = ""
	}
	if name != "" {
		return false, fmt.Errorf("%s:%d: block %q is never closed", path, started, name)
	}

	updated := strings.Join(out, "\n")
	if updated == string(content) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(updated), 0o644)
}
