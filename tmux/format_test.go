package tmux

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFormatTemplateUsesSingleEvaluationQuotedFraming(t *testing.T) {
	t.Parallel()

	fields := []formatField{
		{name: "window_name"},
		{name: "pane_title"},
	}
	want := "#{q:window_name}|#{q:pane_title}="
	if got := formatTemplate(fields); got != want {
		t.Fatalf("formatTemplate() = %q, want %q", got, want)
	}
}

// libtmux:parity libtmux.formats.FORMAT_SEPARATOR
// libtmux:parity libtmux.neo.parse_output
// libtmux:parity libtmux.neo.parse_output#parameter-branch:output:ede30dacf1d2
func TestDecodeFormatRecordsPreservesQuotedValues(t *testing.T) {
	t.Parallel()

	version := mustParseTestVersion(t, "3.7")
	fields := []formatField{
		{name: "window_name"},
		{name: "pane_title"},
		{name: "pane_current_path"},
	}
	output := []byte("dev:␞|line1\nline2|pipe\\|percent\\%\\ back\\\\slash=\n開発|:|=\n")

	records, err := decodeFormatRecords(output, version, fields)
	if err != nil {
		t.Fatalf("decodeFormatRecords() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}

	assertFormatValue(t, records[0], "window_name", "dev:␞", true)
	assertFormatValue(t, records[0], "pane_title", "line1\nline2", true)
	assertFormatValue(t, records[0], "pane_current_path", "pipe|percent% back\\slash", true)
	assertFormatValue(t, records[1], "window_name", "開発", true)
	assertFormatValue(t, records[1], "pane_title", ":", true)
	if got := records[0].tmuxVersion(); got.Compare(version) != 0 {
		t.Fatalf("tmuxVersion() = %s, want %s", got, version)
	}
}

func TestDecodeFormatRecordsRejectsMalformedOutput(t *testing.T) {
	t.Parallel()

	fields := []formatField{{name: "pane_title"}}
	version := mustParseTestVersion(t, "3.2a")
	tests := []struct {
		name   string
		output string
	}{
		{name: "missing record terminator", output: "value"},
		{name: "dangling escape", output: "value\\"},
		{name: "invalid escape", output: "value\\x=\n"},
		{name: "missing record newline", output: "value="},
		{name: "non-newline after terminator", output: "value=x\n"},
		{name: "unexpected field separator", output: "value|=\n"},
		{name: "truncated second record", output: "a=\nb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeFormatRecords([]byte(tt.output), version, fields)
			if !errors.Is(err, ErrMalformedFormatOutput) {
				t.Fatalf("decodeFormatRecords() error = %v, want ErrMalformedFormatOutput", err)
			}
			var decodeErr *FormatDecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("decodeFormatRecords() error type = %T, want *FormatDecodeError", err)
			}
			if decodeErr.Field != "pane_title" {
				t.Errorf("FormatDecodeError.Field = %q, want pane_title", decodeErr.Field)
			}
		})
	}
}

func TestFormatDecodeErrorRecordIsOneBased(t *testing.T) {
	t.Parallel()

	fields := []formatField{{name: "pane_title"}}
	version := mustParseTestVersion(t, "3.2a")
	for _, test := range []struct {
		name   string
		output string
		want   int
	}{
		{name: "first record", output: "bad\\x=\n", want: 1},
		{name: "second record", output: "ok=\nbad\\x=\n", want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeFormatRecords([]byte(test.output), version, fields)
			var decodeError *FormatDecodeError
			if !errors.As(err, &decodeError) || decodeError.Record != test.want {
				t.Fatalf("FormatDecodeError = %#v, want record %d", err, test.want)
			}
		})
	}
}

func TestDecodeFormatRecordsRejectsTerminatorBeforeFinalField(t *testing.T) {
	t.Parallel()
	fields := []formatField{{name: "window_name"}, {name: "pane_title"}}
	_, err := decodeFormatRecords(
		[]byte("dev=\n"),
		mustParseTestVersion(t, "3.2a"),
		fields,
	)
	if !errors.Is(err, ErrMalformedFormatOutput) {
		t.Fatalf("decodeFormatRecords() error = %v, want ErrMalformedFormatOutput", err)
	}
	var decodeErr *FormatDecodeError
	if !errors.As(err, &decodeErr) || decodeErr.Field != "window_name" {
		t.Fatalf("decodeFormatRecords() error = %#v, want window_name decode error", err)
	}
}

