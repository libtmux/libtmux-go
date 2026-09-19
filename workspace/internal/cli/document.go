package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/mattn/go-shellwords"
	"gopkg.in/yaml.v3"
)

type document = map[string]any

// sourceLines maps a document path -- "session_name", "windows.1",
// "windows.1.panes.0.focus" -- to the line its key is written on. A document
// that did not come from YAML text, a JSON file or a converted one, has none,
// and every lookup then adds nothing.
type sourceLines map[string]int

// at prefixes err with the line path is written on, keeping a *failure's code
// and exit status. An unknown path returns err unchanged.
func (l sourceLines) at(path string, err error) error {
	line, known := l[path]
	if err == nil || !known {
		return err
	}
	var specific *failure
	if errors.As(err, &specific) {
		return &failure{specific.Code, fmt.Sprintf("line %d: %s", line, specific.Message), specific.Exit}
	}
	return fmt.Errorf("line %d: %w", line, err)
}

func (l sourceLines) record(node *yaml.Node, prefix string) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			l.record(child, prefix)
		}
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind != yaml.ScalarNode {
				continue
			}
			path := key.Value
			if prefix != "" {
				path = prefix + "." + key.Value
			}
			l[path] = key.Line
			l.record(value, path)
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			path := prefix + "." + strconv.Itoa(index)
			l[path] = child.Line
			l.record(child, path)
		}
	case yaml.ScalarNode, yaml.AliasNode:
		// A scalar holds no keys, and an alias names a node written where its
		// anchor is, not here.
	}
}

func decodeDocument(data []byte) (document, sourceLines, error) {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return nil, nil, &failure{"invalid_workspace", fmt.Sprintf("decode workspace: %v", err), 1}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, nil, &failure{"invalid_workspace", "multiple YAML documents are not supported", 1}
		}
		return nil, nil, &failure{"invalid_workspace", fmt.Sprintf("decode trailing document: %v", err), 1}
	}
	var value document
	if err := root.Decode(&value); err != nil {
		return nil, nil, &failure{"invalid_workspace", fmt.Sprintf("decode workspace: %v", err), 1}
	}
	if value == nil {
		return nil, nil, &failure{"invalid_workspace", "workspace must be a mapping", 1}
	}
	if _, err := json.Marshal(value); err != nil {
		return nil, nil, &failure{"invalid_workspace", fmt.Sprintf("workspace requires JSON-compatible string keys: %v", err), 1}
	}
	lines := sourceLines{}
	lines.record(&root, "")
	return value, lines, nil
}

func readDocument(path string) (document, sourceLines, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".json") {
		var value document
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, nil, &failure{"invalid_workspace", fmt.Sprintf("decode JSON workspace: %v", err), 1}
		}
		if value == nil {
			return nil, nil, &failure{"invalid_workspace", "workspace must be a mapping", 1}
		}
		return value, nil, nil
	}
	return decodeDocument(data)
}

func mapping(value any) document {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func array(value any) []any {
	if a, ok := value.([]any); ok {
		return a
	}
	return nil
}

func textValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

var environmentPattern = regexp.MustCompile(`\$(\w+|\{[^}]*\})`)

func expand(value string) string {
	value = environmentPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := strings.Trim(match[1:], "{}")
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return match
	})
	if home, err := os.UserHomeDir(); err == nil {
		if value == "~" {
			value = home
		} else if rest, ok := strings.CutPrefix(value, "~/"); ok {
			value = filepath.Join(home, rest)
		}
	}
	return value
}

// directory resolves one level's start_directory. Absent (nil, or expanding
// to empty) propagates inherited unchanged, so a level that names nothing
// defers to its parent -- and an empty inherited chain all the way to the top
// leaves the result "", letting tmux fall back to the invoking client's own
// working directory instead of the workspace document's. A present relative
// value is always anchored to base, the document's directory, unless an
// ancestor level already resolved a directory of its own.
func directory(value any, base, inherited string) (string, error) {
	if value == nil {
		return inherited, nil
	}
	s, ok := value.(string)
	if !ok {
		return "", errors.New("start_directory must be a string")
	}
	s = expand(s)
	if s == "" {
		return inherited, nil
	}
	if !filepath.IsAbs(s) {
		anchor := inherited
		if anchor == "" {
			anchor = base
		}
		s = filepath.Join(anchor, s)
	}
	return filepath.Clean(s), nil
}

