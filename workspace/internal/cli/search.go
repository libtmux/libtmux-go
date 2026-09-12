package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

var fieldAliases = map[string]string{"name": "name", "n": "name", "session": "session_name", "session_name": "session_name", "s": "session_name", "path": "path", "p": "path", "window": "window", "w": "window", "pane": "pane"}

// The explicit Python engine supports lookaround, backreferences and Python
// word boundaries. Ordinary search never starts this process.
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
                if not isinstance(value, str): value = str(value)
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

type searchTerm struct {
	Fields  []string `json:"fields"`
	Pattern string   `json:"pattern"`
}

type searchRecord struct {
	Fields map[string][]any `json:"fields"`
	Result map[string]any   `json:"result"`
}

type searchPattern struct {
	fields     []string
	expression *regexp.Regexp
}

func compileSearch(terms []searchTerm, o *options) ([]searchPattern, error) {
	patterns := make([]searchPattern, 0, len(terms))
	for _, term := range terms {
		pattern := term.Pattern
		ignoreCase := o.ignoreCase || (o.smartCase && strings.IndexFunc(pattern, unicode.IsUpper) < 0)
		if o.fixed {
			pattern = regexp.QuoteMeta(pattern)
		}
		if o.word {
			pattern = `\b(?:` + pattern + `)\b`
		}
		if ignoreCase {
			pattern = "(?i)" + pattern
		}
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return nil, usage("invalid Go expression: %v; use --regex-engine python for Python-only syntax", err)
		}
		patterns = append(patterns, searchPattern{term.Fields, expression})
	}
	return patterns, nil
}

func (r *invocation) nativeMatches(patterns []searchPattern, records []searchRecord, o *options) ([]map[string]any, error) {
	output := []map[string]any{}
	for _, record := range records {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		matches := map[string][]string{}
		matchedFields := []string{}
		selected := !o.any
		for _, pattern := range patterns {
			matched := false
			for _, field := range pattern.fields {
				for _, value := range record.Fields[field] {
					text, ok := value.(string)
					if !ok {
						encoded, err := json.Marshal(value)
						if err != nil {
							return nil, err
						}
						text = string(encoded)
					}
					if found := pattern.expression.FindStringIndex(text); found != nil {
						matched = true
						if _, exists := matches[field]; !exists {
							matchedFields = append(matchedFields, field)
						}
						part := text[found[0]:found[1]]
						if !contains(matches[field], part) {
							matches[field] = append(matches[field], part)
						}
					}
				}
			}
			if o.any {
				selected = selected || matched
			} else {
				selected = selected && matched
			}
		}
		if o.invert {
			selected = !selected
		}
		if selected {
			record.Result["matches"] = matches
			record.Result["matched_fields"] = matchedFields
			output = append(output, record.Result)
		}
	}
	return output, nil
}

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
	terms := []searchTerm{}
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
			terms = append(terms, searchTerm{selected, pattern})
		}
	}
	if len(terms) == 0 {
		if !r.machine() {
			return cmd.Help()
		}
		return usage("search requires a nonempty query pattern")
	}
	var patterns []searchPattern
	switch o.regexEngine {
	case "go", "":
		var err error
		patterns, err = compileSearch(terms, o)
		if err != nil {
			return err
		}
	case "python":
		if err := r.checkPython(false); err != nil {
			return err
		}
	default:
		return usage("unknown regex engine %q; choose go or python", o.regexEngine)
	}
	records, _, err := discover(true)
	if err != nil {
		return err
	}
	inputRecords := []searchRecord{}
	for _, record := range records {
		values := map[string][]any{"name": {textValue(record["name"])}, "path": {textValue(record["path"])}, "session_name": {textValue(record["session_name"])}, "window": {}, "pane": {}}
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
							if nonemptySearchCommand(rawCommand) {
								values["pane"] = append(values["pane"], rawCommand)
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
		inputRecords = append(inputRecords, searchRecord{values, result})
	}
	var matches []map[string]any
	if o.regexEngine == "python" {
		matches, err = r.pythonMatches(terms, inputRecords, o)
	} else {
		matches, err = r.nativeMatches(patterns, inputRecords, o)
	}
	if err != nil {
		return err
	}
	return r.writeMatches(matches)
}

func (r *invocation) pythonMatches(terms []searchTerm, inputRecords []searchRecord, o *options) ([]map[string]any, error) {
	payload, err := json.Marshal(map[string]any{"terms": terms, "records": inputRecords, "ignore_case": o.ignoreCase, "smart_case": o.smartCase, "fixed": o.fixed, "word": o.word, "any": o.any, "invert": o.invert})
	if err != nil {
		return nil, err
	}
	result, err := r.process([]string{pythonExecutable(), "-c", searchPython}, "", bytes.NewReader(payload), false, nil)
	if err != nil {
		return nil, err
	}
	if result.Status != 0 {
		return nil, usage("invalid search expression: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}
	if result.Truncated {
		return nil, fmt.Errorf("search compatibility output exceeds %d bytes", captureLimit)
	}
	var matches []map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &matches); err != nil {
		return nil, err
	}
	return matches, nil
}

func (r *invocation) writeMatches(matches []map[string]any) error {
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

func nonemptySearchCommand(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case int:
		return v != 0
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}
