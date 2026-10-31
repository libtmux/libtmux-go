package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// startsTmuxImports are the packages a package imports to reach a tmux
// server: the client, the harness, and the two programs built on them.
var startsTmuxImports = []string{
	tmuxModulePath + "/tmux",
	tmuxModulePath + "/tmux/tmuxtest",
	tmuxModulePath + "/mcp",
	tmuxModulePath + "/workspace",
}

// A test process that starts tmux without tmuxtest.Main keeps the TMUX,
// TMUX_PANE and LIBTMUX_SOCKET* its caller had and the caller's TMUX_TMPDIR,
// so a command that names no socket reaches the caller's own server. This
// holds every package that can start tmux to the one entry that prevents it.
func TestEveryPackageThatStartsTmuxEntersTheSandbox(t *testing.T) {
	t.Parallel()

	for _, finding := range sandboxEntryFindings(t, repositoryRoot(t), modules) {
		t.Error(finding)
	}
}

// The scan has to be able to fail, so it also runs over trees built to be
// wrong.
func TestTheSandboxEntryScanFlagsAPackageWithoutTheEntry(t *testing.T) {
	t.Parallel()

	const module = "module example.test/m\n\ngo 1.26\n"
	const (
		withEntry    = "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"github.com/libtmux/libtmux-go/tmux/tmuxtest\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(tmuxtest.Main(m)) }\n"
		withoutEntry = "package p\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"
		importsTmux  = "package p\n\nimport (\n\t\"testing\"\n\n\t_ \"github.com/libtmux/libtmux-go/tmux\"\n)\n\nfunc TestX(t *testing.T) {}\n"
		execsTmux    = "package p\n\nimport (\n\t\"os/exec\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = exec.CommandContext(t.Context(), \"tmux\", \"-V\") }\n"
		execsOther   = "package p\n\nimport (\n\t\"os/exec\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = exec.Command(\"go\", \"tmux\") }\n"
		// A TestMain that never calls Main is no entry.
		wrongMain = "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t_ \"github.com/libtmux/libtmux-go/tmux\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n"
		// Main called from a function that is not TestMain is no entry either.
		mainElsewhere = "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"github.com/libtmux/libtmux-go/tmux/tmuxtest\"\n)\n\nfunc helper(m *testing.M) int { return tmuxtest.Main(m) }\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n"
		// The client in a source file and the test in another, as in an example.
		clientInSource = "package main\n\nimport _ \"github.com/libtmux/libtmux-go/tmux\"\n"
		plainTest      = "package main\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"
	)
	for _, test := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"a TestMain through tmuxtest.Main", map[string]string{"p/p_test.go": withEntry, "p/p.go": "package p\n"}, nil},
		{"a package that never reaches tmux", map[string]string{"p/p_test.go": withoutEntry}, nil},
		{"an exec of something else", map[string]string{"p/p_test.go": execsOther}, nil},
		{"an import of the client", map[string]string{"p/p_test.go": importsTmux}, []string{"p"}},
		{"an exec of tmux", map[string]string{"p/p_test.go": execsTmux}, []string{"p"}},
		{"a TestMain that skips Main", map[string]string{"p/p_test.go": wrongMain}, []string{"p"}},
		{"Main called outside TestMain", map[string]string{"p/p_test.go": mainElsewhere}, []string{"p"}},
		{
			"a client in source and a test in another file",
			map[string]string{"p/main.go": clientInSource, "p/main_test.go": plainTest},
			[]string{"p"},
		},
		{"a client with no test", map[string]string{"p/main.go": clientInSource}, nil},
		{
			"a nested module is its own scan",
			map[string]string{"p/q/go.mod": "module example.test/q\n", "p/q/q_test.go": importsTmux},
			nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o600); err != nil {
				t.Fatal(err)
			}
			for name, content := range test.files {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			for _, finding := range sandboxEntryFindings(t, root, []string{"."}) {
				got = append(got, strings.SplitN(finding, ":", 2)[0])
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("sandboxEntryFindings() flagged %q, want %q", got, test.want)
			}
		})
	}
}

