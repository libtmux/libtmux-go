package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This file compares what the load command's loader makes of each file in
// tmuxp's example catalog with the structure tmuxp's own loader makes of it.
// The expected files under testdata/tmuxp/expected are copied from libtmux-rs
// (crates/tmux-workspace/tests/fixtures/tmuxp/expected), where README.md says
// how they were generated; the schema is expected.schema.json there.
//
// The test sets HOME, PWD and the other variables the catalog reads to the
// placeholders that README.md lists, and loads from the directory
// /@WORKSPACE_DIR@, so the paths in the plan are the paths in the expected
// files without rewriting either.

// tmuxpPlaceholders are the variables the catalog reads and what the expected
// files hold in their place.
var tmuxpPlaceholders = map[string]string{
	"HOME":             "/@HOME@",
	"MY_ENV_VAR":       "/@MY_ENV_VAR@",
	"PWD":              "/@PWD@",
	"USER":             "@USER@",
	"SERVER_PORT":      "@SERVER_PORT@",
	"MAIN_PANE_HEIGHT": "@MAIN_PANE_HEIGHT@",
}

const tmuxpWorkspaceDirectory = "/@WORKSPACE_DIR@"

// The plan has no field for a window's environment and shell, for the plugins
// a file names, or for the session's suppress_history, so expectedStructure
// leaves them out. Each is applied through a field that is compared: a pane
// holds its window's shell and environment already, a pane its effective
// suppress_history, and the plan only records that a plugin bridge is needed.

// tmuxpDeviation is one place where the command's plan differs from tmuxp's
// expected structure on purpose. The test fails when the path no longer
// differs, when it differs in a way other than Got, and when a path differs
// that no row names.
type tmuxpDeviation struct {
	Path     string
	Got      any
	Reason   string
	Features []string
}

// tmuxpDeviations holds a file's deviations by name.
var tmuxpDeviations = map[string][]tmuxpDeviation{
	"blank-panes.yaml": {
		// tmuxp turns the empty string into a command that is typed and
		// followed by Enter, so the pane gets a fresh prompt line; the
		// example's window is named "Empty string (return)" for that. The
		// loader reads it as a blank pane. This is a bug to decide on, not a
		// design: nothing in the loader's own documentation asks for it.
		{
			Path:     "/windows/2/panes/0/commands",
			Got:      []any{},
			Reason:   "an empty-string pane is read as a blank pane, where tmuxp presses Enter in it",
			Features: []string{"pane.empty_command"},
		},
	},
	"start-directory.yaml": {
		// The loader cleans every path it resolves. tmux gets the same
		// directory either way, so this is a spelling difference.
		{
			Path:     "/session/start_directory",
			Got:      "/var",
			Reason:   "a trailing slash is dropped from a resolved path; tmux starts in the same directory",
			Features: []string{"session.start_directory"},
		},
		{
			Path:     "/windows/0/start_directory",
			Got:      "/var",
			Reason:   "a trailing slash is dropped from a resolved path; tmux starts in the same directory",
			Features: []string{"session.start_directory"},
		},
		{
			Path:     "/windows/0/panes/0/start_directory",
			Got:      "/var",
			Reason:   "a trailing slash is dropped from a resolved path; tmux starts in the same directory",
			Features: []string{"session.start_directory"},
		},
		{
			Path:     "/windows/0/panes/1/start_directory",
			Got:      "/var",
			Reason:   "a trailing slash is dropped from a resolved path; tmux starts in the same directory",
			Features: []string{"session.start_directory"},
		},
	},
}

// tmuxpRefusal is the error the loader reports for a file it refuses, with the
// features that error is about.
type tmuxpRefusal struct {
	Message  string
	Features []string
}

// tmuxpRefusals holds the files the loader refuses; each is a gap, not a
// fixture to drop. Empty while the loader takes every file.
var tmuxpRefusals = map[string]tmuxpRefusal{}

type tmuxpFeature struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Files []string `json:"files"`
}

type tmuxpRecordDeviation struct {
	Path     string   `json:"path"`
	Reason   string   `json:"reason"`
	Features []string `json:"features"`
}

type tmuxpRecordRefusal struct {
	Message  string   `json:"message"`
	Features []string `json:"features"`
}