func boolean(value any, fallback bool) (bool, error) {
	if value == nil {
		return fallback, nil
	}
	switch v := value.(type) {
	case bool:
		return v, nil
	case int:
		if v == 0 || v == 1 {
			return v == 1, nil
		}
	case string:
		switch strings.ToLower(v) {
		case "true", "yes", "on", "1":
			return true, nil
		case "false", "no", "off", "0", "":
			return false, nil
		}
	}
	return false, fmt.Errorf("invalid boolean %q", value)
}

func environment(value any) (map[string]string, error) {
	out := map[string]string{}
	if value == nil {
		return out, nil
	}
	m := mapping(value)
	if m == nil {
		return nil, errors.New("environment and options must be mappings")
	}
	for key, v := range m {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return nil, fmt.Errorf("invalid environment/option name %q", key)
		}
		switch v.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("non-scalar value for %s", key)
		}
		s := expand(textValue(v))
		if strings.ContainsRune(s, 0) {
			return nil, fmt.Errorf("NUL in value for %s", key)
		}
		out[key] = s
	}
	return out, nil
}

func optionValues(value any) (map[string]string, error) {
	out, err := environment(value)
	if err != nil {
		return nil, err
	}
	for key, raw := range mapping(value) {
		if flag, ok := raw.(bool); ok {
			out[key] = "off"
			if flag {
				out[key] = "on"
			}
		}
	}
	return out, nil
}

type loadPlan struct {
	Name, Directory                     string
	BeforeScript                        []string
	Readiness                           string
	ScriptDirectory                     string
	Environment, Options, GlobalOptions map[string]string
	Windows                             []windowPlan
	Bridge                              bool
	// Warnings are reported once the load starts. Nothing here refuses the
	// document.
	Warnings []planWarning
}

// planWarning is one reportable problem that does not refuse the document.
type planWarning struct{ Code, Message string }

type windowPlan struct {
	Name, Directory, Layout string
	Index                   *int
	Focus                   bool
	Options, OptionsAfter   map[string]string
	Panes                   []panePlan
}
type panePlan struct {
	Directory, Shell       string
	Environment            map[string]string
	Focus, SuppressHistory bool
	Commands               []commandPlan
}
type commandPlan struct {
	Text                    string
	Enter                   bool
	SleepBefore, SleepAfter time.Duration
}

// normalize resolves doc into a build plan. Every refusal it makes is a defect
// in the document, so machine output classifies it as such unless a more
// specific classification was already made.
func normalize(doc document, base string) (loadPlan, error) {
	return normalizeSource(doc, base, nil)
}

// normalizeSource is normalize with the positions decodeDocument recorded, so
// a refusal names the line its key is written on.
func normalizeSource(doc document, base string, lines sourceLines) (loadPlan, error) {
	plan, err := normalizeDocument(doc, base, lines)
	if err == nil {
		return plan, nil
	}
	// Several problems are reported together, so the classification has to be
	// one flat failure: a caller that reaches into a joined error for a
	// *failure finds the first and drops everything reported beside it.
	code, exit := "invalid_workspace", 1
	var specific *failure
	if errors.As(err, &specific) {
		code, exit = specific.Code, specific.Exit
	}
	return plan, &failure{code, err.Error(), exit}
}

