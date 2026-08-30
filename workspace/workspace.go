// Package workspace loads and builds the supported subset of tmuxp YAML.
// Unknown fields are rejected. Python-dependent plugins and before_script are
// unsupported.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ErrInvalidWorkspace matches parse and validation failures, but not tmux
// command failures from [Build].
var ErrInvalidWorkspace = errors.New("workspace: invalid workspace")

// Workspace is one tmuxp-style session description.
type Workspace struct {
	// SessionName names the created session. It is required.
	SessionName string `yaml:"session_name"`
	// StartDirectory is the default working directory for every window and pane
	// that does not set its own.
	StartDirectory string `yaml:"start_directory"`
	// Environment is written to the session after its initial pane exists and
	// before later windows are created.
	Environment map[string]string `yaml:"environment"`
	// Options are applied after session creation. tmux resolves window-option
	// names against the session's current window.
	Options map[string]string `yaml:"options"`
	// GlobalOptions are applied after the initial window exists and before later
	// windows are created.
	GlobalOptions map[string]string `yaml:"global_options"`
	// CommandsBefore run in every pane before that pane's own commands. Window
	// and pane entries add to this list rather than replacing it.
	CommandsBefore []Command `yaml:"-"`
	// SuppressHistory prefixes commands with a space unless a window or pane
	// overrides it.
	SuppressHistory Bool `yaml:"suppress_history"`
	// Windows are created in order. A workspace needs at least one.
	Windows []Window `yaml:"windows"`
}

// Window is one window in a workspace.
type Window struct {
	// Name is the window name. Empty lets tmux choose.
	Name string `yaml:"window_name"`
	// Index requests an explicit winlink index. Nil uses the next free index.
	Index *int `yaml:"window_index"`
	// Layout is a tmux layout name applied after the panes exist.
	Layout string `yaml:"layout"`
	// StartDirectory overrides the workspace directory for this window.
	StartDirectory string `yaml:"start_directory"`
	// Shell replaces the command the window's first pane runs.
	Shell string `yaml:"window_shell"`
	// Focus selects this window once the workspace is built.
	Focus Bool `yaml:"focus"`
	// SuppressHistory overrides the workspace setting for this window's panes.
	SuppressHistory *Bool `yaml:"suppress_history"`
	// Options are applied after the window's initial pane exists and before
	// additional panes are split.
	Options map[string]string `yaml:"options"`
	// OptionsAfter are window options applied once the panes exist, for settings
	// that a later split would otherwise overwrite.
	OptionsAfter map[string]string `yaml:"options_after"`
	// Environment is written to the session after this window's initial pane
	// exists and before additional panes are split. Repeated names keep the last
	// value written.
	Environment map[string]string `yaml:"environment"`
	// CommandsBefore run in each of this window's panes before the pane's own
	// commands, after the workspace's.
	CommandsBefore []Command `yaml:"-"`
	// Panes are created in order. The first pane is the window's initial pane;
	// each later one splits the window.
	Panes []Pane `yaml:"panes"`

	line int
}

// Pane is one pane in a window. YAML accepts either a bare command string or a
// mapping, so "echo hello" and {shell_command: echo hello} are equivalent.
type Pane struct {
	// Commands run in the pane, in order.
	Commands []Command `yaml:"-"`
	// CommandsBefore run before Commands, after the window's.
	CommandsBefore []Command `yaml:"-"`
	// StartDirectory overrides the window directory for this pane.
	StartDirectory string `yaml:"start_directory"`
	// Shell replaces this pane's command. On the first pane it conflicts with
	// Window.Shell because tmux creates that pane with the window.
	Shell string `yaml:"shell"`
	// Focus selects this pane once its window is built.
	Focus Bool `yaml:"focus"`
	// SuppressHistory overrides the window setting for this pane.
	SuppressHistory *Bool `yaml:"suppress_history"`
	// Environment is written to the session after all panes in the window exist
	// and before this pane's commands are sent. Pane entries are processed in
	// order, so repeated names keep the last value written.
	Environment map[string]string `yaml:"environment"`
	// Enter applies to every command in this pane unless the command sets its
	// own. Nil presses Enter, matching tmuxp.
	Enter *Bool `yaml:"enter"`
	// SleepBefore delays before each of this pane's commands. YAML writes it
	// as a number of seconds, matching tmuxp, so 0.5 is half a second.
	SleepBefore time.Duration `yaml:"-"`
	// SleepAfter delays after each of this pane's commands, in the same units.
	SleepAfter time.Duration `yaml:"-"`
}

// Bool accepts YAML booleans and tmuxp's quoted spellings. It is exported so
// callers can construct optional *Bool fields.
type Bool bool