func TestDecodeFormatRecordsAcceptsNoRows(t *testing.T) {
	t.Parallel()

	records, err := decodeFormatRecords(nil, mustParseTestVersion(t, "3.2a"), []formatField{{name: "pane_id"}})
	if err != nil {
		t.Fatalf("decodeFormatRecords() error = %v", err)
	}
	if records == nil || len(records) != 0 {
		t.Fatalf("decodeFormatRecords() = %#v, want non-nil empty slice", records)
	}
}

func TestDecodeFormatRecordsBackslashEscapesInvalidUTF8AfterUnquoting(t *testing.T) {
	t.Parallel()

	version := mustParseTestVersion(t, "3.2a")
	fields := []formatField{
		{name: "pane_title"},
		{name: "window_name"},
	}
	output := []byte{0xff, '|', 'd', 'e', 'v', '=', '\n'}

	records, err := decodeFormatRecords(output, version, fields)
	if err != nil {
		t.Fatalf("decodeFormatRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	assertFormatValue(t, records[0], "pane_title", `\xff`, true)
	assertFormatValue(t, records[0], "window_name", "dev", true)
}

// libtmux:parity libtmux.neo.FIELD_VERSION
// libtmux:parity libtmux.neo.SCOPES_BY_LIST_CMD
// libtmux:parity libtmux.neo.get_output_format
// libtmux:parity libtmux.neo.get_output_format#parameter-branch:list_cmd:a798a4eda880
// libtmux:parity libtmux.neo.get_output_format#parameter-branch:tmux_version:c8853f8280c7
func TestFormatFieldsForAppliesScopeAndVersionGates(t *testing.T) {
	t.Parallel()

	minimum := mustParseTestVersion(t, "3.2a")
	sessionFields, err := formatFieldsFor("list-sessions", minimum)
	if err != nil {
		t.Fatalf("formatFieldsFor(list-sessions) error = %v", err)
	}
	assertFormatFieldPresence(t, sessionFields, "session_id", true)
	assertFormatFieldPresence(t, sessionFields, "window_id", true)
	assertFormatFieldPresence(t, sessionFields, "pane_id", true)
	assertFormatFieldPresence(t, sessionFields, "client_name", false)
	assertFormatFieldPresence(t, sessionFields, "bracket_paste_flag", false)

	current := mustParseTestVersion(t, "3.7")
	clientFields, err := formatFieldsFor("list-clients", current)
	if err != nil {
		t.Fatalf("formatFieldsFor(list-clients) error = %v", err)
	}
	assertFormatFieldPresence(t, clientFields, "client_name", true)
	assertFormatFieldPresence(t, clientFields, "bracket_paste_flag", true)
	assertFormatFieldPresence(t, clientFields, "buffer_name", false)
	assertFormatFieldPresence(t, clientFields, "copy_cursor_line", false)
}

// libtmux:parity libtmux.neo.Obj.buffer_name
// libtmux:parity libtmux.neo.Obj.buffer_sample
// libtmux:parity libtmux.neo.Obj.buffer_size
// libtmux:parity libtmux.neo.Obj.command_list_alias
// libtmux:parity libtmux.neo.Obj.command_list_name
// libtmux:parity libtmux.neo.Obj.command_list_usage
// libtmux:parity libtmux.neo.Obj.copy_cursor_line
// libtmux:parity libtmux.neo.Obj.copy_cursor_word
// libtmux:parity libtmux.neo.Obj.copy_cursor_x
// libtmux:parity libtmux.neo.Obj.copy_cursor_y
// libtmux:parity libtmux.neo.Obj.current_file
// libtmux:parity libtmux.neo.Obj.scroll_position
// libtmux:parity libtmux.neo.Obj.search_match
// libtmux:parity libtmux.neo.Obj.selection_end_x
// libtmux:parity libtmux.neo.Obj.selection_end_y
// libtmux:parity libtmux.neo.Obj.selection_start_x
// libtmux:parity libtmux.neo.Obj.selection_start_y
func TestHierarchyListingsExcludeNonHierarchyNeoFormats(t *testing.T) {
	t.Parallel()

	version := mustParseTestVersion(t, "3.7")
	nonHierarchy := []string{
		"buffer_name", "buffer_sample", "buffer_size",
		"command_list_alias", "command_list_name", "command_list_usage",
		"copy_cursor_line", "copy_cursor_word", "copy_cursor_x", "copy_cursor_y",
		"current_file", "scroll_position", "search_match",
		"selection_end_x", "selection_end_y", "selection_start_x", "selection_start_y",
	}
	for _, command := range []string{
		"list-sessions", "list-windows", "list-panes", "list-clients",
	} {
		fields, err := formatFieldsFor(command, version)
		if err != nil {
			t.Fatalf("formatFieldsFor(%s) error = %v", command, err)
		}
		for _, name := range nonHierarchy {
			assertFormatFieldPresence(t, fields, name, false)
		}
	}
}

// libtmux:parity libtmux.neo.Obj.active_window_index
// libtmux:parity libtmux.neo.Obj.alternate_saved_x
// libtmux:parity libtmux.neo.Obj.alternate_saved_y
// libtmux:parity libtmux.neo.Obj.bracket_paste_flag
// libtmux:parity libtmux.neo.Obj.client_activity
// libtmux:parity libtmux.neo.Obj.client_cell_height
// libtmux:parity libtmux.neo.Obj.client_cell_width
// libtmux:parity libtmux.neo.Obj.client_control_mode
// libtmux:parity libtmux.neo.Obj.client_created
// libtmux:parity libtmux.neo.Obj.client_discarded
// libtmux:parity libtmux.neo.Obj.client_flags
// libtmux:parity libtmux.neo.Obj.client_height
// libtmux:parity libtmux.neo.Obj.client_key_table
// libtmux:parity libtmux.neo.Obj.client_last_session
// libtmux:parity libtmux.neo.Obj.client_mode_format
// libtmux:parity libtmux.neo.Obj.client_pid
// libtmux:parity libtmux.neo.Obj.client_prefix
// libtmux:parity libtmux.neo.Obj.client_readonly
// libtmux:parity libtmux.neo.Obj.client_session
// libtmux:parity libtmux.neo.Obj.client_termfeatures
// libtmux:parity libtmux.neo.Obj.client_termname
// libtmux:parity libtmux.neo.Obj.client_termtype
// libtmux:parity libtmux.neo.Obj.client_tty
// libtmux:parity libtmux.neo.Obj.client_uid
// libtmux:parity libtmux.neo.Obj.client_user
// libtmux:parity libtmux.neo.Obj.client_utf8
// libtmux:parity libtmux.neo.Obj.client_width
// libtmux:parity libtmux.neo.Obj.client_written
// libtmux:parity libtmux.neo.Obj.config_files
// libtmux:parity libtmux.neo.Obj.cursor_character
// libtmux:parity libtmux.neo.Obj.cursor_flag
// libtmux:parity libtmux.neo.Obj.cursor_x
// libtmux:parity libtmux.neo.Obj.cursor_y
// libtmux:parity libtmux.neo.Obj.history_bytes
// libtmux:parity libtmux.neo.Obj.history_limit
// libtmux:parity libtmux.neo.Obj.history_size
// libtmux:parity libtmux.neo.Obj.insert_flag
// libtmux:parity libtmux.neo.Obj.keypad_cursor_flag
// libtmux:parity libtmux.neo.Obj.keypad_flag
// libtmux:parity libtmux.neo.Obj.last_window_index
// libtmux:parity libtmux.neo.Obj.line
// libtmux:parity libtmux.neo.Obj.mouse_all_flag
// libtmux:parity libtmux.neo.Obj.mouse_any_flag
// libtmux:parity libtmux.neo.Obj.mouse_button_flag
// libtmux:parity libtmux.neo.Obj.mouse_sgr_flag
// libtmux:parity libtmux.neo.Obj.mouse_standard_flag
// libtmux:parity libtmux.neo.Obj.next_session_id
// libtmux:parity libtmux.neo.Obj.origin_flag
// libtmux:parity libtmux.neo.Obj.pane_active
// libtmux:parity libtmux.neo.Obj.pane_at_bottom
// libtmux:parity libtmux.neo.Obj.pane_at_left
// libtmux:parity libtmux.neo.Obj.pane_at_right
// libtmux:parity libtmux.neo.Obj.pane_at_top
// libtmux:parity libtmux.neo.Obj.pane_bg
// libtmux:parity libtmux.neo.Obj.pane_bottom
// libtmux:parity libtmux.neo.Obj.pane_current_command
// libtmux:parity libtmux.neo.Obj.pane_current_path
// libtmux:parity libtmux.neo.Obj.pane_dead
// libtmux:parity libtmux.neo.Obj.pane_dead_signal
// libtmux:parity libtmux.neo.Obj.pane_dead_status
// libtmux:parity libtmux.neo.Obj.pane_dead_time
// libtmux:parity libtmux.neo.Obj.pane_fg
// libtmux:parity libtmux.neo.Obj.pane_flags
// libtmux:parity libtmux.neo.Obj.pane_floating_flag
// libtmux:parity libtmux.neo.Obj.pane_format
// libtmux:parity libtmux.neo.Obj.pane_height
// libtmux:parity libtmux.neo.Obj.pane_in_mode
// libtmux:parity libtmux.neo.Obj.pane_input_off
// libtmux:parity libtmux.neo.Obj.pane_last
// libtmux:parity libtmux.neo.Obj.pane_left
// libtmux:parity libtmux.neo.Obj.pane_marked
// libtmux:parity libtmux.neo.Obj.pane_marked_set
// libtmux:parity libtmux.neo.Obj.pane_mode
// libtmux:parity libtmux.neo.Obj.pane_path
// libtmux:parity libtmux.neo.Obj.pane_pb_progress
// libtmux:parity libtmux.neo.Obj.pane_pb_state
// libtmux:parity libtmux.neo.Obj.pane_pid
// libtmux:parity libtmux.neo.Obj.pane_pipe
// libtmux:parity libtmux.neo.Obj.pane_pipe_pid
// libtmux:parity libtmux.neo.Obj.pane_right
// libtmux:parity libtmux.neo.Obj.pane_search_string
// libtmux:parity libtmux.neo.Obj.pane_start_command
// libtmux:parity libtmux.neo.Obj.pane_start_path
// libtmux:parity libtmux.neo.Obj.pane_synchronized
// libtmux:parity libtmux.neo.Obj.pane_tabs
// libtmux:parity libtmux.neo.Obj.pane_title
// libtmux:parity libtmux.neo.Obj.pane_top
// libtmux:parity libtmux.neo.Obj.pane_tty
// libtmux:parity libtmux.neo.Obj.pane_width
// libtmux:parity libtmux.neo.Obj.pane_x
// libtmux:parity libtmux.neo.Obj.pane_y
// libtmux:parity libtmux.neo.Obj.pane_z
// libtmux:parity libtmux.neo.Obj.pane_zoomed_flag
// libtmux:parity libtmux.neo.Obj.pid
// libtmux:parity libtmux.neo.Obj.scroll_region_lower
// libtmux:parity libtmux.neo.Obj.scroll_region_upper
// libtmux:parity libtmux.neo.Obj.session_activity
// libtmux:parity libtmux.neo.Obj.session_alerts
// libtmux:parity libtmux.neo.Obj.session_attached
// libtmux:parity libtmux.neo.Obj.session_attached_list
// libtmux:parity libtmux.neo.Obj.session_created
// libtmux:parity libtmux.neo.Obj.session_format
// libtmux:parity libtmux.neo.Obj.session_group
// libtmux:parity libtmux.neo.Obj.session_group_attached
// libtmux:parity libtmux.neo.Obj.session_group_attached_list
// libtmux:parity libtmux.neo.Obj.session_group_list
// libtmux:parity libtmux.neo.Obj.session_group_many_attached
// libtmux:parity libtmux.neo.Obj.session_group_size
// libtmux:parity libtmux.neo.Obj.session_grouped
// libtmux:parity libtmux.neo.Obj.session_last_attached
// libtmux:parity libtmux.neo.Obj.session_many_attached
// libtmux:parity libtmux.neo.Obj.session_marked
// libtmux:parity libtmux.neo.Obj.session_name
// libtmux:parity libtmux.neo.Obj.session_path
// libtmux:parity libtmux.neo.Obj.session_stack
// libtmux:parity libtmux.neo.Obj.session_windows
// libtmux:parity libtmux.neo.Obj.socket_path
// libtmux:parity libtmux.neo.Obj.start_time
// libtmux:parity libtmux.neo.Obj.synchronized_output_flag
// libtmux:parity libtmux.neo.Obj.uid
// libtmux:parity libtmux.neo.Obj.user
// libtmux:parity libtmux.neo.Obj.version
// libtmux:parity libtmux.neo.Obj.window_active
// libtmux:parity libtmux.neo.Obj.window_active_clients
// libtmux:parity libtmux.neo.Obj.window_active_clients_list
// libtmux:parity libtmux.neo.Obj.window_active_sessions
// libtmux:parity libtmux.neo.Obj.window_active_sessions_list
// libtmux:parity libtmux.neo.Obj.window_activity
// libtmux:parity libtmux.neo.Obj.window_activity_flag
// libtmux:parity libtmux.neo.Obj.window_bell_flag
// libtmux:parity libtmux.neo.Obj.window_bigger
// libtmux:parity libtmux.neo.Obj.window_cell_height
// libtmux:parity libtmux.neo.Obj.window_cell_width
// libtmux:parity libtmux.neo.Obj.window_end_flag
// libtmux:parity libtmux.neo.Obj.window_flags
// libtmux:parity libtmux.neo.Obj.window_format
// libtmux:parity libtmux.neo.Obj.window_height
// libtmux:parity libtmux.neo.Obj.window_last_flag
// libtmux:parity libtmux.neo.Obj.window_layout
// libtmux:parity libtmux.neo.Obj.window_linked
// libtmux:parity libtmux.neo.Obj.window_linked_sessions
// libtmux:parity libtmux.neo.Obj.window_linked_sessions_list
// libtmux:parity libtmux.neo.Obj.window_marked_flag
// libtmux:parity libtmux.neo.Obj.window_name
// libtmux:parity libtmux.neo.Obj.window_offset_x
// libtmux:parity libtmux.neo.Obj.window_offset_y
// libtmux:parity libtmux.neo.Obj.window_panes
// libtmux:parity libtmux.neo.Obj.window_raw_flags
// libtmux:parity libtmux.neo.Obj.window_silence_flag
// libtmux:parity libtmux.neo.Obj.window_stack_index
// libtmux:parity libtmux.neo.Obj.window_start_flag
// libtmux:parity libtmux.neo.Obj.window_visible_layout
// libtmux:parity libtmux.neo.Obj.window_width
// libtmux:parity libtmux.neo.Obj.window_zoomed_flag
// libtmux:parity libtmux.neo.Obj.wrap_flag
func TestFormatValuesAccessorsPreserveEmptyAndAbsent(t *testing.T) {
	t.Parallel()

	version := mustParseTestVersion(t, "3.7")
	rawValues := []string{"", "1"}
	values, err := newFormatValues(version, []formatField{
		{name: "pane_title"},
		{name: "bracket_paste_flag"},
	}, rawValues)
	if err != nil {
		t.Fatalf("newFormatValues() error = %v", err)
	}
	rawValues[0] = "mutated"
	got := Pane{formats: values}

	if title, ok := got.Title(); ok || title != "" {
		t.Fatalf("Title() = %q, %t, want empty invalid value", title, ok)
	}
	if raw, ok := got.Formats().Raw("pane_title"); !ok || raw != "" {
		t.Fatalf("Raw(pane_title) = %q, %t, want empty present value", raw, ok)
	}
	if flag, ok := got.BracketPasteFlag(); !ok || !flag {
		t.Fatalf("BracketPasteFlag() = %t, %t, want true, true", flag, ok)
	}
	if value, ok := got.Formats().WindowName(); ok || value != "" {
		t.Fatalf("WindowName() = %q, %t, want empty absent value", value, ok)
	}
	if value, ok := got.Formats().Raw("window_name"); ok || value != "" {
		t.Fatalf("Raw(window_name) = %q, %t, want empty absent value", value, ok)
	}
}

func TestZeroFormatValuesIsSafe(t *testing.T) {
	t.Parallel()

	var values FormatValues
	if raw, ok := values.Raw("pane_title"); ok || raw != "" {
		t.Fatalf("Raw(pane_title) = %q, %t, want empty absent value", raw, ok)
	}
	if title, ok := values.PaneTitle(); ok || title != "" {
		t.Fatalf("PaneTitle() = %q, %t, want empty absent value", title, ok)
	}
}

func TestFormatValuesTypedGettersRejectEmptyAndMalformedValues(t *testing.T) {
	t.Parallel()

	values := formatValues{values: map[string]string{
		"bool_false":        "0",
		"bool_true":         "1",
		"bool_text":         "true",
		"bool_other":        "2",
		"empty":             "",
		"integer_negative":  "-42",
		"integer_overflow":  "999999999999999999999999999999999999",
		"time_epoch":        "0",
		"time_malformed":    "yesterday",
		"session_id":        "$12",
		"session_malformed": "$abc",
		"window_id":         "@34",
		"window_malformed":  "@-1",
		"pane_id":           "%56",
		"pane_malformed":    "%",
		"wrong_sigil":       "#78",
		"client_name":       "/dev/pts/9",
		"version":           "3.7a",
		"version_bad":       "release",
	}}

	boolTests := []struct {
		name string
		want bool
		ok   bool
	}{
		{name: "bool_false", want: false, ok: true},
		{name: "bool_true", want: true, ok: true},
		{name: "bool_text"},
		{name: "bool_other"},
		{name: "empty"},
		{name: "absent"},
	}
	for _, test := range boolTests {
		got, ok := values.getBool(test.name)
		if got != test.want || ok != test.ok {
			t.Errorf("getBool(%q) = %t, %t, want %t, %t", test.name, got, ok, test.want, test.ok)
		}
	}

	if got, ok := values.getInt("integer_negative"); !ok || got != -42 {
		t.Errorf("getInt(integer_negative) = %d, %t, want -42, true", got, ok)
	}
	for _, name := range []string{"integer_overflow", "empty", "absent"} {
		if got, ok := values.getInt(name); ok || got != 0 {
			t.Errorf("getInt(%q) = %d, %t, want 0, false", name, got, ok)
		}
	}

	if got, ok := values.getTime("time_epoch"); !ok || !got.Equal(time.Unix(0, 0).UTC()) || got.Location() != time.UTC {
		t.Errorf("getTime(time_epoch) = %v, %t, want Unix epoch in UTC", got, ok)
	}
	for _, name := range []string{"time_malformed", "empty", "absent"} {
		if got, ok := values.getTime(name); ok || !got.IsZero() {
			t.Errorf("getTime(%q) = %v, %t, want zero, false", name, got, ok)
		}
	}

	if got, ok := values.getSessionID("session_id"); !ok || got != SessionID("$12") {
		t.Errorf("getSessionID(session_id) = %q, %t, want $12, true", got, ok)
	}
	if got, ok := values.getWindowID("window_id"); !ok || got != WindowID("@34") {
		t.Errorf("getWindowID(window_id) = %q, %t, want @34, true", got, ok)
	}
	if got, ok := values.getPaneID("pane_id"); !ok || got != PaneID("%56") {
		t.Errorf("getPaneID(pane_id) = %q, %t, want %%56, true", got, ok)
	}
	if got, ok := values.getSessionID("wrong_sigil"); ok || got != "" {
		t.Errorf("getSessionID(wrong_sigil) = %q, %t, want empty, false", got, ok)
	}
	if got, ok := values.getSessionID("session_malformed"); ok || got != "" {
		t.Errorf("getSessionID(session_malformed) = %q, %t, want empty, false", got, ok)
	}
	if got, ok := values.getWindowID("window_malformed"); ok || got != "" {
		t.Errorf("getWindowID(window_malformed) = %q, %t, want empty, false", got, ok)
	}
	if got, ok := values.getPaneID("pane_malformed"); ok || got != "" {
		t.Errorf("getPaneID(pane_malformed) = %q, %t, want empty, false", got, ok)
	}
	if got, ok := values.getClientName("client_name"); !ok || got != ClientName("/dev/pts/9") {
		t.Errorf("getClientName(client_name) = %q, %t, want /dev/pts/9, true", got, ok)
	}

	if got, ok := values.getVersion("version"); !ok || got.String() != "3.7a" || got.Major() != 3 || got.Minor() != 7 {
		t.Errorf("getVersion(version) = %q, %t, want parsed 3.7a", got, ok)
	}
	for _, name := range []string{"version_bad", "empty", "absent"} {
		if got, ok := values.getVersion(name); ok || got.String() != "" {
			t.Errorf("getVersion(%q) = %q, %t, want zero, false", name, got, ok)
		}
	}
}

func TestMaterializedModelsExposeLosslessRawValues(t *testing.T) {
	t.Parallel()

	formats := formatValues{values: map[string]string{"present_empty": ""}}
	models := []struct {
		name string
		raw  func(string) (string, bool)
	}{
		{name: "Session", raw: (Session{formats: formats}).Formats().Raw},
		{name: "Window", raw: (Window{formats: formats}).Formats().Raw},
		{name: "Pane", raw: (Pane{formats: formats}).Formats().Raw},
		{name: "Client", raw: (Client{formats: formats}).Formats().Raw},
	}
	for _, model := range models {
		if got, ok := model.raw("present_empty"); !ok || got != "" {
			t.Errorf("%s.Raw(present_empty) = %q, %t, want empty, true", model.name, got, ok)
		}
		if got, ok := model.raw("absent"); ok || got != "" {
			t.Errorf("%s.Raw(absent) = %q, %t, want empty, false", model.name, got, ok)
		}
	}
}

func TestProjectedFormatAccessorsPreserveDanglingIDs(t *testing.T) {
	t.Parallel()

	version := mustParseTestVersion(t, "3.7")
	snapshot, err := newSnapshot(Server{}, version, snapshotRecords{
		sessions: []formatValues{projectedFormatValues(t, version,
			"session_id", "$0",
			"window_id", "@91",
			"pane_id", "%92",
		)},
		windows: []formatValues{projectedFormatValues(t, version,
			"session_id", "$90",
			"window_id", "@10",
			"window_index", "4",
			"pane_id", "%93",
		)},
		clients: []formatValues{projectedFormatValues(t, version,
			"client_name", "dangling-client",
			"client_termfeatures", "",
			"session_id", "$94",
			"window_id", "@95",
			"window_index", "6",
			"pane_id", "%96",
		)},
	})
	if err != nil {
		t.Fatalf("newSnapshot() error = %v", err)
	}

	session := snapshot.Sessions()[0]
	assertProjectedFormatValue(t, "Session.Formats.WindowID", session.Formats().WindowID, "@91")
	assertProjectedFormatValue(t, "Session.Formats.PaneID", session.Formats().PaneID, "%92")
	if _, err := snapshot.WindowByID(WindowID("@91")); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("WindowByID(@91) error = %v, want ErrSnapshotNotFound", err)
	}
	if _, err := snapshot.PaneByID(PaneID("%92")); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("PaneByID(%%92) error = %v, want ErrSnapshotNotFound", err)
	}

	window := snapshot.Windows()[0]
	assertProjectedFormatValue(t, "Window.Formats.PaneID", window.Formats().PaneID, "%93")
	if _, ok := window.Session(); ok {
		t.Fatal("window resolved dangling session projection")
	}
	if _, err := snapshot.PaneByID(PaneID("%93")); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("PaneByID(%%93) error = %v, want ErrSnapshotNotFound", err)
	}

	client := snapshot.Clients()[0]
	assertProjectedFormatValue(t, "Client.Formats.SessionID", client.Formats().SessionID, "$94")
	assertProjectedFormatValue(t, "Client.Formats.WindowID", client.Formats().WindowID, "@95")
	assertProjectedFormatValue(t, "Client.Formats.PaneID", client.Formats().PaneID, "%96")
	if value, ok := client.TermFeatures(); ok || value != "" {
		t.Fatalf("TermFeatures() = %q, %t, want empty invalid typed value", value, ok)
	}
	if value, ok := client.Formats().Raw("client_termfeatures"); !ok || value != "" {
		t.Fatalf("Client.Formats().Raw(client_termfeatures) = %q, %t, want empty present value", value, ok)
	}
	if _, ok := client.AttachedSession(); ok {
		t.Fatal("client resolved dangling session projection")
	}
	if _, ok := client.AttachedWindow(); ok {
		t.Fatal("client resolved dangling window projection")
	}
	if _, ok := client.AttachedPane(); ok {
		t.Fatal("client resolved dangling pane projection")
	}
}

