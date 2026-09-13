package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// importWorkspace translates only source behavior represented by the native loader.
func importWorkspace(doc document, kind string) (document, error) {
	if err := importTemplates(doc); err != nil {
		return nil, err
	}
	switch kind {
	case "teamocil":
		if raw, ok := doc["session"]; ok {
			if err := checkFields(doc, "teamocil", "session"); err != nil {
				return nil, err
			}
			doc = mapping(raw)
			if doc == nil {
				return nil, errors.New("teamocil session must be a mapping")
			}
		}
		if err := checkFields(doc, "teamocil", "name", "root", "windows"); err != nil {
			return nil, err
		}
	case "tmuxinator":
		if err := checkFields(doc, "tmuxinator", "name", "project_name", "root", "project_root", "windows", "tabs", "pre_window", "pre_tab"); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown import source %q", kind)
	}
	name, err := importAlias(doc, "name", "project_name")
	if err != nil {
		return nil, err
	}
	if _, err := importString(name, "name"); err != nil {
		return nil, err
	}
	root, err := importAlias(doc, "root", "project_root")
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if _, err := importString(root, "root"); err != nil {
		return nil, err
	}
	absolute, err := directory(root, cwd)
	if err != nil {
		return nil, err
	}
	rawWindows, err := importAlias(doc, "windows", "tabs")
	if err != nil {
		return nil, err
	}
	windows, err := importSequence(rawWindows, "windows")
	if err != nil {
		return nil, err
	}
	before, err := importAlias(doc, "pre_window", "pre_tab")
	if err != nil {
		return nil, err
	}
	command, err := importCommands(before, "pre_window", "; ")
	if err != nil {
		return nil, err
	}
	out := document{"session_name": name, "start_directory": absolute}
	converted := make([]any, 0, len(windows))
	for index, raw := range windows {
		var window document
		if kind == "tmuxinator" {
			window, err = importTmuxinatorWindow(raw)
		} else {
			window, err = importTeamocilWindow(raw)
		}
		if err != nil {
			return nil, fmt.Errorf("window %d: %w", index, err)
		}
		converted = append(converted, window)
	}
	out["windows"] = converted
	if len(command) > 0 {
		out["shell_command_before"] = command
	}
	importFirstFocus(converted)
	return out, nil
}

func importAlias(doc document, names ...string) (any, error) {
	var value any
	selected := ""
	for _, name := range names {
		if raw, ok := doc[name]; ok {
			if selected != "" {
				return nil, fmt.Errorf("conflicting aliases %q and %q", selected, name)
			}
			selected, value = name, raw
		}
	}
	return value, nil
}

func importString(value any, field string) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return text, nil
}

func importSequence(value any, field string) ([]any, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("%s must be a nonempty sequence", field)
	}
	return items, nil
}

func importCommands(value any, field, separator string) ([]any, error) {
	if value == nil {
		return []any{}, nil
	}
	var lines []any
	switch v := value.(type) {
	case string:
		lines = []any{v}
	case []any:
		lines = v
	default:
		return nil, fmt.Errorf("%s must be a command string or sequence", field)
	}
	commands := make([]any, 0, len(lines))
	texts := make([]string, 0, len(lines))
	for index, raw := range lines {
		line, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%s command %d must be a string", field, index)
		}
		texts = append(texts, line)
		commands = append(commands, document{"cmd": line})
	}
	if separator != "" && len(commands) > 0 {
		return []any{document{"cmd": strings.Join(texts, separator)}}, nil
	}
	return commands, nil
}