// UnmarshalYAML decodes a boolean written as a boolean or as a string.
func (f *Bool) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err != nil {
		var value bool
		if err := node.Decode(&value); err != nil {
			return fmt.Errorf("%w: line %d: not a boolean", ErrInvalidWorkspace, node.Line)
		}
		*f = Bool(value)
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true", "yes", "on", "1":
		*f = true
	case "false", "no", "off", "0", "":
		*f = false
	default:
		return fmt.Errorf("%w: line %d: %q is not a boolean", ErrInvalidWorkspace, node.Line, text)
	}
	return nil
}

// UnmarshalYAML decodes the workspace, then its shell_command_before, which
// accepts one command or a list.
func (w *Workspace) UnmarshalYAML(node *yaml.Node) error {
	if err := checkKnownFields(node,
		"session_name", "start_directory", "environment", "options",
		"global_options", "shell_command_before", "suppress_history", "windows",
	); err != nil {
		return err
	}
	type workspaceFields Workspace
	var fields struct {
		workspaceFields `yaml:",inline"`
		Before          yaml.Node `yaml:"shell_command_before"`
	}
	if err := node.Decode(&fields); err != nil {
		return nested(node.Line, err)
	}
	before, err := commandList(fields.Before, "shell_command_before")
	if err != nil {
		return err
	}
	*w = Workspace(fields.workspaceFields)
	w.CommandsBefore = before
	return nil
}

// UnmarshalYAML decodes the window, then its shell_command_before.
func (w *Window) UnmarshalYAML(node *yaml.Node) error {
	if err := checkKnownFields(node,
		"window_name", "window_index", "layout", "start_directory", "window_shell",
		"focus", "suppress_history", "options", "options_after", "environment",
		"shell_command_before", "panes",
	); err != nil {
		return err
	}
	type windowFields Window
	var fields struct {
		windowFields `yaml:",inline"`
		Before       yaml.Node `yaml:"shell_command_before"`
	}
	if err := node.Decode(&fields); err != nil {
		return nested(node.Line, err)
	}
	before, err := commandList(fields.Before, "shell_command_before")
	if err != nil {
		return err
	}
	*w = Window(fields.windowFields)
	w.line = node.Line
	w.CommandsBefore = before
	return nil
}

// UnmarshalYAML decodes a pane written as a bare command or as a mapping.
func (p *Pane) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var command string
		if err := node.Decode(&command); err != nil {
			return fmt.Errorf("%w: line %d: pane is neither a command nor a mapping",
				ErrInvalidWorkspace, node.Line)
		}
		if command != "" {
			p.Commands = []Command{{Command: command}}
		}
		return nil
	}

	if err := checkKnownFields(node,
		"shell_command", "shell_command_before", "start_directory", "shell",
		"focus", "suppress_history", "environment", "enter",
		"sleep_before", "sleep_after",
	); err != nil {
		return err
	}
	type paneFields Pane
	var fields struct {
		paneFields   `yaml:",inline"`
		ShellCommand yaml.Node `yaml:"shell_command"`
		Before       yaml.Node `yaml:"shell_command_before"`
		SleepBefore  float64   `yaml:"sleep_before"`
		SleepAfter   float64   `yaml:"sleep_after"`
	}
	if err := node.Decode(&fields); err != nil {
		return nested(node.Line, err)
	}
	commands, err := commandList(fields.ShellCommand, "shell_command")
	if err != nil {
		return err
	}
	before, err := commandList(fields.Before, "shell_command_before")
	if err != nil {
		return err
	}
	sleepBefore, err := seconds(fields.SleepBefore, node.Line, "sleep_before")
	if err != nil {
		return err
	}
	sleepAfter, err := seconds(fields.SleepAfter, node.Line, "sleep_after")
	if err != nil {
		return err
	}
	*p = Pane(fields.paneFields)
	p.Commands = commands
	p.CommandsBefore = before
	p.SleepBefore = sleepBefore
	p.SleepAfter = sleepAfter
	return nil
}

