package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var extensions = []string{".yaml", ".yml", ".json"}

func privatePath(path string) string {
	home, _ := os.UserHomeDir()
	if path == home {
		return "~"
	}
	if home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func globalDirectories() ([]map[string]any, string) {
	type candidate struct{ path, source string }
	candidates := []candidate{}
	if path, ok := os.LookupEnv("TMUXP_CONFIGDIR"); ok {
		candidates = append(candidates, candidate{expand(path), "$TMUXP_CONFIGDIR"})
	}
	if xdg, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok {
		candidates = append(candidates, candidate{filepath.Join(expand(xdg), "tmuxp"), "$XDG_CONFIG_HOME/tmuxp"})
	} else {
		candidates = append(candidates, candidate{expand("~/.config/tmuxp"), "XDG default"})
	}
	candidates = append(candidates, candidate{expand("~/.tmuxp"), "Legacy"})
	active := ""
	result := []map[string]any{}
	for _, c := range candidates {
		info, err := os.Stat(c.path)
		exists := err == nil && info.IsDir()
		if active == "" && exists {
			active = c.path
		}
		count := len(directoryFiles(c.path))
		result = append(result, map[string]any{"path": privatePath(c.path), "source": c.source, "exists": exists, "workspace_count": count})
	}
	if active == "" {
		active = candidates[len(candidates)-1].path
	}
	for i, c := range candidates {
		result[i]["active"] = c.path == active
	}
	return result, active
}

func directoryFiles(dir string) []string {
	result := []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && contains(extensions, strings.ToLower(filepath.Ext(entry.Name()))) {
			path := filepath.Join(dir, entry.Name())
			if isFile(path) {
				result = append(result, path)
			}
		}
	}
	return result
}

func resolveFile(input, global string) (string, error) {
	input = expand(input)
	if global == "" {
		_, global = globalDirectories()
	}
	pure := input != "" && input != "." && !strings.ContainsAny(input, "/\\") && filepath.Ext(input) == ""
	if pure {
		for _, ext := range extensions {
			candidate := filepath.Join(global, input+ext)
			if isFile(candidate) {
				return filepath.Abs(candidate)
			}
		}
		return "", fmt.Errorf("workspace %q not found in %s", input, privatePath(global))
	}
	path, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	if info, e := os.Stat(path); e == nil && info.IsDir() {
		for _, ext := range extensions {
			candidate := filepath.Join(path, ".tmuxp"+ext)
			if isFile(candidate) {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("no .tmuxp workspace in %s", privatePath(path))
	}
	if !isFile(path) {
		return "", fmt.Errorf("workspace file %s not found", privatePath(path))
	}
	return path, nil
}

func discover(full bool) ([]map[string]any, []map[string]any, error) {
	dirs, active := globalDirectories()
	cwd, err := os.Getwd()
	if err != nil {
		return nil, dirs, err
	}
	home, _ := os.UserHomeDir()
	records := []map[string]any{}
	seen := map[string]bool{}
	add := func(path, source string) {
		absolute, e := filepath.Abs(path)
		if e != nil || seen[absolute] {
			return
		}
		seen[absolute] = true
		stat, e := os.Stat(absolute)
		if e != nil {
			return
		}
		doc, _ := readDocument(absolute)
		format := "yaml"
		if strings.ToLower(filepath.Ext(path)) == ".json" {
			format = "json"
		}
		record := map[string]any{"name": strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "path": privatePath(absolute), "format": format, "size": stat.Size(), "mtime": stat.ModTime().UTC().Format(time.RFC3339Nano), "session_name": nil, "source": source}
		if doc != nil {
			record["session_name"] = doc["session_name"]
		}
		if full {
			record["config"] = doc
		}
		records = append(records, record)
	}
	for current := cwd; ; current = filepath.Dir(current) {
		for _, ext := range extensions {
			path := filepath.Join(current, ".tmuxp"+ext)
			if isFile(path) {
				add(path, "local")
				break
			}
		}
		if current == home || filepath.Dir(current) == current {
			break
		}
	}
	for _, path := range directoryFiles(active) {
		add(path, "global")
	}
	return records, dirs, nil
}

func (r *invocation) list(_ *cobra.Command, o *options, _ []string) error {
	records, dirs, err := discover(o.full)
	if err != nil {
		return err
	}
	if r.ndjson {
		for _, record := range records {
			if err := r.encode(record); err != nil {
				return err
			}
		}
		return nil
	}
	if r.json {
		return r.encode(map[string]any{"workspaces": records, "global_workspace_dirs": dirs})
	}
	for _, record := range records {
		prefix := ""
		if o.tree {
			prefix = "  └─ "
		}
		if _, err := fmt.Fprintf(r.out, "%s%s  %s  %s\n", prefix, r.style("subject", textValue(record["name"])), r.style("info", textValue(record["path"])), r.style("secondary", textValue(record["source"]))); err != nil {
			return err
		}
		if o.full {
			data, e := yaml.Marshal(record["config"])
			if e != nil {
				return e
			}
			if _, e = r.out.Write(data); e != nil {
				return e
			}
		}
	}
	if len(records) == 0 {
		_, err = fmt.Fprintln(r.out, "No workspaces found.")
	}
	return err
}

func (r *invocation) prompt(label, fallback string) (string, error) {
	if r.machine() {
		return "", usage("%s must be supplied in machine mode", label)
	}
	if _, err := fmt.Fprintf(r.err, "%s [%s]: ", label, fallback); err != nil {
		return "", err
	}
	reader, ok := r.in.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(r.in)
		r.in = reader
	}
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", errors.New("input required; use explicit noninteractive options")
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = fallback
	}
	return line, nil
}

