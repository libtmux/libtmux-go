// Command docs replaces marked Markdown blocks with matching Go source regions.
// Text outside marker pairs is unchanged.
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
	sourceRegionStart = regexp.MustCompile(`^\s*// docs:([a-z0-9-]+)\s*$`)
	sourceRegionEnd   = regexp.MustCompile(`^\s*// docs:end\s*$`)
	markdownStart     = regexp.MustCompile(`^<!-- docs:([a-z0-9-]+) -->$`)
	markdownEnd       = regexp.MustCompile(`^<!-- docs:end -->$`)
)

// skipDirectories excludes generated fixtures and dependency caches.
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

// region holds one named, dedented source block.
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
		// Check the end marker first because sourceRegionStart also accepts "end".
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
	slices.Sort(documents)
	return documents, err
}

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
			if match := markdownStart.FindStringSubmatch(line); match != nil &&
				!markdownEnd.MatchString(line) {
				name, started = match[1], index+1
			}
			continue
		}
		if !markdownEnd.MatchString(line) {
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
