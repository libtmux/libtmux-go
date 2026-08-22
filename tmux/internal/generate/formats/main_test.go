package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckedInFormatAccessorsAreScopedAndUnique(t *testing.T) {
	t.Parallel()

	spec, err := readFormatSpec("spec.json")
	if err != nil {
		t.Fatal(err)
	}
	generated, err := generateFormats(spec)
	if err != nil {
		t.Fatal(err)
	}
	methods := generatedFormatMethods(t, generated)

	wantOverrides := map[string]string{
		"session_windows":        "WindowCount",
		"window_panes":           "PaneCount",
		"window_linked_sessions": "LinkedSessionCount",
		"pane_pipe":              "Piping",
		"pane_pid":               "ProcessPID",
		"client_pid":             "ProcessPID",
		"client_uid":             "ProcessUID",
		"client_user":            "ProcessUser",
	}
	seenOverrides := make(map[string]bool, len(wantOverrides))
	seenMethods := make(map[string]string)
	directCounts := make(map[string]int, len(accessorReceivers))
	for _, receiver := range accessorReceivers {
		for _, field := range spec.Fields {
			if !generateRecordAccessor(receiver.typeName, field) {
				continue
			}
			directCounts[receiver.typeName]++
			want := accessorNameForReceiver(receiver.typeName, field)
			key := receiver.typeName + "." + want
			if previous, found := seenMethods[key]; found {
				t.Errorf("%s and %s both derive %s", previous, field.Name, key)
			}
			seenMethods[key] = field.Name
			if !methods[key] {
				t.Errorf("generated output is missing %s for %s", key, field.Name)
			}
			ownPrefix := strings.ToLower(receiver.typeName) + "_"
			fullName := accessorName(field.Name)
			if strings.HasPrefix(field.Name, ownPrefix) && methods[receiver.typeName+"."+fullName] {
				t.Errorf("generated output retains receiver-stuttering %s.%s", receiver.typeName, fullName)
			}
			if !token.IsIdentifier(want) || !token.IsExported(want) {
				t.Errorf("%s derives invalid exported accessor %q", field.Name, want)
			}
		}
	}
	viewCount := 0
	for _, field := range spec.Fields {
		if !generateFormatValuesAccessor(field) {
			continue
		}
		viewCount++
		accessor := accessorName(field.Name)
		key := "FormatValues." + accessor
		if previous, found := seenMethods[key]; found {
			t.Errorf("%s and %s both derive %s", previous, field.Name, key)
		}
		seenMethods[key] = field.Name
		if !methods[key] {
			t.Errorf("generated output is missing %s for %s", key, field.Name)
		}
		if !token.IsIdentifier(accessor) || !token.IsExported(accessor) {
			t.Errorf("%s derives invalid exported view accessor %q", field.Name, accessor)
		}
	}
	for _, field := range spec.Fields {
		want, overridden := wantOverrides[field.Name]
		if !overridden {
			if field.OwnAccessor != "" {
				t.Errorf("%s has unexpected ownAccessor %q", field.Name, field.OwnAccessor)
			}
			continue
		}
		seenOverrides[field.Name] = true
		if field.OwnAccessor != want {
			t.Errorf("%s ownAccessor = %q, want %q", field.Name, field.OwnAccessor, want)
		}
	}
	for name := range wantOverrides {
		if !seenOverrides[name] {
			t.Errorf("checked-in format spec is missing collision override for %s", name)
		}
	}
	wantDirectCounts := map[string]int{
		"Session": 27,
		"Window":  32,
		"Pane":    77,
		"Client":  24,
	}
	for receiver, want := range wantDirectCounts {
		if got := directCounts[receiver]; got != want {
			t.Errorf("%s direct format accessors = %d, want %d", receiver, got, want)
		}
	}
	if viewCount != 181 {
		t.Errorf("FormatValues accessors = %d, want 181", viewCount)
	}
	if len(methods) != 341 {
		t.Fatalf("generated format accessors = %d, want 341", len(methods))
	}
	for _, absent := range []string{
		"Session.WindowWidth", "Window.SessionName", "Pane.WindowName",
		"Client.PaneActive", "Session.Version",
	} {
		if methods[absent] {
			t.Errorf("cross-scope accessor %s was retained on a core record", absent)
		}
	}
	for _, retained := range []string{
		"FormatValues.WindowWidth", "FormatValues.SessionName",
		"FormatValues.PaneActive", "FormatValues.Version",
	} {
		if !methods[retained] {
			t.Errorf("hierarchy format accessor %s was not retained", retained)
		}
	}
}

