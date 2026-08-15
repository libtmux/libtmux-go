// Package workspace loads tmuxp-style YAML workspace files and builds them
// with the tmux package.
//
// It is a consumer of the tmux module rather than part of it: the tmux module
// takes no runtime dependency, while parsing YAML needs one, so this lives in
// its own module. Compatibility with tmuxp is deliberate but partial. The
// fields below are the ones tmuxp's own examples use; anything else in a file
// is rejected rather than silently ignored, so a workspace that loads is a
// workspace that was understood.
//
// tmuxp features that require a Python runtime are out of scope and stay
// rejected: plugins loads Python classes, and before_script runs an external
// script through tmuxp's own process handling.
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

// ErrInvalidWorkspace identifies a workspace file that could not be decoded or
// that Validate rejects. It is matched by errors.Is.
//
// It does not cover a failure tmux reports while [Build] runs. tmux refusing a
// layout name or an option name is a command failure, so classify those with
// the tmux package's own errors rather than with this one.
var ErrInvalidWorkspace = errors.New("workspace: invalid workspace")

// Workspace is one tmuxp-style session description.
type Workspace struct {
	// SessionName names the created session. It is required.
	SessionName string `yaml:"session_name"`
	// StartDirectory is the default working directory for every window and pane
	// that does not set its own.
	StartDirectory string `yaml:"start_directory"`
	// Environment is set on the session before its windows are created.
	Environment map[string]string `yaml:"environment"`
	// Options are applied to the session after it exists. Keys are tmux option
	// names. A name from tmux's window table, such as main-pane-height, is
	// accepted here as tmux accepts it, and lands on the session's current
	// window the way tmux would put it there.
	Options map[string]string `yaml:"options"`
	// GlobalOptions are applied at tmux's global scope before the windows are
	// created, so a window can inherit them.
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
	// Options are window options applied before this window's panes are created.
	Options map[string]string `yaml:"options"`
	// OptionsAfter are window options applied once the panes exist, for settings
	// that a later split would otherwise overwrite.
	OptionsAfter map[string]string `yaml:"options_after"`
	// Environment is set on the session before this window is created. tmux
	// keeps one environment per session rather than one per window, so a name
	// used by more than one window keeps the last value written and every
	// process started later in that session sees it.
	Environment map[string]string `yaml:"environment"`
	// CommandsBefore run in each of this window's panes before the pane's own
	// commands, after the workspace's.
	CommandsBefore []Command `yaml:"-"`
	// Panes are created in order. The first pane is the window's initial pane;
	// each later one splits the window.
	Panes []Pane `yaml:"panes"`

	// line is where this window began in the document, so a rejection can name
	// the place to fix rather than only the window's position in a list.
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
	// Shell replaces the command this pane runs. On a window's first pane it
	// is the window's command, because tmux creates that pane with the window;
	// setting it alongside window_shell is rejected rather than resolved.
	Shell string `yaml:"shell"`
	// Focus selects this pane once its window is built.
	Focus Bool `yaml:"focus"`
	// SuppressHistory overrides the window setting for this pane.
	SuppressHistory *Bool `yaml:"suppress_history"`
	// Environment is set on the session before this pane is created. It is the
	// session's environment rather than the pane's, so a name used by more
	// than one pane keeps the last value written, including for panes the user
	// opens afterwards.
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

// Bool is a YAML boolean that also accepts tmuxp's quoted spellings, because
// tmuxp workspaces in the wild write focus: "true" as often as focus: true.
//
// It is exported so that every field holding one can be written in Go as well
// as read from a file: an optional setting is a *Bool, and a pointer to an
// unexported type is not something a caller outside this package can make.
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
	// shell_command and shell_command_before each accept one command or a list,
	// so they are decoded separately from the plain fields.
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

// Validate reports whether the workspace describes something buildable.
//
// Every problem it finds is reported, not the first, because fixing a file one
// complaint per run is the slowest way to learn what is wrong with it. The
// result matches [ErrInvalidWorkspace] however many problems it carries.
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
	return errors.Join(problems...)
}

// windowProblems reports everything wrong with one window.
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

// refusedFields are tmuxp fields this module will not grow, as opposed to
// names it does not recognise. Reporting them as unknown reads as a typo and
// sends a reader looking for the spelling that works.
var refusedFields = map[string]string{
	"plugins": "it loads Python classes, which needs a Python runtime",
	"before_script": "it runs a script through tmuxp's own process handling, " +
		"which needs a Python runtime; run it yourself before calling Build",
}

// position renders where a window began, for a rejection that happens after
// decoding and so has no node of its own to point at. A workspace built in Go
// rather than parsed has no line, and says nothing rather than "line 0".
func windowPosition(window Window) string {
	if window.line <= 0 {
		return ""
	}
	return "line " + strconv.Itoa(window.line) + ": "
}

// layoutNames are the layouts tmux's select-layout knows by name. The mirrored
// pair arrived after the oldest tmux this module supports, so a name is
// accepted here and left for tmux to refuse on a version that lacks it: this
// check exists to catch a misspelling before anything is built, not to decide
// what a given tmux can do.
var layoutNames = map[string]bool{
	"even-horizontal":          true,
	"even-vertical":            true,
	"main-horizontal":          true,
	"main-horizontal-mirrored": true,
	"main-vertical":            true,
	"main-vertical-mirrored":   true,
	"tiled":                    true,
}

// validLayout reports whether tmux could accept the layout.
//
// select-layout also takes a layout string, which is what tmux prints for a
// window it has already arranged. Those carry a checksum and geometry
// separated by commas, so anything with a comma is passed through for tmux to
// parse rather than matched against the names.
func validLayout(layout string) bool {
	return strings.Contains(layout, ",") || layoutNames[layout]
}

// nested returns err unchanged when it already carries ErrInvalidWorkspace,
// so a failure deep in a document reports one sentinel prefix rather than one
// per nesting level.
func nested(line int, err error) error {
	if errors.Is(err, ErrInvalidWorkspace) {
		return err
	}
	return fmt.Errorf("%w: line %d: %w", ErrInvalidWorkspace, line, err)
}

// checkKnownFields rejects mapping keys outside allowed. A custom
// UnmarshalYAML receives a node rather than the decoder, and yaml.Node.Decode
// does not inherit the decoder's KnownFields setting, so strictness has to be
// re-established per type or a misspelled key is silently dropped.
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

// MissingDirectories reports the start directories the workspace names that do
// not exist, in the order they appear.
//
// It is separate from [Workspace.Validate] because a missing directory is not
// necessarily a mistake: a workspace whose shell_command_before creates one is
// ordinary, and rejecting it would refuse a file that works. tmux does not
// report one either. It starts the pane in the user's home directory and
// reports success, so a workspace naming a directory absent on this machine
// builds and puts panes somewhere other than where its file says.
//
// Call it before [Build] to turn that silence into something a caller can
// report. A name beginning with ~ is resolved against the current user's home
// directory, as tmux resolves it.
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