func TestGeneratedFormatMetadataHasUniqueNames(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, len(generatedFormatFields))
	for _, field := range generatedFormatFields {
		if _, exists := seen[field.name]; exists {
			t.Fatalf("generatedFormatFields contains duplicate %q", field.name)
		}
		seen[field.name] = struct{}{}
	}
	for _, name := range []string{
		"active_window_index",
		"client_name",
		"pane_dead_signal",
		"pane_title",
		"session_id",
		"window_visible_layout",
	} {
		if _, ok := seen[name]; !ok {
			t.Errorf("generatedFormatFields is missing %q", name)
		}
	}
}

func quotedFormatRecord(values []string) []byte {
	const shellEscaped = "|&;<>()$`\\\"'*?[# =%"
	encoded := make([]byte, 0)
	for index, value := range values {
		if index != 0 {
			encoded = append(encoded, '|')
		}
		for valueIndex := range len(value) {
			if strings.IndexByte(shellEscaped, value[valueIndex]) >= 0 {
				encoded = append(encoded, '\\')
			}
			encoded = append(encoded, value[valueIndex])
		}
	}
	return append(encoded, '=', '\n')
}

func assertFormatValue(t *testing.T, values formatValues, name, want string, wantOK bool) {
	t.Helper()

	got, ok := values.get(name)
	if got != want || ok != wantOK {
		t.Errorf("format value %q = %q, %t, want %q, %t", name, got, ok, want, wantOK)
	}
}