func TestReadFormatSpecValidatesOwnAccessorsAndReservations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fields  string
		wantErr string
	}{
		{
			name:   "valid override",
			fields: `{"name":"pane_pipe","scope":"pane","since":"3.2a","kind":"bool","ownAccessor":"Piping"}`,
		},
		{
			name:    "invalid identifier",
			fields:  `{"name":"pane_title","scope":"pane","since":"3.2a","kind":"string","ownAccessor":"not-exported"}`,
			wantErr: "invalid ownAccessor",
		},
		{
			name:    "misplaced override",
			fields:  `{"name":"session_name","scope":"pane","since":"3.2a","kind":"string","ownAccessor":"Name"}`,
			wantErr: "ownAccessor is only valid",
		},
		{
			name:    "generated collision",
			fields:  `{"name":"pane_left","scope":"pane","since":"3.2a","kind":"int","ownAccessor":"Edge"},{"name":"pane_right","scope":"pane","since":"3.2a","kind":"int","ownAccessor":"Edge"}`,
			wantErr: "produce accessor",
		},
		{
			name:    "identity collision",
			fields:  `{"name":"session_name","scope":"session","since":"3.2a","kind":"string","ownAccessor":"ID"}`,
			wantErr: "reserved method",
		},
		{
			name:    "relationship collision",
			fields:  `{"name":"session_name","scope":"session","since":"3.2a","kind":"string","ownAccessor":"Windows"}`,
			wantErr: "reserved method",
		},
		{
			name:    "format view collision",
			fields:  `{"name":"raw","scope":"universal","since":"3.2a","kind":"string"}`,
			wantErr: "reserved method",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "spec.json")
			contents := []byte(`{"schema":2,"fields":[` + test.fields + `]}`)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readFormatSpec(path)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("readFormatSpec() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("readFormatSpec() error = %v, want text %q", err, test.wantErr)
			}
		})
	}
}

func generatedFormatMethods(t *testing.T, source []byte) map[string]bool {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "format_generated.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	methods := make(map[string]bool)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
			continue
		}
		receiver, ok := function.Recv.List[0].Type.(*ast.Ident)
		if ok {
			methods[receiver.Name+"."+function.Name.Name] = true
		}
	}
	return methods
}