func normalizeDocument(doc document, base string, lines sourceLines) (loadPlan, error) {
	plugins := doc["plugins"]
	if items, ok := plugins.([]any); ok && len(items) == 0 {
		plugins = nil
	}
	plan := loadPlan{Name: expand(textValue(doc["session_name"])), Readiness: "auto", Bridge: plugins != nil || doc["workspace_builder"] != nil}
	if !plan.Bridge {
		if err := checkFieldsAt(doc, "workspace", lines, "",
			"session_name", "description", "start_directory", "environment", "options",
			"global_options", "shell_command_before", "suppress_history", "windows",
			"before_script", "plugins", "workspace_builder", "workspace_builder_paths", "workspace_builder_options",
		); err != nil {
			return plan, err
		}
	}
	if raw, exists := doc["workspace_builder_options"]; exists && raw != nil {
		catalog := mapping(raw)
		if catalog == nil {
			return plan, lines.at("workspace_builder_options", errors.New("workspace_builder_options must be a mapping"))
		}
		if !plan.Bridge {
			// The key is shared across ports whose settings are not the same
			// set, so an unrecognised one is reported and ignored rather than
			// making the document unloadable here.
			for _, key := range slices.Sorted(maps.Keys(catalog)) {
				if key == "pane_readiness" || strings.HasPrefix(key, "x-") {
					continue
				}
				plan.Warnings = append(plan.Warnings, planWarning{"unsupported_key", fmt.Sprintf("workspace_builder_options: ignoring unknown setting %q", key)})
			}
		}
		if value := catalog["pane_readiness"]; value != nil {
			switch strings.ToLower(strings.TrimSpace(textValue(value))) {
			case "auto":
			case "always", "true", "on", "yes", "1":
				plan.Readiness = "always"
			case "never", "false", "off", "no", "0":
				plan.Readiness = "never"
			default:
				return plan, lines.at("workspace_builder_options.pane_readiness", errors.New("pane_readiness must be auto, always or never"))
			}
		}
	}
	if plan.Name == "" || strings.ContainsAny(plan.Name, ".:\x00\r\n") {
		return plan, lines.at("session_name", errors.New("session_name must be nonempty and contain no colon, period, NUL or newline"))
	}
	var err error
	directories := map[string]bool{}
	plan.Directory, err = directory(doc["start_directory"], base, "")
	if err != nil {
		return plan, lines.at("start_directory", err)
	}
	directories[plan.Directory] = true
	if doc["start_directory"] != nil {
		plan.ScriptDirectory = plan.Directory
	}
	if script := expand(textValue(doc["before_script"])); script != "" && !plan.Bridge {
		plan.BeforeScript, err = shellwords.Parse(script)
		if err != nil {
			return plan, lines.at("before_script", fmt.Errorf("before_script: %w", err))
		}
		if len(plan.BeforeScript) == 0 {
			return plan, lines.at("before_script", errors.New("before_script must contain a command"))
		}
		if strings.HasPrefix(plan.BeforeScript[0], ".") {
			plan.BeforeScript[0] = filepath.Join(base, plan.BeforeScript[0])
		}
	}
	plan.Environment, err = environment(doc["environment"])
	if err != nil {
		return plan, lines.at("environment", err)
	}
	plan.Options, err = optionValues(doc["options"])
	if err != nil {
		return plan, lines.at("options", err)
	}
	plan.GlobalOptions, err = optionValues(doc["global_options"])
	if err != nil {
		return plan, lines.at("global_options", err)
	}
	suppress, err := boolean(doc["suppress_history"], true)
	if err != nil {
		return plan, lines.at("suppress_history", err)
	}
	windows, ok := doc["windows"].([]any)
	if !ok || len(windows) == 0 {
		return plan, lines.at("windows", errors.New("windows must be a nonempty sequence"))
	}
	// Every window is checked, so one mistake does not hide the next: fix
	// one, rerun, find the next is the loop a config file otherwise puts a
	// user in.
	scope := windowScope{doc, base, lines, plan.Bridge, plan.Directory, suppress, map[int]bool{}, directories}
	var problems []error
	for position, raw := range windows {
		wp, err := normalizeWindow(raw, position, scope)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		plan.Windows = append(plan.Windows, wp)
	}
	if len(problems) > 0 {
		return plan, errors.Join(problems...)
	}
	// tmux starts a pane in $HOME when the directory it was given does not
	// exist, so a typo is invisible: the panes come up elsewhere and the load
	// reports success. It is not a refusal -- the workspace still builds.
	for _, path := range slices.Sorted(maps.Keys(directories)) {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			continue
		}
		plan.Warnings = append(plan.Warnings, planWarning{"start_directory_missing", "start_directory is not a directory, tmux will fall back to $HOME: " + path})
	}
	return plan, nil
}

