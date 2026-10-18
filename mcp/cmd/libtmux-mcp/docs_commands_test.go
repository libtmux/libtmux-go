package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// A documented command that names a flag or an environment variable this
// command does not read fails for the reader who pastes it, and nothing else
// reads it. These tests hold every libtmux-mcp command in the repository's
// Markdown to what main.go declares and the module reads.

// declaredFlags returns the flags main.go registers with the flag package,
// each mapped to whether it takes a value.
func declaredFlags(t *testing.T) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := selector.X.(*ast.Ident); !ok || pkg.Name != "flag" {
			return true
		}
		name, ok := call.Args[0].(*ast.BasicLit)
		if !ok || name.Kind != token.STRING {
			return true
		}
		unquoted, err := strconv.Unquote(name.Value)
		if err != nil {
			t.Fatal(err)
		}
		flags[unquoted] = selector.Sel.Name != "Bool"
		return true
	})
	return flags
}

// flagProblems reports each argument after libtmux-mcp that is not a declared
// flag. The flag package takes -name and --name alike.
func flagProblems(declared map[string]bool, args []string) []string {
	var problems []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name, _, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		takesValue, ok := declared[name]
		switch {
		case !ok:
			problems = append(problems, arg+" is not a flag of libtmux-mcp")
		case takesValue && !assigned:
			index++
		}
	}
	return problems
}

var environmentName = regexp.MustCompile(`^(LIBTMUX_[A-Z0-9_]+)=`)

// environmentProblems reports each LIBTMUX_ variable a command sets that the
// module's source does not name, whether it leads the command or follows -e.
func environmentProblems(source string, words []string) []string {
	var problems []string
	for _, word := range words {
		if match := environmentName.FindStringSubmatch(word); match != nil &&
			!strings.Contains(source, `"`+match[1]+`"`) {
			problems = append(problems, match[1]+" is not read by the mcp module")
		}
	}
	return problems
}

// commandAfter returns what follows the first word equal to program, up to
// the first shell operator.
func commandAfter(words []string, program string) ([]string, bool) {
	start := slices.Index(words, program)
	if start < 0 {
		return nil, false
	}
	args := words[start+1:]
	for index, word := range args {
		if slices.Contains([]string{"|", "||", "&&", ";", ">", ">>", "<"}, word) {
			return args[:index], true
		}
	}
	return args, true
}

// consoleCommands returns the words of every command a ```console block shows.
// A command starts at a "$ " line and runs over lines ending in a backslash.
func consoleCommands(markdown string) [][]string {
	var (
		commands [][]string
		inside   bool
		pending  string
	)
	for line := range strings.SplitSeq(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inside = trimmed == "```console"
			pending = ""
		case !inside:
		case pending != "":
			pending += " " + trimmed
		case strings.HasPrefix(trimmed, "$ "):
			pending = strings.TrimPrefix(trimmed, "$ ")
		}
		if pending == "" {
			continue
		}
		if strings.HasSuffix(pending, `\`) {
			pending = strings.TrimSuffix(pending, `\`)
			continue
		}
		commands = append(commands, strings.Fields(pending))
		pending = ""
	}
	return commands
}

func TestDocumentedFlagsAreDeclared(t *testing.T) {
	declared := map[string]bool{"socket-name": true, "doctor": false, "tools": false}
	for _, test := range []struct {
		name    string
		command string
		want    []string
	}{
		{"a flag with a value", "-doctor -socket-name my-application", nil},
		{"a double dash and an assignment", "--socket-name=x --tools", nil},
		{"a value that looks like a flag name", "-socket-name tools", nil},
		{"an undeclared flag", "-doctor -socket-nam x", []string{"-socket-nam is not a flag of libtmux-mcp"}},
		{"a boolean given a value", "-tools x -verbose", []string{"-verbose is not a flag of libtmux-mcp"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := flagProblems(declared, strings.Fields(test.command))
			if !slices.Equal(got, test.want) {
				t.Errorf("flagProblems(%q) = %q, want %q", test.command, got, test.want)
			}
		})
	}
}

func TestDocumentedEnvironmentIsRead(t *testing.T) {
	source := `const toolsets = "LIBTMUX_TOOLSETS"`
	for _, test := range []struct {
		command string
		want    []string
	}{
		{"LIBTMUX_TOOLSETS=inspect libtmux-mcp -tools", nil},
		{"claude mcp add tmux -e LIBTMUX_TOOLSETS=inspect -- libtmux-mcp", nil},
		{"LIBTMUX_TOOLSET=inspect libtmux-mcp -tools", []string{"LIBTMUX_TOOLSET is not read by the mcp module"}},
	} {
		got := environmentProblems(source, strings.Fields(test.command))
		if !slices.Equal(got, test.want) {
			t.Errorf("environmentProblems(%q) = %q, want %q", test.command, got, test.want)
		}
	}
}

// TestDocumentedCommandsExist holds every libtmux-mcp command in the
// repository's Markdown to a flag and an environment variable that exist.
func TestDocumentedCommandsExist(t *testing.T) {
	declared := declaredFlags(t)
	if _, ok := declared["doctor"]; !ok || !declared["socket-name"] {
		t.Fatalf("declaredFlags() = %v, want socket-name taking a value and doctor a boolean", declared)
	}
	module := filepath.Join("..", "..")
	var source strings.Builder
	sources, err := filepath.Glob(filepath.Join(module, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		source.Write(content)
	}

	root := filepath.Join(module, "..")
	checked := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root &&
				(strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, words := range consoleCommands(string(content)) {
			problems := environmentProblems(source.String(), words)
			if args, ok := commandAfter(words, "libtmux-mcp"); ok {
				checked++
				problems = append(problems, flagProblems(declared, args)...)
			}
			for _, problem := range problems {
				t.Errorf("%s shows %q: %s", filepath.ToSlash(path), strings.Join(words, " "), problem)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 {
		t.Fatalf("checked %d documented commands, want at least 10; the scan is broken", checked)
	}
}