func assertProjectedFormatValue[T ~string](
	t *testing.T,
	name string,
	accessor func() (T, bool),
	want T,
) {
	t.Helper()
	got, ok := accessor()
	if !ok || got != want {
		t.Fatalf("%s() = %q, %t, want %q, true", name, got, ok, want)
	}
}

func projectedFormatValues(t *testing.T, version Version, pairs ...string) formatValues {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("projectedFormatValues requires name/value pairs")
	}
	fields := []formatField{
		{name: "version"},
		{name: "pid"},
		{name: "start_time"},
		{name: "socket_path"},
	}
	values := []string{version.String(), "1234", "5678", "/tmp/projected-format-selector"}
	for index := 0; index < len(pairs); index += 2 {
		fields = append(fields, formatField{name: pairs[index]})
		values = append(values, pairs[index+1])
	}
	result, err := newFormatValues(version, fields, values)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertFormatFieldPresence(t *testing.T, fields []formatField, name string, want bool) {
	t.Helper()

	got := slices.ContainsFunc(fields, func(field formatField) bool {
		return field.name == name
	})
	if got != want {
		t.Errorf("format field %q presence = %t, want %t", name, got, want)
	}
}

func mustParseTestVersion(t *testing.T, raw string) Version {
	t.Helper()

	version, err := ParseVersion(raw)
	if err != nil {
		t.Fatalf("ParseVersion(%q) error = %v", raw, err)
	}
	return version
}