// Parse decodes one workspace document. Unknown fields are rejected so a
// misspelled key fails loudly instead of being dropped.
func Parse(document []byte) (Workspace, error) {
	decoder := yaml.NewDecoder(strings.NewReader(string(document)))
	decoder.KnownFields(true)

	var workspace Workspace
	if err := decoder.Decode(&workspace); err != nil {
		return Workspace{}, nested(0, err)
	}
	if err := workspace.Validate(); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

// Validate reports all problems joined under [ErrInvalidWorkspace].
func (w Workspace) Validate() error {
	var problems []error
	if strings.TrimSpace(w.SessionName) == "" {
		problems = append(problems,
			fmt.Errorf("%w: session_name is required", ErrInvalidWorkspace))
	}
	if len(w.Windows) == 0 {
		problems = append(problems,
			fmt.Errorf("%w: at least one window is required", ErrInvalidWorkspace))
	}
	for index, window := range w.Windows {
		problems = append(problems, windowProblems(index, window)...)
	}
	problems = append(problems, indexCollisions(w.Windows)...)
	return errors.Join(problems...)
}

// indexCollisions reports duplicate window indexes before [Build].
func indexCollisions(windows []Window) []error {
	claimed := map[int]int{}
	var problems []error
	for position, window := range windows {
		if window.Index == nil || *window.Index < 0 {
			continue
		}
		if first, taken := claimed[*window.Index]; taken {
			problems = append(problems, fmt.Errorf(
				"%w: %swindow %d (%q) asks for window_index %d, which window %d "+
					"(%q) already has",
				ErrInvalidWorkspace, windowPosition(window), position, window.Name,
				*window.Index, first, windows[first].Name))
			continue
		}
		claimed[*window.Index] = position
	}
	return problems
}

func windowProblems(index int, window Window) []error {
	var problems []error
	where := windowPosition(window)
	if window.Index != nil && *window.Index < 0 {
		problems = append(problems, fmt.Errorf(
			"%w: %swindow %d (%q) has a negative window_index",
			ErrInvalidWorkspace, where, index, window.Name))
	}
	if window.Layout != "" && !validLayout(window.Layout) {
		problems = append(problems, fmt.Errorf(
			"%w: %swindow %d (%q) has an unknown layout %q",
			ErrInvalidWorkspace, where, index, window.Name, window.Layout))
	}
	if window.Shell != "" && len(window.Panes) > 0 && window.Panes[0].Shell != "" {
		problems = append(problems, fmt.Errorf(
			"%w: %swindow %d (%q) sets window_shell and a shell on its first "+
				"pane, which are the same pane's command",
			ErrInvalidWorkspace, where, index, window.Name))
	}
	return problems
}

// refusedFields distinguishes unsupported tmuxp features from misspellings.
var refusedFields = map[string]string{
	"plugins": "it loads Python classes, which needs a Python runtime",
	"before_script": "it runs a script through tmuxp's own process handling, " +
		"which needs a Python runtime; run it yourself before calling Build",
}

func windowPosition(window Window) string {
	if window.line <= 0 {
		return ""
	}
	return "line " + strconv.Itoa(window.line) + ": "
}

// layoutNames includes layouts newer than the support floor. tmux remains the
// authority for availability on the running version.
var layoutNames = map[string]bool{
	"even-horizontal":          true,
	"even-vertical":            true,
	"main-horizontal":          true,
	"main-horizontal-mirrored": true,
	"main-vertical":            true,
	"main-vertical-mirrored":   true,
	"tiled":                    true,
}

// validLayout accepts named layouts and serialized layouts containing commas;
// tmux validates serialized layout syntax.
func validLayout(layout string) bool {
	return strings.Contains(layout, ",") || layoutNames[layout]
}

// nested adds the sentinel and source line once.
func nested(line int, err error) error {
	if errors.Is(err, ErrInvalidWorkspace) {
		return err
	}
	if line <= 0 {
		return fmt.Errorf("%w: %w", ErrInvalidWorkspace, err)
	}
	return fmt.Errorf("%w: line %d: %w", ErrInvalidWorkspace, line, err)
}

// checkKnownFields restores strict decoding because yaml.Node.Decode does not
// inherit the decoder's KnownFields setting.
func checkKnownFields(node *yaml.Node, allowed ...string) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	known := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		known[name] = true
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind != yaml.ScalarNode {
			continue
		}
		if !known[key.Value] {
			if reason, refused := refusedFields[key.Value]; refused {
				return fmt.Errorf("%w: line %d: %s is not supported: %s",
					ErrInvalidWorkspace, key.Line, key.Value, reason)
			}
			return fmt.Errorf("%w: line %d: unknown field %q",
				ErrInvalidWorkspace, key.Line, key.Value)
		}
	}
	return nil
}

// MissingDirectories returns absent start directories in document order. It is
// separate from [Workspace.Validate] because a prior command may create them.
// tmux otherwise falls back to the user's home directory without an error.
// Names beginning with ~ are resolved as tmux resolves them.
func (w Workspace) MissingDirectories() []string {
	var missing []string
	seen := make(map[string]bool)
	consider := func(directory string) {
		if directory == "" {
			return
		}
		resolved, err := resolveDirectory(directory)
		if err != nil || seen[directory] {
			return
		}
		seen[directory] = true
		if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
			missing = append(missing, directory)
		}
	}
	consider(w.StartDirectory)
	for _, window := range w.Windows {
		consider(window.StartDirectory)
		for _, pane := range window.Panes {
			consider(pane.StartDirectory)
		}
	}
	return missing
}

// resolveDirectory expands a leading ~ the way tmux does, so a check here
// agrees with what tmux will do with the same value.
func resolveDirectory(directory string) (string, error) {
	if directory != "~" && !strings.HasPrefix(directory, "~/") {
		return directory, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(directory, "~"), "/")), nil
}
