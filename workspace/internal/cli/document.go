package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type document = map[string]any

func decodeDocument(data []byte) (document, error) {
	var value document
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode workspace: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not supported")
		}
		return nil, fmt.Errorf("decode trailing document: %w", err)
	}
	if value == nil {
		return nil, errors.New("workspace must be a mapping")
	}
	if _, err := json.Marshal(value); err != nil {
		return nil, fmt.Errorf("workspace requires JSON-compatible string keys: %w", err)
	}
	return value, nil
}

func readDocument(path string) (document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
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
	if value == "~" || strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
			if strings.HasSuffix(value, "/~") {
				value = home
			}
		}
	}
	return value
}

func directory(value any, parent string) (string, error) {
	if value == nil {
		return parent, nil
	}
	s, ok := value.(string)
	if !ok {
		return "", errors.New("start_directory must be a string")
	}
	s = expand(s)
	if s == "" {
		return parent, nil
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(parent, s)
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

type loadPlan struct {
	Name, Directory, BeforeScript       string
	Readiness                           string
	ScriptDirectory                     string
	Environment, Options, GlobalOptions map[string]string
	Windows                             []windowPlan
	Bridge                              bool
}
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

func normalize(doc document, base string) (loadPlan, error) {
	plan := loadPlan{Name: expand(textValue(doc["session_name"])), Readiness: "auto", Bridge: doc["plugins"] != nil || doc["workspace_builder"] != nil || doc["workspace_builder_paths"] != nil}
	if raw, exists := doc["workspace_builder_options"]; exists && raw != nil {
		catalog := mapping(raw)
		if catalog == nil {
			return plan, errors.New("workspace_builder_options must be a mapping")
		}
		if value := catalog["pane_readiness"]; value != nil {
			switch strings.ToLower(strings.TrimSpace(textValue(value))) {
			case "auto":
			case "always", "true", "on", "yes", "1":
				plan.Readiness = "always"
			case "never", "false", "off", "no", "0":
				plan.Readiness = "never"
			default:
				return plan, errors.New("pane_readiness must be auto, always or never")
			}
		}
	}
	if plan.Name == "" || strings.ContainsAny(plan.Name, ".:\x00\r\n") {
		return plan, errors.New("session_name must be nonempty and contain no colon, period, NUL or newline")
	}
	var err error
	plan.Directory, err = directory(doc["start_directory"], base)
	if err != nil {
		return plan, err
	}
	if doc["start_directory"] != nil {
		plan.ScriptDirectory = plan.Directory
	}
	plan.BeforeScript = expand(textValue(doc["before_script"]))
	if strings.HasPrefix(plan.BeforeScript, ".") {
		plan.BeforeScript = filepath.Join(base, plan.BeforeScript)
	}
	plan.Environment, err = environment(doc["environment"])
	if err != nil {
		return plan, err
	}
	plan.Options, err = environment(doc["options"])
	if err != nil {
		return plan, err
	}
	plan.GlobalOptions, err = environment(doc["global_options"])
	if err != nil {
		return plan, err
	}
	suppress, err := boolean(doc["suppress_history"], true)
	if err != nil {
		return plan, err
	}
	windows, ok := doc["windows"].([]any)
	if !ok || len(windows) == 0 {
		return plan, errors.New("windows must be a nonempty sequence")
	}
	seen := map[int]bool{}
	for wi, raw := range windows {
		w := mapping(raw)
		if w == nil {
			return plan, fmt.Errorf("window %d must be a mapping", wi)
		}
		wp := windowPlan{Name: expand(textValue(w["window_name"])), Layout: expand(textValue(w["layout"]))}
		if strings.ContainsRune(wp.Name, 0) {
			return plan, errors.New("NUL in window name")
		}
		wp.Directory, err = directory(w["start_directory"], plan.Directory)
		if err != nil {
			return plan, err
		}
		wp.Focus, err = boolean(w["focus"], false)
		if err != nil {
			return plan, err
		}
		if index, exists := w["window_index"]; exists {
			n, e := strconv.Atoi(textValue(index))
			if e != nil || n < 0 {
				return plan, errors.New("window_index must be nonnegative")
			}
			if seen[n] {
				return plan, fmt.Errorf("duplicate window_index %d", n)
			}
			seen[n] = true
			wp.Index = &n
		}
		wp.Options, err = environment(w["options"])
		if err != nil {
			return plan, err
		}
		wp.OptionsAfter, err = environment(w["options_after"])
		if err != nil {
			return plan, err
		}
		windowEnv, err := environment(w["environment"])
		if err != nil {
			return plan, err
		}
		windowSuppress, err := boolean(w["suppress_history"], suppress)
		if err != nil {
			return plan, err
		}
		panes, ok := w["panes"].([]any)
		if _, exists := w["panes"]; !exists {
			panes, ok = []any{nil}, true
		}
		if !ok || len(panes) == 0 {
			return plan, fmt.Errorf("window %d panes must be a nonempty sequence", wi)
		}
		for pi, rawPane := range panes {
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
					return plan, fmt.Errorf("window %d pane %d has invalid shorthand", wi, pi)
				}
			}
			pp := panePlan{Shell: expand(textValue(w["window_shell"])), Environment: windowEnv}
			if shell, exists := p["shell"]; exists {
				pp.Shell = expand(textValue(shell))
			}
			pp.Directory, err = directory(p["start_directory"], wp.Directory)
			if err != nil {
				return plan, err
			}
			pp.Focus, err = boolean(p["focus"], false)
			if err != nil {
				return plan, err
			}
			pp.SuppressHistory, err = boolean(p["suppress_history"], windowSuppress)
			if err != nil {
				return plan, err
			}
			if env, exists := p["environment"]; exists {
				pp.Environment, err = environment(env)
				if err != nil {
					return plan, err
				}
			}
			state := commandPlan{Enter: true}
			if err := commandSettings(&state, p); err != nil {
				return plan, err
			}
			for _, commands := range []any{doc["shell_command_before"], w["shell_command_before"], p["shell_command_before"], p["shell_command"]} {
				parsed, e := commandsWithState(commands, &state)
				if e != nil {
					return plan, e
				}
				pp.Commands = append(pp.Commands, parsed...)
			}
			wp.Panes = append(wp.Panes, pp)
		}
		plan.Windows = append(plan.Windows, wp)
	}
	return plan, nil
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

func commandsWithState(value any, state *commandPlan) ([]commandPlan, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		values = []any{value}
	}
	result := []commandPlan{}
	for _, raw := range values {
		if raw == nil {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			m := mapping(raw)
			if m == nil {
				return nil, errors.New("commands must be strings or cmd mappings")
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
