package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A documented command that names a flag or a subcommand the CLI does not
// have fails for the reader who pastes it, and nothing else reads it. These
// tests hold every `$ tmux-workspace ...` command in the repository's Markdown
// to the command tree the CLI exports.

type commandNode struct {
	Name           string        `json:"name"`
	Flags          []commandFlag `json:"flags"`
	InheritedFlags []commandFlag `json:"inherited_flags"`
	Children       []commandNode `json:"children"`
}

type commandFlag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand"`
	Type      string `json:"type"`
}

func (node commandNode) child(name string) (commandNode, bool) {
	for _, child := range node.Children {
		if child.Name == name {
			return child, true
		}
	}
	return commandNode{}, false
}

func (node commandNode) flag(name string, short bool) (commandFlag, bool) {
	for _, flag := range slices.Concat(node.Flags, node.InheritedFlags) {
		if short && flag.Shorthand == name || !short && flag.Name == name {
			return flag, true
		}
	}
	return commandFlag{}, false
}

// commandProblems reports what is wrong with the arguments after
// tmux-workspace: a subcommand or flag the tree does not have.
func commandProblems(tree commandNode, args []string) []string {
	var problems []string
	node, descending := tree, true
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--":
			return problems
		case strings.HasPrefix(arg, "--"):
			name, _, assigned := strings.Cut(arg[2:], "=")
			flag, ok := node.flag(name, false)
			if !ok {
				problems = append(problems, arg+" is not a flag of "+node.Name)
			} else if flag.Type != "bool" && !assigned {
				index++
			}
		case strings.HasPrefix(arg, "-") && arg != "-":
			// A cluster such as -yd is a run of boolean flags that may end in
			// one taking a value.
			for position, letter := range arg[1:] {
				flag, ok := node.flag(string(letter), true)
				if !ok {
					problems = append(problems, "-"+string(letter)+" is not a flag of "+node.Name)
					break
				}
				if flag.Type != "bool" {
					if position == len(arg)-2 {
						index++
					}
					break
				}
			}
		case descending:
			if child, ok := node.child(arg); ok {
				node = child
			} else if len(node.Children) > 0 {
				problems = append(problems, arg+" is not a command of "+node.Name)
				return problems
			} else {
				descending = false
			}
		}
	}
	return problems
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
		commands = append(commands, shellWords(pending))
		pending = ""
	}
	return commands
}

// shellWords splits a command line at spaces outside quotes.
func shellWords(line string) []string {
	var (
		words []string
		word  strings.Builder
		quote rune
		open  bool
	)
	for _, char := range line {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(char)
		case char == '\'' || char == '"':
			quote, open = char, true
		case char == ' ' || char == '\t':
			if open || word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
				open = false
			}
		default:
			word.WriteRune(char)
		}
	}
	if open || word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}

// workspaceArguments returns what follows tmux-workspace in a command, up to
// the first shell operator, and whether the command runs it at all.
func workspaceArguments(words []string) ([]string, bool) {
	start := slices.Index(words, "tmux-workspace")
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

func loadCommandTree(t *testing.T) commandNode {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	code, out, diagnostic := invoke(t, "--command-tree", "--json")
	var tree commandNode
	if code != 0 || json.Unmarshal([]byte(out), &tree) != nil || diagnostic != "" {
		t.Fatalf("command tree = %d %q %q", code, out, diagnostic)
	}
	return tree
}

func TestCommandProblemsFindsWhatIsWrong(t *testing.T) {
	tree := loadCommandTree(t)
	for _, test := range []struct {
		name    string
		command string
		want    []string
	}{
		{"load with a socket and detached", "load -L project -d ./project.yaml", nil},
		{"a long flag with a value", "load --socket-name=project --json -d x.yaml", nil},
		{"a global flag before the command", "--json ls", nil},
		{"a nested command", "import tmuxinator --json ./sample.yml", nil},
		{"a cluster ending in a boolean", "load -yd x.yaml", nil},
		{"an unknown flag", "load --bogus x.yaml", []string{"--bogus is not a flag of load"}},
		{"an unknown short flag", "ls -Z", []string{"-Z is not a flag of ls"}},
		{"a flag of another command", "ls --save-to x", []string{"--save-to is not a flag of ls"}},
		{"an unknown command", "frobnicate x.yaml", []string{"frobnicate is not a command of tmux-workspace"}},
		{"an unknown nested command", "import frobnicate x", []string{"frobnicate is not a command of import"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := commandProblems(tree, strings.Fields(test.command))
			if !slices.Equal(got, test.want) {
				t.Errorf("commandProblems(%q) = %q, want %q", test.command, got, test.want)
			}
		})
	}
}

func TestConsoleCommandsReadsContinuationsAndQuotes(t *testing.T) {
	markdown := "```console\n$ tmux-workspace load \\\n    -L project \\\n    ./a b.yaml\n```\n\n" +
		"```text\n$ tmux-workspace nope\n```\n\n```console\n$ FOO='x y' tmux-workspace ls | cat\n```\n"
	got := consoleCommands(markdown)
	want := [][]string{
		{"tmux-workspace", "load", "-L", "project", "./a", "b.yaml"},
		{"FOO=x y", "tmux-workspace", "ls", "|", "cat"},
	}
	if len(got) != len(want) {
		t.Fatalf("consoleCommands() = %q, want %q", got, want)
	}
	for index := range want {
		if !slices.Equal(got[index], want[index]) {
			t.Errorf("consoleCommands()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

// TestDocumentedCommandsExist holds every tmux-workspace command in the
// repository's Markdown to a subcommand and flags the CLI has.
func TestDocumentedCommandsExist(t *testing.T) {
	tree := loadCommandTree(t)
	root := filepath.Join("..", "..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
			args, ok := workspaceArguments(words)
			if !ok {
				continue
			}
			checked++
			for _, problem := range commandProblems(tree, args) {
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