// windowScope is what a window's fields resolve against: the values the
// document level already settled, and the two maps that span every window.
type windowScope struct {
	doc         document
	base        string
	lines       sourceLines
	bridge      bool
	directory   string
	suppress    bool
	indexes     map[int]bool
	directories map[string]bool
}

func normalizeWindow(raw any, position int, scope windowScope) (windowPlan, error) {
	windowPath := "windows." + strconv.Itoa(position)
	w := mapping(raw)
	if w == nil {
		return windowPlan{}, scope.lines.at(windowPath, fmt.Errorf("window %d must be a mapping", position))
	}
	if !scope.bridge {
		if err := checkFieldsAt(w, fmt.Sprintf("window %d", position), scope.lines, windowPath,
			"window_name", "description", "window_index", "layout", "start_directory", "window_shell",
			"focus", "suppress_history", "options", "options_after", "environment", "shell_command_before", "panes",
		); err != nil {
			return windowPlan{}, err
		}
	}
	wp := windowPlan{Name: expand(textValue(w["window_name"])), Layout: expand(textValue(w["layout"]))}
	// The validator's own text describes a library call and the tmux
	// release the check exists for; what the user wrote is a name.
	if (tmux.SelectLayoutRequest{Layout: wp.Layout}).Validate() != nil {
		return wp, scope.lines.at(windowPath+".layout", fmt.Errorf("window %d: layout %q is not a tmux layout name or a saved layout string", position, wp.Layout))
	}
	if strings.ContainsRune(wp.Name, 0) {
		return wp, scope.lines.at(windowPath+".window_name", errors.New("NUL in window name"))
	}
	var err error
	wp.Directory, err = directory(w["start_directory"], scope.base, scope.directory)
	if err != nil {
		return wp, scope.lines.at(windowPath+".start_directory", err)
	}
	scope.directories[wp.Directory] = true
	wp.Focus, err = boolean(w["focus"], false)
	if err != nil {
		return wp, scope.lines.at(windowPath+".focus", err)
	}
	if index, exists := w["window_index"]; exists {
		n, e := strconv.Atoi(textValue(index))
		if e != nil || n < 0 {
			return wp, scope.lines.at(windowPath+".window_index", errors.New("window_index must be nonnegative"))
		}
		if scope.indexes[n] {
			return wp, scope.lines.at(windowPath+".window_index", fmt.Errorf("duplicate window_index %d", n))
		}
		scope.indexes[n] = true
		wp.Index = &n
	}
	wp.Options, err = optionValues(w["options"])
	if err != nil {
		return wp, scope.lines.at(windowPath+".options", err)
	}
	wp.OptionsAfter, err = optionValues(w["options_after"])
	if err != nil {
		return wp, scope.lines.at(windowPath+".options_after", err)
	}
	windowEnv, err := environment(w["environment"])
	if err != nil {
		return wp, scope.lines.at(windowPath+".environment", err)
	}
	windowSuppress, err := boolean(w["suppress_history"], scope.suppress)
	if err != nil {
		return wp, scope.lines.at(windowPath+".suppress_history", err)
	}
	panes, ok := w["panes"].([]any)
	// An omitted panes key and an explicit empty sequence both mean one
	// pane with no command -- tmuxp raises IndexError on the empty form,
	// but nothing here asked for that.
	if _, exists := w["panes"]; !exists || (ok && len(panes) == 0) {
		panes, ok = []any{nil}, true
	}
	if !ok || len(panes) == 0 {
		return wp, scope.lines.at(windowPath+".panes", fmt.Errorf("window %d panes must be a nonempty sequence", position))
	}
	for pi, rawPane := range panes {
		panePath := windowPath + ".panes." + strconv.Itoa(pi)
		p := mapping(rawPane)
		if p == nil {
			p = document{}
			switch v := rawPane.(type) {
			case nil:
			case string:
				if v != "pane" && v != "blank" && v != "" {
					p["shell_command"] = v
				}
			case []any:
				p["shell_command"] = v
			default:
				return wp, scope.lines.at(panePath, fmt.Errorf("window %d pane %d has invalid shorthand", position, pi))
			}
		}
		if !scope.bridge {
			if err := checkFieldsAt(p, fmt.Sprintf("window %d pane %d", position, pi), scope.lines, panePath,
				"shell_command", "shell_command_before", "description", "start_directory", "shell",
				"focus", "suppress_history", "environment", "enter", "sleep_before", "sleep_after",
			); err != nil {
				return wp, err
			}
		}
		pp := panePlan{Shell: expand(textValue(w["window_shell"])), Environment: windowEnv}
		if shell, exists := p["shell"]; exists {
			pp.Shell = expand(textValue(shell))
		}
		pp.Directory, err = directory(p["start_directory"], scope.base, wp.Directory)
		if err != nil {
			return wp, scope.lines.at(panePath+".start_directory", err)
		}
		scope.directories[pp.Directory] = true
		pp.Focus, err = boolean(p["focus"], false)
		if err != nil {
			return wp, scope.lines.at(panePath+".focus", err)
		}
		pp.SuppressHistory, err = boolean(p["suppress_history"], windowSuppress)
		if err != nil {
			return wp, scope.lines.at(panePath+".suppress_history", err)
		}
		if env, exists := p["environment"]; exists {
			pp.Environment, err = environment(env)
			if err != nil {
				return wp, scope.lines.at(panePath+".environment", err)
			}
		}
		state := commandPlan{Enter: true}
		if err := commandSettings(&state, p); err != nil {
			return wp, scope.lines.at(panePath, err)
		}
		for _, commands := range []any{scope.doc["shell_command_before"], w["shell_command_before"], p["shell_command_before"], p["shell_command"]} {
			parsed, e := commandsWithState(commands, &state, !scope.bridge)
			if e != nil {
				return wp, scope.lines.at(panePath, fmt.Errorf("window %d pane %d: %w", position, pi, e))
			}
			pp.Commands = append(pp.Commands, parsed...)
		}
		wp.Panes = append(wp.Panes, pp)
	}
	return wp, nil
}