type tmuxpRecordFile struct {
	File       string                 `json:"file"`
	Outcome    string                 `json:"outcome"`
	Deviations []tmuxpRecordDeviation `json:"deviations"`
	Refusal    *tmuxpRecordRefusal    `json:"refusal"`
}

type tmuxpRecordFeature struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Detail *string  `json:"detail"`
	Files  []string `json:"files"`
}

type tmuxpRecord struct {
	SchemaVersion int                  `json:"schema_version"`
	Port          string               `json:"port"`
	Surface       string               `json:"surface"`
	Tmuxp         string               `json:"tmuxp"`
	Files         []tmuxpRecordFile    `json:"files"`
	Features      []tmuxpRecordFeature `json:"features"`
}

func tmuxpTestdata(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata", "tmuxp"}, parts...)...)
}

func readTmuxpJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// tmuxString is what tmux receives for an option value: tmuxp hands a
// boolean to libtmux, which sends on or off, and a number as its digits.
func tmuxString(value any) any {
	switch v := value.(type) {
	case bool:
		if v {
			return "on"
		}
		return "off"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return value
}

func tmuxStrings(value any) any {
	m, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := make(map[string]any, len(m))
	for key, v := range m {
		out[key] = tmuxString(v)
	}
	return out
}

// nonEmpty maps the plan's empty string and empty map to null, which is how
// the expected files say a file set nothing.
func nonEmpty(value any) any {
	switch v := value.(type) {
	case string:
		if v == "" {
			return nil
		}
	case map[string]string:
		if len(v) == 0 {
			return nil
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = item
		}
		return out
	}
	return value
}

// planStructure maps a plan onto the expected files' shape, as JSON would
// decode it. The plan already holds each pane's effective directory, shell and
// environment, which is what the expected panes hold.
func planStructure(plan loadPlan) map[string]any {
	windows := []any{}
	for _, window := range plan.Windows {
		panes := []any{}
		for _, pane := range window.Panes {
			commands := []any{}
			for _, command := range pane.Commands {
				commands = append(commands, map[string]any{
					"cmd":   command.Text,
					"enter": command.Enter,
					"runtime": map[string]any{
						"sleep_before": nonZeroSeconds(command.SleepBefore),
						"sleep_after":  nonZeroSeconds(command.SleepAfter),
					},
				})
			}
			panes = append(panes, map[string]any{
				"shell":           nonEmpty(pane.Shell),
				"start_directory": nonEmpty(pane.Directory),
				"environment":     nonEmpty(pane.Environment),
				"focus":           pane.Focus,
				"commands":        commands,
				"runtime":         map[string]any{"suppress_history": pane.SuppressHistory},
			})
		}
		var index any
		if window.Index != nil {
			index = float64(*window.Index)
		}
		windows = append(windows, map[string]any{
			"window_name":     nonEmpty(window.Name),
			"window_index":    index,
			"layout":          nonEmpty(window.Layout),
			"options":         nonEmpty(window.Options),
			"options_after":   nonEmpty(window.OptionsAfter),
			"start_directory": nonEmpty(window.Directory),
			"focus":           window.Focus,
			"panes":           panes,
		})
	}
	return map[string]any{
		"session": map[string]any{
			"session_name":    nonEmpty(plan.Name),
			"start_directory": nonEmpty(plan.Directory),
			"environment":     nonEmpty(plan.Environment),
			"options":         nonEmpty(plan.Options),
			"global_options":  nonEmpty(plan.GlobalOptions),
		},
		"windows": windows,
		"runtime": map[string]any{
			"before_script": nonEmpty(strings.Join(plan.BeforeScript, " ")),
		},
	}
}

func nonZeroSeconds(d time.Duration) any {
	if d == 0 {
		return nil
	}
	return d.Seconds()
}

// expectedStructure reads an expected file and brings it to the shape
// planStructure produces: option values as the strings tmux receives, the
// fields the plan has no place for removed, the identifying keys dropped.
func expectedStructure(t *testing.T, name string) map[string]any {
	t.Helper()
	var expected map[string]any
	readTmuxpJSON(t, tmuxpTestdata("expected", strings.TrimSuffix(name, ".yaml")+".json"), &expected)
	delete(expected, "schema_version")
	delete(expected, "source")
	delete(expected, "tmuxp")
	session := expected["session"].(map[string]any)
	session["options"] = tmuxStrings(session["options"])
	session["global_options"] = tmuxStrings(session["global_options"])
	for _, raw := range expected["windows"].([]any) {
		window := raw.(map[string]any)
		window["options"] = tmuxStrings(window["options"])
		window["options_after"] = tmuxStrings(window["options_after"])
		delete(window, "environment")
		delete(window, "shell")
	}
	runtime := expected["runtime"].(map[string]any)
	delete(runtime, "plugins")
	delete(runtime, "suppress_history")
	return expected
}

func escapePointer(key string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
}

// diffStructure lists the JSON pointers where want and got differ, each with
// the value got holds there. Arrays of different lengths differ as a whole.
func diffStructure(want, got any, path string, out map[string]any) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			out[path] = got
			return
		}
		for _, key := range slices.Sorted(maps.Keys(w)) {
			diffStructure(w[key], g[key], path+"/"+escapePointer(key), out)
		}
		for _, key := range slices.Sorted(maps.Keys(g)) {
			if _, ok := w[key]; !ok {
				out[path+"/"+escapePointer(key)] = g[key]
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			out[path] = got
			return
		}
		for i := range w {
			diffStructure(w[i], g[i], path+"/"+strconv.Itoa(i), out)
		}
	default:
		if !reflect.DeepEqual(want, got) {
			out[path] = got
		}
	}
}