func TestGenerateFormatsIsDeterministicAndScopesModelAccessors(t *testing.T) {
	t.Parallel()

	spec := formatSpec{Schema: 2, Fields: []fieldSpec{
		{Name: "buffer_name", Scope: "buffer", Since: "3.2a", Kind: "string"},
		{Name: "client_created", Scope: "client", Since: "3.2a", Kind: "time"},
		{Name: "client_name", Scope: "client", Since: "3.2a", Kind: "client-name"},
		{Name: "client_termfeatures", Scope: "client", Since: "3.2a", Kind: "string"},
		{Name: "hook", Scope: "event", Since: "3.2a", Kind: "string"},
		{Name: "mouse_word", Scope: "context", Since: "3.2a", Kind: "string"},
		{Name: "pane_active", Scope: "pane", Since: "3.2a", Kind: "bool"},
		{Name: "pane_id", Scope: "pane", Since: "3.2a", Kind: "pane-id"},
		{Name: "pane_title", Scope: "pane", Since: "3.2a", Kind: "string"},
		{Name: "pane_width", Scope: "pane", Since: "3.2a", Kind: "int"},
		{Name: "session_id", Scope: "session", Since: "3.2a", Kind: "session-id"},
		{Name: "session_name", Scope: "session", Since: "3.2a", Kind: "string"},
		{Name: "version", Scope: "universal", Since: "3.2a", Kind: "version"},
		{Name: "window_id", Scope: "window", Since: "3.2a", Kind: "window-id"},
		{Name: "window_index", Scope: "window", Since: "3.2a", Kind: "int"},
		{Name: "window_name", Scope: "window", Since: "3.2a", Kind: "string"},
	}}

	first, err := generateFormats(spec)
	if err != nil {
		t.Fatalf("generateFormats() error = %v", err)
	}
	second, err := generateFormats(spec)
	if err != nil {
		t.Fatalf("second generateFormats() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generateFormats() output changed between identical calls")
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "format_generated.go", first, parser.AllErrors); err != nil {
		t.Fatalf("generated Go does not parse: %v", err)
	}
	output := string(first)
	for _, method := range []string{
		"func (s Session) Name() (string, bool)",
		"func (w Window) Name() (string, bool)",
		"func (p Pane) Active() (bool, bool)",
		"func (p Pane) Title() (string, bool)",
		"func (p Pane) Width() (int, bool)",
		"func (c Client) Created() (time.Time, bool)",
		"func (c Client) TermFeatures() (string, bool)",
		"func (v FormatValues) ClientCreated() (time.Time, bool)",
		"func (v FormatValues) ClientName() (ClientName, bool)",
		"func (v FormatValues) ClientTermFeatures() (string, bool)",
		"func (v FormatValues) PaneActive() (bool, bool)",
		"func (v FormatValues) PaneID() (PaneID, bool)",
		"func (v FormatValues) PaneTitle() (string, bool)",
		"func (v FormatValues) PaneWidth() (int, bool)",
		"func (v FormatValues) SessionID() (SessionID, bool)",
		"func (v FormatValues) SessionName() (string, bool)",
		"func (v FormatValues) Version() (Version, bool)",
		"func (v FormatValues) WindowID() (WindowID, bool)",
		"func (v FormatValues) WindowIndex() (int, bool)",
		"func (v FormatValues) WindowName() (string, bool)",
	} {
		if !strings.Contains(output, method) {
			t.Errorf("generated output is missing %s", method)
		}
	}
	for _, kind := range []string{
		"formatKindString",
		"formatKindBool",
		"formatKindInt",
		"formatKindTime",
		"formatKindSessionID",
		"formatKindWindowID",
		"formatKindPaneID",
		"formatKindClientName",
		"formatKindVersion",
	} {
		if !strings.Contains(output, "kind: "+kind) {
			t.Errorf("generated output is missing %s metadata", kind)
		}
	}
	for _, method := range []string{
		"func (v formatValues)",
		"func (s Session) SessionID(",
		"func (s Session) ClientTermFeatures(",
		"func (s Session) WindowID(",
		"func (s Session) PaneID(",
		"func (s Session) Version(",
		"func (w Window) ClientTermFeatures(",
		"func (w Window) PaneID(",
		"func (w Window) Version(",
		"func (p Pane) ClientTermFeatures(",
		"func (p Pane) Version(",
		"func (c Client) SessionID(",
		"func (c Client) WindowID(",
		"func (c Client) PaneID(",
		"func (c Client) Version(",
		"func (w Window) SessionID(",
		"func (w Window) WindowID(",
		"func (w Window) WindowIndex(",
		"func (p Pane) SessionID(",
		"func (p Pane) WindowID(",
		"func (p Pane) WindowIndex(",
		"func (p Pane) PaneID(",
		"func (c Client) ClientName(",
		"BufferName() (string, bool)",
		"Hook() (string, bool)",
		"MouseWord() (string, bool)",
	} {
		if strings.Contains(output, method) {
			t.Errorf("generated output unexpectedly contains %s", method)
		}
	}
}

func TestValidateFormatSpecRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec formatSpec
	}{
		{
			name: "unsupported schema",
			spec: formatSpec{Schema: 1, Fields: []fieldSpec{
				{Name: "pane_title", Scope: "pane", Since: "3.2a", Kind: "string"},
			}},
		},
		{
			name: "missing kind",
			spec: formatSpec{Schema: 2, Fields: []fieldSpec{
				{Name: "pane_title", Scope: "pane", Since: "3.2a"},
			}},
		},
		{
			name: "unknown kind",
			spec: formatSpec{Schema: 2, Fields: []fieldSpec{
				{Name: "pane_title", Scope: "pane", Since: "3.2a", Kind: "mystery"},
			}},
		},
		{
			name: "duplicate",
			spec: formatSpec{Schema: 2, Fields: []fieldSpec{
				{Name: "pane_title", Scope: "pane", Since: "3.2a", Kind: "string"},
				{Name: "pane_title", Scope: "pane", Since: "3.2a", Kind: "string"},
			}},
		},
		{
			name: "unknown scope",
			spec: formatSpec{Schema: 2, Fields: []fieldSpec{
				{Name: "pane_title", Scope: "mystery", Since: "3.2a", Kind: "string"},
			}},
		},
		{
			name: "invalid name",
			spec: formatSpec{Schema: 2, Fields: []fieldSpec{
				{Name: "pane-title", Scope: "pane", Since: "3.2a", Kind: "string"},
			}},
		},
		{
			name: "invalid version",
			spec: formatSpec{Schema: 2, Fields: []fieldSpec{
				{Name: "pane_title", Scope: "pane", Since: "next", Kind: "string"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := validateFormatSpec(tt.spec); err == nil {
				t.Fatal("validateFormatSpec() error = nil, want validation failure")
			}
		})
	}
}

func TestValidateFormatSpecAcceptsEveryClosedKind(t *testing.T) {
	t.Parallel()

	kinds := []string{
		"string", "bool", "int", "time", "session-id", "window-id",
		"pane-id", "client-name", "version",
	}
	fields := make([]fieldSpec, len(kinds))
	for index, kind := range kinds {
		fields[index] = fieldSpec{
			Name:  "field_" + strings.ReplaceAll(kind, "-", "_"),
			Scope: "universal",
			Since: "3.2a",
			Kind:  formatKind(kind),
		}
	}
	if err := validateFormatSpec(formatSpec{Schema: 2, Fields: fields}); err != nil {
		t.Fatalf("validateFormatSpec() error = %v", err)
	}
}

func TestReadFormatSpecRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	valid := `{"schema":2,"fields":[{"name":"pane_title","scope":"pane","since":"3.2a","kind":"string"}]}`
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "whitespace", content: valid + " \n\t"},
		{name: "second object", content: valid + `{}`, wantErr: true},
		{name: "closing bracket", content: valid + `]`, wantErr: true},
		{name: "closing brace", content: valid + `}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "spec.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readFormatSpec(path)
			if (err != nil) != test.wantErr {
				t.Fatalf("readFormatSpec() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

// libtmux:parity libtmux.formats.CLIENT_FORMATS
// libtmux:parity libtmux.formats.PANE_FORMATS
// libtmux:parity libtmux.formats.SESSION_FORMATS
// libtmux:parity libtmux.formats.WINDOW_FORMATS
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
func TestCheckedInFormatSpecGeneratesCheckedInOutput(t *testing.T) {
	t.Parallel()

	spec, err := readFormatSpec("spec.json")
	if err != nil {
		t.Fatalf("read checked-in format spec: %v", err)
	}
	if len(spec.Fields) != 200 {
		t.Fatalf("checked-in format fields = %d, want 200", len(spec.Fields))
	}
	if spec.Schema != 2 {
		t.Fatalf("checked-in format schema = %d, want 2", spec.Schema)
	}
	foundCopyCursorLine := false
	for _, field := range spec.Fields {
		if field.Name != "copy_cursor_line" {
			continue
		}
		foundCopyCursorLine = true
		if field.Kind != formatKindString {
			t.Errorf("copy_cursor_line kind = %q, want %q for textual line content", field.Kind, formatKindString)
		}
	}
	if !foundCopyCursorLine {
		t.Error("checked-in format spec is missing copy_cursor_line")
	}
	for _, required := range []string{"alternate_on", "mouse_utf8_flag"} {
		found := false
		for _, field := range spec.Fields {
			if field.Name == required && field.Scope == "pane" && field.Since == "3.2a" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("checked-in format spec is missing pane field %q at 3.2a", required)
		}
	}

	generated, err := generateFormats(spec)
	if err != nil {
		t.Fatalf("generate checked-in formats: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "..", "format_generated.go"))
	if err != nil {
		t.Fatalf("read checked-in generated formats: %v", err)
	}
	if !bytes.Equal(generated, want) {
		t.Fatal("checked-in format_generated.go is stale; run go generate ./...")
	}
}

func TestGeneratedFormatAccessorsDocumentSnapshotContracts(t *testing.T) {
	t.Parallel()

	spec, err := readFormatSpec("spec.json")
	if err != nil {
		t.Fatalf("read checked-in format spec: %v", err)
	}
	generated, err := generateFormats(spec)
	if err != nil {
		t.Fatalf("generate checked-in formats: %v", err)
	}
	documentation := generatedAccessorDocumentation(t, generated)

	wantAccessors := 0
	for _, field := range spec.Fields {
		if !generateFormatValuesAccessor(field) {
			continue
		}
		wantAccessors++
		accessor := accessorName(field.Name)
		key := "FormatValues." + accessor
		doc, found := documentation[key]
		if !found {
			t.Errorf("generated accessor %s has no documentation", key)
			continue
		}
		if !strings.HasPrefix(doc, accessor+" ") {
			t.Errorf("generated accessor %s documentation is not identifier-led: %q", key, doc)
		}
		for _, required := range []string{
			"#{" + field.Name + "}",
			field.Scope + "-scoped",
			"tmux " + field.Since + " or later",
			"a materialized hierarchy record",
			"typed " + accessorKinds[field.Kind].returnType + " value and an ok result",
			"not a live tmux read",
			"[Server.Snapshot]",
			"projected cross-scope fields do not guarantee that the referenced object is present in the same snapshot",
			"ok == false means the field was absent, empty, or malformed",
			"[FormatValues.Raw]",
			"exact materialized expansion",
		} {
			if !strings.Contains(doc, required) {
				t.Errorf("generated accessor %s documentation does not contain %q: %q", key, required, doc)
			}
		}
	}
	for _, receiver := range accessorReceivers {
		for _, field := range spec.Fields {
			if !generateRecordAccessor(receiver.typeName, field) {
				continue
			}
			wantAccessors++
			accessor := accessorNameForReceiver(receiver.typeName, field)
			key := receiver.typeName + "." + accessor
			doc, found := documentation[key]
			if !found {
				t.Errorf("generated accessor %s has no documentation", key)
				continue
			}
			if !strings.HasPrefix(doc, accessor+" ") {
				t.Errorf("generated accessor %s documentation is not identifier-led: %q", key, doc)
			}
			for _, required := range []string{
				"#{" + field.Name + "}",
				field.Scope + "-scoped",
				"tmux " + field.Since + " or later",
				"this " + receiver.typeName + "'s materialized",
				"typed " + accessorKinds[field.Kind].returnType + " value and an ok result",
				"not a live tmux read",
				"[Server.Snapshot]",
				"[" + receiver.typeName + ".Formats]",
				"ok == false means the field was absent, empty, or malformed",
				"[FormatValues.Raw]",
				"exact materialized expansion",
			} {
				if !strings.Contains(doc, required) {
					t.Errorf("generated accessor %s documentation does not contain %q: %q", key, required, doc)
				}
			}
		}
	}
	if wantAccessors != 341 {
		t.Fatalf("generated format accessors = %d, want 341", wantAccessors)
	}
	if len(documentation) != wantAccessors {
		t.Errorf("documented generated accessors = %d, want %d", len(documentation), wantAccessors)
	}
}

func generatedAccessorDocumentation(t *testing.T, source []byte) map[string]string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "format_generated.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse generated formats: %v", err)
	}
	documentation := make(map[string]string)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !function.Name.IsExported() || function.Recv == nil || function.Doc == nil {
			continue
		}
		receiver, ok := function.Recv.List[0].Type.(*ast.Ident)
		if !ok {
			continue
		}
		documentation[receiver.Name+"."+function.Name.Name] = strings.TrimSpace(function.Doc.Text())
	}
	return documentation
}