func importTmuxinatorWindow(raw any) (document, error) {
	named := mapping(raw)
	if len(named) != 1 {
		return nil, errors.New("tmuxinator window requires exactly one name")
	}
	var name string
	var value any
	for key, raw := range named {
		name, value = key, raw
	}
	out := document{"window_name": name}
	settings := mapping(value)
	if settings == nil {
		commands, err := importCommands(value, "window commands", "")
		if err != nil {
			return nil, err
		}
		out["panes"] = []any{document{"shell_command": commands, "focus": true}}
		return out, nil
	}
	if err := checkFields(settings, "tmuxinator window", "root", "layout", "pre", "panes", "synchronize"); err != nil {
		return nil, err
	}
	if err := importWindowFields(settings, out); err != nil {
		return nil, err
	}
	before, err := importCommands(settings["pre"], "pre", " && ")
	if err != nil {
		return nil, err
	}
	panes := []any{nil}
	if raw, ok := settings["panes"]; ok {
		panes, err = importSequence(raw, "panes")
		if err != nil {
			return nil, err
		}
	} else if len(before) > 0 {
		return nil, errors.New("window pre requires explicit panes")
	}
	converted := make([]any, 0, len(panes))
	for index, pane := range panes {
		commands, err := importCommands(pane, "pane", "")
		if err != nil {
			return nil, err
		}
		converted = append(converted, document{"shell_command": commands, "focus": index == 0})
	}
	out["panes"] = converted
	if len(before) > 0 {
		out["shell_command_before"] = before
	}
	switch sync := settings["synchronize"]; sync {
	case nil, false:
	case true, "before":
		out["options"] = document{"synchronize-panes": true}
	case "after":
		out["options_after"] = document{"synchronize-panes": true}
	default:
		return nil, fmt.Errorf("synchronize must be false, true, before or after: %v", sync)
	}
	return out, nil
}

func importWindowFields(source, target document) error {
	for _, field := range []struct{ from, to string }{{"root", "start_directory"}, {"layout", "layout"}} {
		if raw, ok := source[field.from]; ok {
			if _, err := importString(raw, field.from); err != nil {
				return err
			}
			target[field.to] = raw
		}
	}
	return nil
}

func importTeamocilWindow(raw any) (document, error) {
	settings := mapping(raw)
	if settings == nil {
		return nil, errors.New("teamocil window must be a mapping")
	}
	if err := checkFields(settings, "teamocil window", "name", "root", "layout", "options", "focus", "panes", "splits"); err != nil {
		return nil, err
	}
	if _, err := importString(settings["name"], "name"); err != nil {
		return nil, err
	}
	out := document{"window_name": settings["name"]}
	if err := importWindowFields(settings, out); err != nil {
		return nil, err
	}
	if err := importFocus(settings, out); err != nil {
		return nil, err
	}
	if value, ok := settings["options"]; ok {
		if _, err := optionValues(value); err != nil {
			return nil, err
		}
		out["options"] = value
	}
	rawPanes, err := importAlias(settings, "panes", "splits")
	if err != nil {
		return nil, err
	}
	panes := []any{document{}}
	if rawPanes != nil {
		panes, err = importSequence(rawPanes, "panes")
		if err != nil {
			return nil, err
		}
	}
	converted := make([]any, 0, len(panes))
	for index, rawPane := range panes {
		pane := mapping(rawPane)
		if pane == nil {
			return nil, fmt.Errorf("pane %d must be a mapping", index)
		}
		if err := checkFields(pane, fmt.Sprintf("pane %d", index), "commands", "cmd", "focus"); err != nil {
			return nil, err
		}
		commands, err := importAlias(pane, "commands", "cmd")
		if err != nil {
			return nil, err
		}
		if raw, ok := pane["commands"]; ok && raw != nil {
			if _, ok := raw.([]any); !ok {
				return nil, errors.New("commands must be a sequence")
			}
		}
		grouped, err := importCommands(commands, "pane commands", "; ")
		if err != nil {
			return nil, err
		}
		target := document{"shell_command": grouped}
		if err := importFocus(pane, target); err != nil {
			return nil, err
		}
		converted = append(converted, target)
	}
	importFirstFocus(converted)
	out["panes"] = converted
	return out, nil
}

func importFocus(source, target document) error {
	if value, ok := source["focus"]; ok && value != nil {
		flag, ok := value.(bool)
		if !ok {
			return errors.New("focus must be a boolean")
		}
		target["focus"] = flag
	}
	return nil
}

func importFirstFocus(items []any) {
	first := 0
	for index, item := range items {
		if mapping(item)["focus"] == true {
			first = index
			break
		}
	}
	for index, item := range items {
		mapping(item)["focus"] = index == first
	}
}

func importTemplates(value any) error {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, "<%") {
			return errors.New("import does not evaluate ERB templates")
		}
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if err := importTemplates(key); err != nil {
				return err
			}
			if err := importTemplates(v[key]); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := importTemplates(item); err != nil {
				return err
			}
		}
	}
	return nil
}
