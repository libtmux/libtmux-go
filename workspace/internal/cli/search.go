package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var fieldAliases = map[string]string{"name": "name", "n": "name", "session": "session_name", "session_name": "session_name", "s": "session_name", "path": "path", "p": "path", "window": "window", "w": "window", "pane": "pane"}

// Python owns expression matching so lookaround, backreferences and Unicode word
// boundaries have Python semantics. Discovery and field extraction remain native.
const searchPython = `import json, re, sys
q = json.load(sys.stdin)
try:
    patterns = []
    for term in q['terms']:
        fields, pattern = term['fields'], term['pattern']
        flags = re.IGNORECASE if q['ignore_case'] or (q['smart_case'] and not any(c.isupper() for c in pattern)) else 0
        if q['fixed']: pattern = re.escape(pattern)
        if q['word']: pattern = r'\b(?:' + pattern + r')\b'
        patterns.append((fields, re.compile(pattern, flags)))
except re.error as error:
    print(json.dumps({'error': str(error)}))
    sys.exit(2)
output = []
for record in q['records']:
    matches, states = {}, []
    for fields, pattern in patterns:
        matched = False
        for field in fields:
            for value in record['fields'].get(field, []):
                found = pattern.search(value)
                if found:
                    matched = True
                    matches.setdefault(field, []).append(found.group())
        states.append(matched)
    selected = any(states) if q['any'] else all(states)
    if q['invert']: selected = not selected
    if selected:
        result = record['result']
        result['matches'] = matches
        result['matched_fields'] = list(matches)
        output.append(result)
print(json.dumps(output, ensure_ascii=False))
`

func (r *invocation) search(cmd *cobra.Command, o *options, args []string) error {
	fields := []string{"name", "session_name", "path", "window", "pane"}
	if len(o.fields) > 0 {
		fields = []string{}
		for _, field := range o.fields {
			canonical, ok := fieldAliases[strings.ToLower(field)]
			if !ok {
				return usage("unknown search field %q", field)
			}
			if !contains(fields, canonical) {
				fields = append(fields, canonical)
			}
		}
	}
	terms := []map[string]any{}
	for _, arg := range args {
		selected := fields
		pattern := arg
		if prefix, suffix, ok := strings.Cut(arg, ":"); ok {
			if canonical, exists := fieldAliases[strings.ToLower(prefix)]; exists {
				selected = []string{canonical}
				pattern = suffix
			}
		}
		if pattern != "" {
			terms = append(terms, map[string]any{"fields": selected, "pattern": pattern})
		}
	}
	if len(terms) == 0 {
		if !r.machine() {
			return cmd.Help()
		}
		return usage("search requires a nonempty query pattern")
	}
	if err := r.checkPython(false); err != nil {
		return err
	}
	records, _, err := discover(true)
	if err != nil {
		return err
	}
	inputRecords := []map[string]any{}
	for _, record := range records {
		values := map[string][]string{"name": {textValue(record["name"])}, "path": {textValue(record["path"])}, "session_name": {textValue(record["session_name"])}, "window": {}, "pane": {}}
		for _, rawWindow := range array(mapping(record["config"])["windows"]) {
			w := mapping(rawWindow)
			if name := textValue(w["window_name"]); name != "" {
				values["window"] = append(values["window"], name)
			}
			for _, rawPane := range array(w["panes"]) {
				if text, ok := rawPane.(string); ok {
					values["pane"] = append(values["pane"], text)
				} else if pane := mapping(rawPane); pane != nil {
					if text, ok := pane["shell_command"].(string); ok {
						values["pane"] = append(values["pane"], text)
					} else {
						for _, rawCommand := range array(pane["shell_command"]) {
							if rawCommand != nil {
								values["pane"] = append(values["pane"], textValue(rawCommand))
							}
						}
					}
				}
			}
		}
		result := map[string]any{}
		for _, key := range []string{"name", "path", "session_name", "source"} {
			result[key] = record[key]
		}
		inputRecords = append(inputRecords, map[string]any{"fields": values, "result": result})
	}
	payload, err := json.Marshal(map[string]any{"terms": terms, "records": inputRecords, "ignore_case": o.ignoreCase, "smart_case": o.smartCase, "fixed": o.fixed, "word": o.word, "any": o.any, "invert": o.invert})
	if err != nil {
		return err
	}
	result, err := r.process([]string{pythonExecutable(), "-c", searchPython}, "", bytes.NewReader(payload), false, nil)
	if err != nil {
		return err
	}
	if result.Status != 0 {
		return usage("invalid search expression: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}
	if result.Truncated {
		return fmt.Errorf("search compatibility output exceeds %d bytes", captureLimit)
	}
	var matches []map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &matches); err != nil {
		return err
	}
	if r.ndjson {
		for _, match := range matches {
			if err := r.encode(match); err != nil {
				return err
			}
		}
		return nil
	}
	if r.json {
		return r.encode(matches)
	}
	for _, match := range matches {
		if _, err := fmt.Fprintf(r.out, "%s  %s\n", r.style("subject", textValue(match["name"])), r.style("info", textValue(match["path"]))); err != nil {
			return err
		}
	}
	return nil
}