func checkFields(doc document, scope string, allowed ...string) error {
	return checkFieldsAt(doc, scope, nil, "", allowed...)
}

func checkFieldsAt(doc document, scope string, lines sourceLines, prefix string, allowed ...string) error {
	for _, key := range slices.Sorted(maps.Keys(doc)) {
		// A key starting with "x-", at any level, is inert: accepted here,
		// ignored by every field lookup below, and left untouched by convert.
		if strings.HasPrefix(key, "x-") {
			continue
		}
		if !slices.Contains(allowed, key) {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			return lines.at(path, &failure{"unsupported_key", fmt.Sprintf("%s: unknown field %q (custom fields use an \"x-\" prefix)", scope, key), 1})
		}
	}
	return nil
}

func commandSettings(state *commandPlan, m document) error {
	var err error
	if enter, ok := m["enter"]; ok {
		state.Enter, err = boolean(enter, true)
		if err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name   string
		target *time.Duration
	}{{"sleep_before", &state.SleepBefore}, {"sleep_after", &state.SleepAfter}} {
		if raw, ok := m[field.name]; ok {
			n, e := strconv.ParseFloat(textValue(raw), 64)
			if e != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) || n > float64(math.MaxInt64)/float64(time.Second) {
				return fmt.Errorf("%s must be a finite nonnegative duration", field.name)
			}
			*field.target = time.Duration(n * float64(time.Second))
		}
	}
	return nil
}

func commandsWithState(value any, state *commandPlan, strict bool) ([]commandPlan, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		values = []any{value}
	}
	result := []commandPlan{}
	for index, raw := range values {
		if raw == nil {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			m := mapping(raw)
			if m == nil {
				return nil, errors.New("commands must be strings or cmd mappings")
			}
			if strict {
				if err := checkFields(m, fmt.Sprintf("command %d", index), "cmd", "enter", "sleep_before", "sleep_after"); err != nil {
					return nil, err
				}
			}
			var exists bool
			text, exists = m["cmd"].(string)
			if !exists {
				return nil, errors.New("command mapping requires a string cmd")
			}
			if err := commandSettings(state, m); err != nil {
				return nil, err
			}
		}
		if strings.ContainsRune(text, 0) {
			return nil, errors.New("NUL in shell command")
		}
		command := *state
		command.Text = expand(text)
		result = append(result, command)
	}
	return result, nil
}