// sandboxEntryFindings returns one line for each package under the module
// directories of root that can start tmux and has a test but no TestMain calling
// tmuxtest.Main. A package can start tmux when any of its files imports one of
// [startsTmuxImports] or is one, or when a test file runs an executable named
// tmux. The scan reads source, so a program named by a variable is not seen.
func sandboxEntryFindings(t *testing.T, root string, moduleDirectories []string) []string {
	t.Helper()

	var findings []string
	for _, module := range moduleDirectories {
		moduleDirectory := filepath.Join(root, filepath.FromSlash(module))
		modulePath := declaredModule(t, moduleDirectory)
		err := filepath.WalkDir(moduleDirectory, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if path != moduleDirectory {
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
					name == "testdata" || name == "vendor" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			relative, err := filepath.Rel(moduleDirectory, path)
			if err != nil {
				return err
			}
			packagePath := modulePath
			if relative != "." {
				packagePath += "/" + filepath.ToSlash(relative)
			}
			if reason := entryProblem(t, path, packagePath); reason != "" {
				where, _ := filepath.Rel(root, path)
				findings = append(findings, filepath.ToSlash(where)+": "+reason)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return findings
}

// declaredModule reads the module path from the go.mod in directory.
func declaredModule(t *testing.T, directory string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(content)) {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(path)
		}
	}
	t.Fatalf("no module line in %s/go.mod", directory)
	return ""
}

// entryProblem explains why the package in directory, whose import path is
// packagePath, needs tmuxtest.Main and lacks it, or returns "".
func entryProblem(t *testing.T, directory, packagePath string) string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	startsTmux := slices.Contains(startsTmuxImports, packagePath)
	reason := ""
	if startsTmux {
		reason = "is a package that starts tmux"
	}
	hasTest, hasEntry := false, false
	fileSet := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fileSet, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		isTest := strings.HasSuffix(file, "_test.go")
		hasTest = hasTest || isTest
		for _, imported := range parsed.Imports {
			path, _ := strconv.Unquote(imported.Path.Value)
			if slices.Contains(startsTmuxImports, path) && !startsTmux {
				startsTmux, reason = true, "imports "+path[len(tmuxModulePath)+1:]
			}
		}
		if !isTest {
			continue
		}
		if !startsTmux && runsTmux(parsed) {
			startsTmux, reason = true, "runs tmux"
		}
		hasEntry = hasEntry || callsTmuxtestMain(parsed)
	}
	if !hasTest || !startsTmux || hasEntry {
		return ""
	}
	return reason + ", but no TestMain calls tmuxtest.Main"
}

// runsTmux reports whether file runs an executable named tmux: exec.Command or
// exec.CommandContext with that literal as the program to run. A literal
// further along is an argument to some other program.
func runsTmux(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := selector.X.(*ast.Ident); !ok || pkg.Name != "exec" {
			return true
		}
		program := -1
		switch selector.Sel.Name {
		case "Command":
			program = 0
		case "CommandContext":
			program = 1
		}
		if program < 0 || program >= len(call.Args) {
			return true
		}
		if literal, ok := call.Args[program].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			if value, err := strconv.Unquote(literal.Value); err == nil && value == "tmux" {
				found = true
			}
		}
		return true
	})
	return found
}

// callsTmuxtestMain reports whether file declares TestMain and that function
// calls Main on the tmuxtest package, under whatever name file imports it.
func callsTmuxtestMain(file *ast.File) bool {
	name := ""
	for _, imported := range file.Imports {
		if path, _ := strconv.Unquote(imported.Path.Value); path == tmuxModulePath+"/tmux/tmuxtest" {
			name = "tmuxtest"
			if imported.Name != nil {
				name = imported.Name.Name
			}
		}
	}
	if name == "" {
		return false
	}
	found := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name.Name != "TestMain" || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Main" {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == name {
					found = true
				}
			}
			return true
		})
	}
	return found
}