func roundTrip(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// tmuxpOutcome is what loading one file showed.
type tmuxpOutcome struct {
	File       string
	Err        error
	Differ     map[string]any
	Deviations []tmuxpDeviation
	Refusal    tmuxpRefusal
}

// observeTmuxpExample loads one file and returns its outcome. It reports a
// failure for anything the tables above do not account for.
func observeTmuxpExample(t *testing.T, path string) tmuxpOutcome {
	t.Helper()
	name := filepath.Base(path)
	outcome := tmuxpOutcome{File: name}
	doc, lines, err := readDocument(path)
	var plan loadPlan
	if err == nil {
		plan, err = normalizeSource(doc, tmuxpWorkspaceDirectory, lines)
	}
	refusal, refused := tmuxpRefusals[name]
	if err != nil {
		if !refused {
			t.Errorf("%s: the loader refuses it with %q, and no refusal is recorded", name, err)
			return outcome
		}
		if err.Error() != refusal.Message {
			t.Errorf("%s: refusal = %q, want the recorded %q", name, err, refusal.Message)
		}
		outcome.Err, outcome.Refusal = err, refusal
		return outcome
	}
	if refused {
		t.Errorf("%s: the loader accepts it, but a refusal is recorded: %q", name, refusal.Message)
		return outcome
	}
	want := expectedStructure(t, name)
	got := roundTrip(t, planStructure(plan))
	differ := map[string]any{}
	diffStructure(want, got, "", differ)
	outcome.Differ = differ
	outcome.Deviations = tmuxpDeviations[name]
	recorded := map[string]tmuxpDeviation{}
	for _, row := range outcome.Deviations {
		recorded[row.Path] = row
		if row.Reason == "" || len(row.Features) == 0 {
			t.Errorf("%s: deviation at %s needs a reason and the features it limits", name, row.Path)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(differ)) {
		row, known := recorded[path]
		switch {
		case !known:
			t.Errorf("%s: %s differs and no deviation is recorded\n  tmuxp: %v\n  plan:  %v", name, path, pointerValue(want, path), differ[path])
		case !reflect.DeepEqual(roundTrip(t, row.Got), differ[path]):
			t.Errorf("%s: %s = %v, the recorded deviation says %v", name, path, differ[path], row.Got)
		}
	}
	for _, row := range outcome.Deviations {
		if _, differs := differ[row.Path]; !differs {
			t.Errorf("%s: a deviation is recorded at %s, but the plan now matches tmuxp there", name, row.Path)
		}
	}
	return outcome
}

// pointerValue follows an RFC 6901 pointer into a decoded document.
func pointerValue(root any, pointer string) any {
	current := root
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if pointer == "" {
			break
		}
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		switch c := current.(type) {
		case map[string]any:
			current = c[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i >= len(c) {
				return nil
			}
			current = c[i]
		default:
			return nil
		}
	}
	return current
}

// buildTmuxpRecord turns what the test saw into the syntax-support record.
// A feature is refused when a refusal names it, supported with a difference
// when a deviation names it, supported when a file that exercises it loaded
// and no recorded difference is about it (every other path matched), and
// unverified when no such file exists.
func buildTmuxpRecord(outcomes []tmuxpOutcome, features []tmuxpFeature) tmuxpRecord {
	record := tmuxpRecord{
		SchemaVersion: 1,
		Port:          "go",
		Surface:       "tmux-workspace load: readDocument and normalizeSource, the plan the command builds",
		Tmuxp:         "1.74.0",
		Files:         []tmuxpRecordFile{},
		Features:      []tmuxpRecordFeature{},
	}
	byName := map[string]tmuxpOutcome{}
	for _, outcome := range outcomes {
		byName[outcome.File] = outcome
		entry := tmuxpRecordFile{File: outcome.File, Outcome: "matches", Deviations: []tmuxpRecordDeviation{}}
		switch {
		case outcome.Err != nil:
			entry.Outcome = "refused"
			entry.Refusal = &tmuxpRecordRefusal{Message: outcome.Refusal.Message, Features: outcome.Refusal.Features}
		case len(outcome.Deviations) > 0:
			entry.Outcome = "deviates"
			for _, row := range outcome.Deviations {
				entry.Deviations = append(entry.Deviations, tmuxpRecordDeviation{row.Path, row.Reason, row.Features})
			}
		}
		record.Files = append(record.Files, entry)
	}
	for _, feature := range features {
		var refusals, differences []string
		matched := false
		for _, file := range feature.Files {
			outcome, ok := byName[file]
			if !ok {
				continue
			}
			named := false
			if outcome.Err != nil {
				if slices.Contains(outcome.Refusal.Features, feature.ID) {
					refusals = append(refusals, outcome.Refusal.Message)
					named = true
				}
			} else {
				for _, row := range outcome.Deviations {
					if slices.Contains(row.Features, feature.ID) {
						differences = append(differences, row.Reason)
						named = true
					}
				}
			}
			if !named && outcome.Err == nil {
				matched = true
			}
		}
		entry := tmuxpRecordFeature{ID: feature.ID, Files: feature.Files}
		switch {
		case len(refusals) > 0:
			entry.Status, entry.Detail = "refused", joinUnique(refusals)
		case len(differences) > 0:
			entry.Status, entry.Detail = "supported_with_difference", joinUnique(differences)
		case matched:
			entry.Status = "supported"
		default:
			entry.Status = "unverified"
		}
		if entry.Files == nil {
			entry.Files = []string{}
		}
		record.Features = append(record.Features, entry)
	}
	return record
}

func joinUnique(items []string) *string {
	var kept []string
	for _, item := range items {
		if !slices.Contains(kept, item) {
			kept = append(kept, item)
		}
	}
	joined := strings.Join(kept, "; ")
	return &joined
}

func marshalTmuxpRecord(t *testing.T, record tmuxpRecord) []byte {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(record); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestEveryTmuxpExampleMatchesTheStructureTmuxpBuilds loads each file in
// tmuxp's example catalog, kept under workspace/testdata/tmuxp, the way the
// load command does (readDocument, then normalizeSource) and compares the plan
// with the structure tmuxp's own loader produced. A difference is a deviation
// in tmuxpDeviations with its reason, or a bug; a refusal is in tmuxpRefusals.
//
// It then checks the committed testdata/tmuxp/syntax-support.json against what
// it saw. Run with UPDATE_TMUXP_SUPPORT=1 to rewrite that file.
func TestEveryTmuxpExampleMatchesTheStructureTmuxpBuilds(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(tmuxpPlaceholders)) {
		t.Setenv(name, tmuxpPlaceholders[name])
	}
	paths, err := filepath.Glob(tmuxpTestdata("*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 20 {
		t.Fatalf("found %d tmuxp examples, want at least 20", len(paths))
	}
	var outcomes []tmuxpOutcome
	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(strings.TrimSuffix(name, ".yaml"), func(t *testing.T) {
			outcomes = append(outcomes, observeTmuxpExample(t, path))
		})
	}
	for name := range tmuxpDeviations {
		if !slices.Contains(paths, tmuxpTestdata(name)) {
			t.Errorf("deviations are recorded for %s, which is not in the catalog", name)
		}
	}
	for name := range tmuxpRefusals {
		if !slices.Contains(paths, tmuxpTestdata(name)) {
			t.Errorf("a refusal is recorded for %s, which is not in the catalog", name)
		}
	}
	var catalog struct {
		Features []tmuxpFeature `json:"features"`
	}
	readTmuxpJSON(t, tmuxpTestdata("expected", "syntax-features.json"), &catalog)
	known := map[string]bool{}
	for _, feature := range catalog.Features {
		known[feature.ID] = true
	}
	for name, rows := range tmuxpDeviations {
		for _, row := range rows {
			for _, id := range row.Features {
				if !known[id] {
					t.Errorf("%s: deviation at %s names %q, which is not a feature", name, row.Path, id)
				}
			}
		}
	}
	for name, refusal := range tmuxpRefusals {
		for _, id := range refusal.Features {
			if !known[id] {
				t.Errorf("%s: the refusal names %q, which is not a feature", name, id)
			}
		}
	}
	if t.Failed() {
		return
	}
	slices.SortFunc(outcomes, func(a, b tmuxpOutcome) int { return strings.Compare(a.File, b.File) })
	got := marshalTmuxpRecord(t, buildTmuxpRecord(outcomes, catalog.Features))
	recordPath := tmuxpTestdata("syntax-support.json")
	if os.Getenv("UPDATE_TMUXP_SUPPORT") != "" {
		if err := os.WriteFile(recordPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	committed, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, got) {
		t.Fatalf("%s is not what the test observed; rerun with UPDATE_TMUXP_SUPPORT=1 and review the diff\n%s", recordPath, firstDifference(committed, got))
	}
}

func firstDifference(committed, got []byte) string {
	a, b := strings.Split(string(committed), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return fmt.Sprintf("line %d: committed %q, observed %q", i+1, x, y)
		}
	}
	return ""
}

func TestTmuxpRecordStatusesFollowTheOutcomes(t *testing.T) {
	features := []tmuxpFeature{
		{ID: "a", Files: []string{"clean.yaml"}},
		{ID: "b", Files: []string{"differs.yaml"}},
		{ID: "c", Files: []string{"refused.yaml"}},
		{ID: "d", Files: []string{"refused.yaml"}},
		{ID: "e", Files: []string{"differs.yaml", "clean.yaml"}},
		{ID: "f"},
		{ID: "g", Files: []string{"differs.yaml"}},
	}
	record := buildTmuxpRecord([]tmuxpOutcome{
		{File: "clean.yaml"},
		{File: "differs.yaml", Deviations: []tmuxpDeviation{{Path: "/x", Reason: "kept as text", Features: []string{"b"}}}},
		{File: "refused.yaml", Err: fmt.Errorf("no"), Refusal: tmuxpRefusal{Message: "no", Features: []string{"c"}}},
	}, features)
	want := map[string]string{"a": "supported", "b": "supported_with_difference", "c": "refused", "d": "unverified", "e": "supported", "f": "unverified", "g": "supported"}
	for _, feature := range record.Features {
		if feature.Status != want[feature.ID] {
			t.Errorf("feature %s = %s, want %s", feature.ID, feature.Status, want[feature.ID])
		}
	}
	if detail := record.Features[1].Detail; detail == nil || *detail != "kept as text" {
		t.Errorf("detail of b = %v", detail)
	}
	if detail := record.Features[2].Detail; detail == nil || *detail != "no" {
		t.Errorf("detail of c = %v", detail)
	}
}

func TestDiffStructureNamesEachDifferingPath(t *testing.T) {
	want := map[string]any{"a": []any{"x", "y"}, "b": map[string]any{"c": nil, "d/e": 1.0}, "f": "same"}
	got := map[string]any{"a": []any{"x", "z"}, "b": map[string]any{"c": "set", "d/e": 2.0}, "f": "same", "g": true}
	differ := map[string]any{}
	diffStructure(want, got, "", differ)
	wantDiff := map[string]any{"/a/1": "z", "/b/c": "set", "/b/d~1e": 2.0, "/g": true}
	if !reflect.DeepEqual(differ, wantDiff) {
		t.Fatalf("diff = %v, want %v", differ, wantDiff)
	}
}