func (r *invocation) confirm(label string, yes bool) error {
	if yes {
		return nil
	}
	answer, err := r.prompt(label+" (y/n)", "n")
	if err != nil {
		return err
	}
	if strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes" {
		return errors.New("operation declined")
	}
	return nil
}

func validateFormat(format string) error {
	if format != "" && !contains([]string{"yaml", "json"}, format) {
		return usage("invalid workspace format %q", format)
	}
	return nil
}

func atomicWrite(path string, data []byte, force bool) error {
	path = expand(path)
	if _, err := os.Lstat(path); err == nil && !force {
		return fmt.Errorf("destination exists: %s; use --force to replace", privatePath(path))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tmux-workspace-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if force {
		return os.Rename(name, path)
	}
	// Linking provides no-replace publication even if another writer races us.
	if err = os.Link(name, path); err != nil {
		return fmt.Errorf("publish destination without replacement: %w", err)
	}
	return nil
}

func (r *invocation) documentResult(o *options, doc document, source, format string, warnings []string) error {
	if o.format != "" {
		format = o.format
	}
	if o.saveTo == "" && r.machine() {
		if r.ndjson {
			return r.result(map[string]any{"status": "ok", "document": doc, "format": format, "warnings": warnings})
		}
		return r.encode(doc)
	}
	destination := o.saveTo
	if destination == "" {
		base := strings.TrimSuffix(source, filepath.Ext(source))
		if base == "" {
			base = expand("~/.tmuxp/" + textValue(doc["session_name"]))
		}
		destination = base + "." + format
		if !o.yes {
			var err error
			destination, err = r.prompt("Save workspace", destination)
			if err != nil {
				return err
			}
		}
	}
	var data []byte
	var err error
	if format == "json" {
		data, err = json.MarshalIndent(doc, "", "  ")
		data = append(data, '\n')
	} else {
		data, err = yaml.Marshal(doc)
	}
	if err != nil {
		return err
	}
	if err = atomicWrite(destination, data, o.force); err != nil {
		return err
	}
	if r.machine() {
		return r.result(map[string]any{"status": "ok", "destination": privatePath(destination), "format": format, "warnings": warnings})
	}
	if !o.quiet {
		_, err = fmt.Fprintf(r.out, "%s %s\n", r.style("success", "Saved"), r.style("info", privatePath(destination)))
	}
	return err
}

func (r *invocation) convert(_ *cobra.Command, o *options, args []string) error {
	if err := validateFormat(o.format); err != nil {
		return err
	}
	path, err := resolveFile(args[0], "")
	if err != nil {
		return err
	}
	doc, err := readDocument(path)
	if err != nil {
		return err
	}
	format := "json"
	if strings.ToLower(filepath.Ext(path)) == ".json" {
		format = "yaml"
	}
	if !r.machine() && o.saveTo == "" {
		if err := r.confirm("Convert workspace", o.yes); err != nil {
			return err
		}
	}
	return r.documentResult(o, doc, path, format, nil)
}

func (r *invocation) importDocument(_ *cobra.Command, o *options, args []string, kind string) error {
	if err := validateFormat(o.format); err != nil {
		return err
	}
	global := expand("~/." + kind)
	if kind == "tmuxinator" {
		if configured, exists := os.LookupEnv("TMUXINATOR_CONFIG"); exists {
			global = expand(configured)
		}
	}
	path, err := resolveFile(args[0], global)
	if err != nil {
		return err
	}
	doc, err := readDocument(path)
	if err != nil {
		return err
	}
	converted, err := importWorkspace(doc, kind)
	if err != nil {
		return err
	}
	return r.documentResult(o, converted, path, "yaml", []string{"import preserves the reference adapter's lossy field mapping"})
}

func importWorkspace(doc document, kind string) (document, error) {
	result := document{"windows": []any{}}
	windows := []any{}
	if kind == "teamocil" {
		if inner := mapping(doc["session"]); inner != nil {
			doc = inner
		}
		result["session_name"] = doc["name"]
		if root, ok := doc["root"]; ok {
			result["start_directory"] = root
		}
		for _, raw := range array(doc["windows"]) {
			w := mapping(raw)
			if w == nil {
				return nil, errors.New("teamocil window must be a mapping")
			}
			out := document{"window_name": w["name"]}
			for from, to := range map[string]string{"root": "start_directory", "layout": "layout", "clear": "clear"} {
				if value, ok := w[from]; ok {
					out[to] = value
				}
			}
			if filters := mapping(w["filters"]); filters != nil {
				if value, ok := filters["before"]; ok {
					out["shell_command_before"] = value
				}
				if value, ok := filters["after"]; ok {
					out["shell_command_after"] = value
				}
			}
			panes := w["panes"]
			if splits, ok := w["splits"]; ok {
				panes = splits
			}
			if panes != nil {
				converted := []any{}
				for _, rawPane := range array(panes) {
					p := mapping(rawPane)
					if p == nil {
						return nil, errors.New("teamocil panes must be mappings")
					}
					if value, ok := p["cmd"]; ok {
						p["shell_command"] = value
						delete(p, "cmd")
					}
					delete(p, "width")
					converted = append(converted, p)
				}
				out["panes"] = converted
			}
			windows = append(windows, out)
		}
	} else {
		name := doc["name"]
		if value, ok := doc["project_name"]; ok {
			name = value
		}
		result["session_name"] = name
		for _, field := range []string{"root", "project_root"} {
			if value, ok := doc[field]; ok {
				result["start_directory"] = value
			}
		}
		for _, field := range []string{"tmux_options", "cli_args"} {
			if value, ok := doc[field]; ok {
				result["config"] = strings.TrimSpace(strings.ReplaceAll(textValue(value), "-f", ""))
			}
		}
		if value, ok := doc["socket_name"]; ok {
			result["socket_name"] = value
		}
		if pre, ok := doc["pre"]; ok {
			before := pre
			if value, exists := doc["pre_window"]; exists {
				result["shell_command"] = pre
				before = value
			}
			if text, ok := before.(string); ok {
				before = []any{text}
			}
			result["shell_command_before"] = before
		}
		if value, ok := doc["rbenv"]; ok {
			before := array(result["shell_command_before"])
			result["shell_command_before"] = append(before, "rbenv shell "+textValue(value))
		}
		rawWindows := doc["windows"]
		if tabs, ok := doc["tabs"]; ok {
			rawWindows = tabs
		}
		for _, raw := range array(rawWindows) {
			w := mapping(raw)
			if w == nil {
				return nil, errors.New("tmuxinator window must be a mapping")
			}
			for name, value := range w {
				out := document{"window_name": name}
				if m := mapping(value); m != nil {
					for from, to := range map[string]string{"pre": "shell_command_before", "panes": "panes", "root": "start_directory", "layout": "layout"} {
						if v, ok := m[from]; ok {
							out[to] = v
						}
					}
				} else if _, ok := value.([]any); ok {
					out["panes"] = value
				} else {
					out["panes"] = []any{value}
				}
				windows = append(windows, out)
			}
		}
	}
	if doc["windows"] == nil && doc["tabs"] == nil {
		return nil, errors.New("import source requires windows or tabs")
	}
	result["windows"] = windows
	return result, nil
}
