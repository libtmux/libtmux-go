package tmux_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/libtmux/libtmux-go"
)

type serverOptionSignatures interface {
	Backspace() tmux.OptionValue[string]
	BufferLimit() tmux.OptionValue[int64]
	CommandAlias() tmux.OptionValue[tmux.SparseArray[string]]
	ExitEmpty() tmux.OptionValue[bool]
}

type sessionOptionSignatures interface {
	DestroyUnattached() tmux.OptionValue[tmux.DestroyUnattached]
	Mouse() tmux.OptionValue[bool]
	StatusFormat() tmux.OptionValue[tmux.SparseArray[string]]
	UpdateEnvironment() tmux.OptionValue[tmux.SparseArray[string]]
}

type windowOptionSignatures interface {
	AggressiveResize() tmux.OptionValue[bool]
	AllowPassthrough() tmux.OptionValue[tmux.AllowPassthrough]
	MonitorSilence() tmux.OptionValue[int64]
	PaneColours() tmux.OptionValue[tmux.SparseArray[string]]
	WindowStatusStyle() tmux.OptionValue[string]
	XTermKeys() tmux.OptionValue[bool]
}

type paneOptionSignatures interface {
	AllowPassthrough() tmux.OptionValue[tmux.AllowPassthrough]
	CursorColour() tmux.OptionValue[string]
	PaneColours() tmux.OptionValue[tmux.SparseArray[string]]
	SynchronizePanes() tmux.OptionValue[bool]
}

type scalarOptionSetterSignatures interface {
	SetMouse(context.Context, bool) error
	SetStatus(context.Context, tmux.Status) error
}

type windowScalarOptionSetterSignatures interface {
	SetPaneBorderStatus(context.Context, tmux.PaneBorderStatus) error
	SetMonitorSilence(context.Context, int64) error
	SetPaneBorderFormat(context.Context, string) error
}

type paneScalarOptionSetterSignatures interface {
	SetAllowPassthrough(context.Context, tmux.AllowPassthrough) error
}

type serverScalarOptionSetterSignatures interface {
	SetBufferLimit(context.Context, int64) error
	SetClipboard(context.Context, tmux.SetClipboard) error
}

type globalSessionScalarOptionSetterSignatures interface {
	SetTitles(context.Context, bool) error
	SetTitlesString(context.Context, string) error
}

type globalWindowScalarOptionSetterSignatures interface {
	SetPaneBorderStatus(context.Context, tmux.PaneBorderStatus) error
}

type serverArrayOptionSetterSignatures interface {
	SetCodepointWidths(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
	SetCommandAlias(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
	SetTerminalFeatures(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
	SetTerminalOverrides(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
	SetUserKeys(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
}

type sessionArrayOptionSetterSignatures interface {
	SetStatusFormat(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
	SetUpdateEnvironment(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
}

type windowArrayOptionSetterSignatures interface {
	SetPaneColours(context.Context, tmux.SparseArray[string]) (tmux.SetArrayResult, error)
}

type serverHookSignatures interface {
	AfterBindKey() tmux.OptionValue[tmux.SparseArray[string]]
	WindowLinked() tmux.OptionValue[tmux.SparseArray[string]]
}

type sessionHookSignatures interface {
	AfterBindKey() tmux.OptionValue[tmux.SparseArray[string]]
	WindowLinked() tmux.OptionValue[tmux.SparseArray[string]]
}

type windowHookSignatures interface {
	PaneDied() tmux.OptionValue[tmux.SparseArray[string]]
	WindowLinked() tmux.OptionValue[tmux.SparseArray[string]]
	WindowResized() tmux.OptionValue[tmux.SparseArray[string]]
}

type paneHookSignatures interface {
	PaneDied() tmux.OptionValue[tmux.SparseArray[string]]
}

type choiceValue interface {
	String() string
	Valid() bool
}

var (
	_ serverOptionSignatures                    = tmux.ServerOptionValues{}
	_ sessionOptionSignatures                   = tmux.SessionOptionValues{}
	_ windowOptionSignatures                    = tmux.WindowOptionValues{}
	_ paneOptionSignatures                      = tmux.PaneOptionValues{}
	_ serverHookSignatures                      = tmux.ServerHookValues{}
	_ sessionHookSignatures                     = tmux.SessionHookValues{}
	_ windowHookSignatures                      = tmux.WindowHookValues{}
	_ paneHookSignatures                        = tmux.PaneHookValues{}
	_ scalarOptionSetterSignatures              = tmux.Session{}
	_ windowScalarOptionSetterSignatures        = tmux.Window{}
	_ paneScalarOptionSetterSignatures          = tmux.Pane{}
	_ serverScalarOptionSetterSignatures        = tmux.Server{}
	_ globalSessionScalarOptionSetterSignatures = tmux.GlobalSessionScope{}
	_ globalWindowScalarOptionSetterSignatures  = tmux.GlobalWindowScope{}
	_ serverArrayOptionSetterSignatures         = tmux.Server{}
	_ sessionArrayOptionSetterSignatures        = tmux.Session{}
	_ sessionArrayOptionSetterSignatures        = tmux.GlobalSessionScope{}
	_ windowArrayOptionSetterSignatures         = tmux.Window{}
	_ windowArrayOptionSetterSignatures         = tmux.GlobalWindowScope{}
	_ windowArrayOptionSetterSignatures         = tmux.Pane{}
	_ choiceValue                               = tmux.ActivityAction("")
	_ choiceValue                               = tmux.AllowPassthrough("")
	_ choiceValue                               = tmux.BellAction("")
	_ choiceValue                               = tmux.ClockModeStyle("")
	_ choiceValue                               = tmux.CopyModeLineNumbers("")
	_ choiceValue                               = tmux.CursorStyle("")
	_ choiceValue                               = tmux.DestroyUnattached("")
	_ choiceValue                               = tmux.DetachOnDestroy("")
	_ choiceValue                               = tmux.ExtendedKeys("")
	_ choiceValue                               = tmux.ExtendedKeysFormat("")
	_ choiceValue                               = tmux.GetClipboard("")
	_ choiceValue                               = tmux.MenuBorderLines("")
	_ choiceValue                               = tmux.MessageLine("")
	_ choiceValue                               = tmux.ModeKeys("")
	_ choiceValue                               = tmux.PaneBorderIndicators("")
	_ choiceValue                               = tmux.PaneBorderLines("")
	_ choiceValue                               = tmux.PaneBorderStatus("")
	_ choiceValue                               = tmux.PaneScrollbars("")
	_ choiceValue                               = tmux.PaneScrollbarsPosition("")
	_ choiceValue                               = tmux.PopupBorderLines("")
	_ choiceValue                               = tmux.PromptCommandCursorStyle("")
	_ choiceValue                               = tmux.PromptCursorStyle("")
	_ choiceValue                               = tmux.RemainOnExit("")
	_ choiceValue                               = tmux.SetClipboard("")
	_ choiceValue                               = tmux.SilenceAction("")
	_ choiceValue                               = tmux.Status("")
	_ choiceValue                               = tmux.StatusJustify("")
	_ choiceValue                               = tmux.StatusKeys("")
	_ choiceValue                               = tmux.StatusPosition("")
	_ choiceValue                               = tmux.VisualActivity("")
	_ choiceValue                               = tmux.VisualBell("")
	_ choiceValue                               = tmux.VisualSilence("")
	_ choiceValue                               = tmux.WindowSize("")
)

var allChoiceConstants = []any{
	tmux.ActivityActionNone, tmux.ActivityActionAny, tmux.ActivityActionCurrent, tmux.ActivityActionOther,
	tmux.AllowPassthroughOff, tmux.AllowPassthroughOn, tmux.AllowPassthroughAll,
	tmux.BellActionNone, tmux.BellActionAny, tmux.BellActionCurrent, tmux.BellActionOther,
	tmux.ClockModeStyle12, tmux.ClockModeStyle24, tmux.ClockModeStyle12WithSeconds, tmux.ClockModeStyle24WithSeconds,
	tmux.CopyModeLineNumbersOff, tmux.CopyModeLineNumbersDefault, tmux.CopyModeLineNumbersAbsolute,
	tmux.CopyModeLineNumbersRelative, tmux.CopyModeLineNumbersHybrid,
	tmux.CursorStyleDefault, tmux.CursorStyleBlinkingBlock, tmux.CursorStyleBlock,
	tmux.CursorStyleBlinkingUnderline, tmux.CursorStyleUnderline, tmux.CursorStyleBlinkingBar, tmux.CursorStyleBar,
	tmux.DestroyUnattachedOff, tmux.DestroyUnattachedOn, tmux.DestroyUnattachedKeepLast, tmux.DestroyUnattachedKeepGroup,
	tmux.DetachOnDestroyOff, tmux.DetachOnDestroyOn, tmux.DetachOnDestroyNoDetached,
	tmux.DetachOnDestroyPrevious, tmux.DetachOnDestroyNext,
	tmux.ExtendedKeysOff, tmux.ExtendedKeysOn, tmux.ExtendedKeysAlways,
	tmux.ExtendedKeysFormatCSIU, tmux.ExtendedKeysFormatXTerm,
	tmux.GetClipboardOff, tmux.GetClipboardBuffer, tmux.GetClipboardRequest, tmux.GetClipboardBoth,
	tmux.MenuBorderLinesSingle, tmux.MenuBorderLinesDouble, tmux.MenuBorderLinesHeavy,
	tmux.MenuBorderLinesSimple, tmux.MenuBorderLinesRounded, tmux.MenuBorderLinesPadded, tmux.MenuBorderLinesNone,
	tmux.MessageLine0, tmux.MessageLine1, tmux.MessageLine2, tmux.MessageLine3, tmux.MessageLine4,
	tmux.ModeKeysEmacs, tmux.ModeKeysVi,
	tmux.PaneBorderIndicatorsOff, tmux.PaneBorderIndicatorsColour,
	tmux.PaneBorderIndicatorsArrows, tmux.PaneBorderIndicatorsBoth,
	tmux.PaneBorderLinesSingle, tmux.PaneBorderLinesDouble, tmux.PaneBorderLinesHeavy,
	tmux.PaneBorderLinesSimple, tmux.PaneBorderLinesNumber, tmux.PaneBorderLinesSpaces,
	tmux.PaneBorderStatusOff, tmux.PaneBorderStatusTop, tmux.PaneBorderStatusBottom,
	tmux.PaneScrollbarsOff, tmux.PaneScrollbarsModal, tmux.PaneScrollbarsOn,
	tmux.PaneScrollbarsPositionRight, tmux.PaneScrollbarsPositionLeft,
	tmux.PopupBorderLinesSingle, tmux.PopupBorderLinesDouble, tmux.PopupBorderLinesHeavy,
	tmux.PopupBorderLinesSimple, tmux.PopupBorderLinesRounded, tmux.PopupBorderLinesPadded, tmux.PopupBorderLinesNone,
	tmux.PromptCommandCursorStyleDefault, tmux.PromptCommandCursorStyleBlinkingBlock,
	tmux.PromptCommandCursorStyleBlock, tmux.PromptCommandCursorStyleBlinkingUnderline,
	tmux.PromptCommandCursorStyleUnderline, tmux.PromptCommandCursorStyleBlinkingBar,
	tmux.PromptCommandCursorStyleBar,
	tmux.PromptCursorStyleDefault, tmux.PromptCursorStyleBlinkingBlock, tmux.PromptCursorStyleBlock,
	tmux.PromptCursorStyleBlinkingUnderline, tmux.PromptCursorStyleUnderline,
	tmux.PromptCursorStyleBlinkingBar, tmux.PromptCursorStyleBar,
	tmux.RemainOnExitOff, tmux.RemainOnExitOn, tmux.RemainOnExitFailed, tmux.RemainOnExitKey,
	tmux.SetClipboardOff, tmux.SetClipboardExternal, tmux.SetClipboardOn,
	tmux.SilenceActionNone, tmux.SilenceActionAny, tmux.SilenceActionCurrent, tmux.SilenceActionOther,
	tmux.StatusOff, tmux.StatusOn, tmux.Status2, tmux.Status3, tmux.Status4, tmux.Status5,
	tmux.StatusJustifyLeft, tmux.StatusJustifyCentre, tmux.StatusJustifyRight, tmux.StatusJustifyAbsoluteCentre,
	tmux.StatusKeysEmacs, tmux.StatusKeysVi, tmux.StatusPositionTop, tmux.StatusPositionBottom,
	tmux.VisualActivityOff, tmux.VisualActivityOn, tmux.VisualActivityBoth,
	tmux.VisualBellOff, tmux.VisualBellOn, tmux.VisualBellBoth,
	tmux.VisualSilenceOff, tmux.VisualSilenceOn, tmux.VisualSilenceBoth,
	tmux.WindowSizeLargest, tmux.WindowSizeSmallest, tmux.WindowSizeManual, tmux.WindowSizeLatest,
}

func TestAllChoiceConstantsCompileExternally(t *testing.T) {
	t.Parallel()
	if got := len(allChoiceConstants); got != 136 {
		t.Fatalf("choice constants = %d, want 136", got)
	}
	for _, constant := range allChoiceConstants {
		value, ok := constant.(choiceValue)
		if !ok || !value.Valid() || value.String() == "" {
			t.Fatalf("choice constant = %#v, want nonempty valid choice", constant)
		}
	}
	zeroValues := []choiceValue{
		tmux.ActivityAction(""), tmux.AllowPassthrough(""), tmux.BellAction(""),
		tmux.ClockModeStyle(""), tmux.CopyModeLineNumbers(""), tmux.CursorStyle(""),
		tmux.DestroyUnattached(""), tmux.DetachOnDestroy(""), tmux.ExtendedKeys(""),
		tmux.ExtendedKeysFormat(""), tmux.GetClipboard(""), tmux.MenuBorderLines(""),
		tmux.MessageLine(""), tmux.ModeKeys(""), tmux.PaneBorderIndicators(""),
		tmux.PaneBorderLines(""), tmux.PaneBorderStatus(""), tmux.PaneScrollbars(""),
		tmux.PaneScrollbarsPosition(""), tmux.PopupBorderLines(""),
		tmux.PromptCommandCursorStyle(""), tmux.PromptCursorStyle(""),
		tmux.RemainOnExit(""), tmux.SetClipboard(""), tmux.SilenceAction(""),
		tmux.Status(""), tmux.StatusJustify(""), tmux.StatusKeys(""),
		tmux.StatusPosition(""), tmux.VisualActivity(""), tmux.VisualBell(""),
		tmux.VisualSilence(""), tmux.WindowSize(""),
	}
	for _, value := range zeroValues {
		if value.Valid() || value.String() != "" {
			t.Fatalf("zero choice = %#v, want empty invalid value", value)
		}
	}
}

func TestTypedOptionSettersExcludeWrongScopesAndAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		receiver any
		methods  []string
	}{
		{receiver: tmux.Server{}, methods: []string{
			"SetMouse", "SetPaneBorderStatus", "SetStatusFormat", "SetSetClipboard",
		}},
		{receiver: tmux.Session{}, methods: []string{
			"SetBufferLimit", "SetCommandAlias", "SetPaneColours",
			"SetSetTitles", "SetSetTitlesString",
		}},
		{receiver: tmux.Window{}, methods: []string{"SetMouse", "SetCommandAlias", "SetClockModeColor"}},
		{receiver: tmux.Pane{}, methods: []string{"SetMouse", "SetStatusFormat", "SetCursorColor"}},
		{receiver: tmux.GlobalSessionScope{}, methods: []string{"SetBufferLimit", "SetCommandAlias", "SetPaneColours"}},
		{receiver: tmux.GlobalWindowScope{}, methods: []string{"SetMouse", "SetStatusFormat"}},
	}
	for _, test := range tests {
		receiver := reflect.TypeOf(test.receiver)
		for _, method := range test.methods {
			if _, found := receiver.MethodByName(method); found {
				t.Errorf("%s unexpectedly exposes %s", receiver.Name(), method)
			}
		}
	}
}

func TestSetArrayResultExposesOnlyConfirmedProgress(t *testing.T) {
	t.Parallel()

	typeOf := reflect.TypeOf(tmux.SetArrayResult{})
	if typeOf.NumField() != 2 || typeOf.Field(0).Name != "Replaced" ||
		typeOf.Field(0).Type.Kind() != reflect.Bool ||
		typeOf.Field(1).Name != "AppliedIndices" ||
		typeOf.Field(1).Type != reflect.TypeOf([]int{}) {
		t.Fatalf("SetArrayResult fields = %#v, want bool Replaced and []int AppliedIndices", reflect.VisibleFields(typeOf))
	}
}

func TestOptionValueErrorExposesOnlySafeOptionName(t *testing.T) {
	t.Parallel()

	typeOf := reflect.TypeOf(tmux.OptionValueError{})
	if typeOf.NumField() != 1 || typeOf.Field(0).Name != "Name" ||
		typeOf.Field(0).Type.Kind() != reflect.String {
		t.Fatalf("OptionValueError fields = %#v, want only string Name", reflect.VisibleFields(typeOf))
	}
}

// libtmux:parity libtmux._internal.constants.HookArray
// libtmux:parity libtmux._internal.constants.Hooks
// libtmux:parity libtmux._internal.constants.Hooks.after_bind_key
// libtmux:parity libtmux._internal.constants.Hooks.after_capture_pane
// libtmux:parity libtmux._internal.constants.Hooks.after_copy_mode
// libtmux:parity libtmux._internal.constants.Hooks.after_display_message
// libtmux:parity libtmux._internal.constants.Hooks.after_display_panes
// libtmux:parity libtmux._internal.constants.Hooks.after_kill_pane
// libtmux:parity libtmux._internal.constants.Hooks.after_list_buffers
// libtmux:parity libtmux._internal.constants.Hooks.after_list_clients
// libtmux:parity libtmux._internal.constants.Hooks.after_list_keys
// libtmux:parity libtmux._internal.constants.Hooks.after_list_panes
// libtmux:parity libtmux._internal.constants.Hooks.after_list_sessions
// libtmux:parity libtmux._internal.constants.Hooks.after_list_windows
// libtmux:parity libtmux._internal.constants.Hooks.after_load_buffer
// libtmux:parity libtmux._internal.constants.Hooks.after_lock_server
// libtmux:parity libtmux._internal.constants.Hooks.after_new_session
// libtmux:parity libtmux._internal.constants.Hooks.after_new_window
// libtmux:parity libtmux._internal.constants.Hooks.after_paste_buffer
// libtmux:parity libtmux._internal.constants.Hooks.after_pipe_pane
// libtmux:parity libtmux._internal.constants.Hooks.after_queue
// libtmux:parity libtmux._internal.constants.Hooks.after_refresh_client
// libtmux:parity libtmux._internal.constants.Hooks.after_rename_session
// libtmux:parity libtmux._internal.constants.Hooks.after_rename_window
// libtmux:parity libtmux._internal.constants.Hooks.after_resize_pane
// libtmux:parity libtmux._internal.constants.Hooks.after_resize_window
// libtmux:parity libtmux._internal.constants.Hooks.after_save_buffer
// libtmux:parity libtmux._internal.constants.Hooks.after_select_layout
// libtmux:parity libtmux._internal.constants.Hooks.after_select_pane
// libtmux:parity libtmux._internal.constants.Hooks.after_select_window
// libtmux:parity libtmux._internal.constants.Hooks.after_send_keys
// libtmux:parity libtmux._internal.constants.Hooks.after_set_buffer
// libtmux:parity libtmux._internal.constants.Hooks.after_set_environment
// libtmux:parity libtmux._internal.constants.Hooks.after_set_hook
// libtmux:parity libtmux._internal.constants.Hooks.after_set_option
// libtmux:parity libtmux._internal.constants.Hooks.after_show_environment
// libtmux:parity libtmux._internal.constants.Hooks.after_show_messages
// libtmux:parity libtmux._internal.constants.Hooks.after_show_options
// libtmux:parity libtmux._internal.constants.Hooks.after_split_window
// libtmux:parity libtmux._internal.constants.Hooks.after_unbind_key
// libtmux:parity libtmux._internal.constants.Hooks.alert_activity
// libtmux:parity libtmux._internal.constants.Hooks.alert_bell
// libtmux:parity libtmux._internal.constants.Hooks.alert_silence
// libtmux:parity libtmux._internal.constants.Hooks.client_active
// libtmux:parity libtmux._internal.constants.Hooks.client_attached
// libtmux:parity libtmux._internal.constants.Hooks.client_dark_theme
// libtmux:parity libtmux._internal.constants.Hooks.client_detached
// libtmux:parity libtmux._internal.constants.Hooks.client_focus_in
// libtmux:parity libtmux._internal.constants.Hooks.client_focus_out
// libtmux:parity libtmux._internal.constants.Hooks.client_light_theme
// libtmux:parity libtmux._internal.constants.Hooks.client_resized
// libtmux:parity libtmux._internal.constants.Hooks.client_session_changed
// libtmux:parity libtmux._internal.constants.Hooks.command_error
// libtmux:parity libtmux._internal.constants.Hooks.pane_died
// libtmux:parity libtmux._internal.constants.Hooks.pane_exited
// libtmux:parity libtmux._internal.constants.Hooks.pane_focus_in
// libtmux:parity libtmux._internal.constants.Hooks.pane_focus_out
// libtmux:parity libtmux._internal.constants.Hooks.pane_mode_changed
// libtmux:parity libtmux._internal.constants.Hooks.pane_set_clipboard
// libtmux:parity libtmux._internal.constants.Hooks.pane_title_changed
// libtmux:parity libtmux._internal.constants.Hooks.session_closed
// libtmux:parity libtmux._internal.constants.Hooks.session_created
// libtmux:parity libtmux._internal.constants.Hooks.session_renamed
// libtmux:parity libtmux._internal.constants.Hooks.session_window_changed
// libtmux:parity libtmux._internal.constants.Hooks.window_layout_changed
// libtmux:parity libtmux._internal.constants.Hooks.window_linked
// libtmux:parity libtmux._internal.constants.Hooks.window_pane_changed
// libtmux:parity libtmux._internal.constants.Hooks.window_renamed
// libtmux:parity libtmux._internal.constants.Hooks.window_resized
// libtmux:parity libtmux._internal.constants.Hooks.window_unlinked
// libtmux:parity libtmux._internal.constants.Options
// libtmux:parity libtmux._internal.constants.PaneOptions
// libtmux:parity libtmux._internal.constants.PaneOptions.allow_passthrough
// libtmux:parity libtmux._internal.constants.PaneOptions.allow_rename
// libtmux:parity libtmux._internal.constants.PaneOptions.alternate_screen
// libtmux:parity libtmux._internal.constants.PaneOptions.cursor_colour
// libtmux:parity libtmux._internal.constants.PaneOptions.cursor_style
// libtmux:parity libtmux._internal.constants.PaneOptions.pane_active_border_style
// libtmux:parity libtmux._internal.constants.PaneOptions.pane_border_style
// libtmux:parity libtmux._internal.constants.PaneOptions.pane_colours
// libtmux:parity libtmux._internal.constants.PaneOptions.pane_scrollbars
// libtmux:parity libtmux._internal.constants.PaneOptions.pane_scrollbars_style
// libtmux:parity libtmux._internal.constants.PaneOptions.remain_on_exit
// libtmux:parity libtmux._internal.constants.PaneOptions.remain_on_exit_format
// libtmux:parity libtmux._internal.constants.PaneOptions.scroll_on_clear
// libtmux:parity libtmux._internal.constants.PaneOptions.synchronize_panes
// libtmux:parity libtmux._internal.constants.PaneOptions.tree_mode_preview_format
// libtmux:parity libtmux._internal.constants.PaneOptions.window_active_style
// libtmux:parity libtmux._internal.constants.PaneOptions.window_style
// libtmux:parity libtmux._internal.constants.ServerOptions
// libtmux:parity libtmux._internal.constants.ServerOptions.backspace
// libtmux:parity libtmux._internal.constants.ServerOptions.buffer_limit
// libtmux:parity libtmux._internal.constants.ServerOptions.command_alias
// libtmux:parity libtmux._internal.constants.ServerOptions.copy_command
// libtmux:parity libtmux._internal.constants.ServerOptions.default_client_command
// libtmux:parity libtmux._internal.constants.ServerOptions.default_terminal
// libtmux:parity libtmux._internal.constants.ServerOptions.editor
// libtmux:parity libtmux._internal.constants.ServerOptions.escape_time
// libtmux:parity libtmux._internal.constants.ServerOptions.exit_empty
// libtmux:parity libtmux._internal.constants.ServerOptions.exit_unattached
// libtmux:parity libtmux._internal.constants.ServerOptions.extended_keys
// libtmux:parity libtmux._internal.constants.ServerOptions.extended_keys_format
// libtmux:parity libtmux._internal.constants.ServerOptions.focus_events
// libtmux:parity libtmux._internal.constants.ServerOptions.get_clipboard
// libtmux:parity libtmux._internal.constants.ServerOptions.history_file
// libtmux:parity libtmux._internal.constants.ServerOptions.message_limit
// libtmux:parity libtmux._internal.constants.ServerOptions.prompt_history_limit
// libtmux:parity libtmux._internal.constants.ServerOptions.set_clipboard
// libtmux:parity libtmux._internal.constants.ServerOptions.terminal_overrides
// libtmux:parity libtmux._internal.constants.ServerOptions.user_keys
// libtmux:parity libtmux._internal.constants.SessionOptions
// libtmux:parity libtmux._internal.constants.SessionOptions.activity_action
// libtmux:parity libtmux._internal.constants.SessionOptions.assume_paste_time
// libtmux:parity libtmux._internal.constants.SessionOptions.base_index
// libtmux:parity libtmux._internal.constants.SessionOptions.bell_action
// libtmux:parity libtmux._internal.constants.SessionOptions.default_command
// libtmux:parity libtmux._internal.constants.SessionOptions.default_shell
// libtmux:parity libtmux._internal.constants.SessionOptions.default_size
// libtmux:parity libtmux._internal.constants.SessionOptions.destroy_unattached
// libtmux:parity libtmux._internal.constants.SessionOptions.detach_on_destroy
// libtmux:parity libtmux._internal.constants.SessionOptions.display_panes_active_colour
// libtmux:parity libtmux._internal.constants.SessionOptions.display_panes_colour
// libtmux:parity libtmux._internal.constants.SessionOptions.display_panes_time
// libtmux:parity libtmux._internal.constants.SessionOptions.display_time
// libtmux:parity libtmux._internal.constants.SessionOptions.focus_follows_mouse
// libtmux:parity libtmux._internal.constants.SessionOptions.history_limit
// libtmux:parity libtmux._internal.constants.SessionOptions.key_table
// libtmux:parity libtmux._internal.constants.SessionOptions.lock_after_time
// libtmux:parity libtmux._internal.constants.SessionOptions.lock_command
// libtmux:parity libtmux._internal.constants.SessionOptions.menu_border_lines
// libtmux:parity libtmux._internal.constants.SessionOptions.menu_border_style
// libtmux:parity libtmux._internal.constants.SessionOptions.menu_selected_style
// libtmux:parity libtmux._internal.constants.SessionOptions.menu_style
// libtmux:parity libtmux._internal.constants.SessionOptions.message_command_style
// libtmux:parity libtmux._internal.constants.SessionOptions.message_format
// libtmux:parity libtmux._internal.constants.SessionOptions.message_line
// libtmux:parity libtmux._internal.constants.SessionOptions.message_style
// libtmux:parity libtmux._internal.constants.SessionOptions.mouse
// libtmux:parity libtmux._internal.constants.SessionOptions.prefix
// libtmux:parity libtmux._internal.constants.SessionOptions.prefix2
// libtmux:parity libtmux._internal.constants.SessionOptions.prompt_command_cursor_style
// libtmux:parity libtmux._internal.constants.SessionOptions.renumber_windows
// libtmux:parity libtmux._internal.constants.SessionOptions.repeat_time
// libtmux:parity libtmux._internal.constants.SessionOptions.set_titles
// libtmux:parity libtmux._internal.constants.SessionOptions.set_titles_string
// libtmux:parity libtmux._internal.constants.SessionOptions.silence_action
// libtmux:parity libtmux._internal.constants.SessionOptions.status
// libtmux:parity libtmux._internal.constants.SessionOptions.status_format
// libtmux:parity libtmux._internal.constants.SessionOptions.status_interval
// libtmux:parity libtmux._internal.constants.SessionOptions.status_justify
// libtmux:parity libtmux._internal.constants.SessionOptions.status_keys
// libtmux:parity libtmux._internal.constants.SessionOptions.status_left
// libtmux:parity libtmux._internal.constants.SessionOptions.status_left_length
// libtmux:parity libtmux._internal.constants.SessionOptions.status_left_style
// libtmux:parity libtmux._internal.constants.SessionOptions.status_position
// libtmux:parity libtmux._internal.constants.SessionOptions.status_right
// libtmux:parity libtmux._internal.constants.SessionOptions.status_right_length
// libtmux:parity libtmux._internal.constants.SessionOptions.status_right_style
// libtmux:parity libtmux._internal.constants.SessionOptions.status_style
// libtmux:parity libtmux._internal.constants.SessionOptions.update_environment
// libtmux:parity libtmux._internal.constants.SessionOptions.visual_activity
// libtmux:parity libtmux._internal.constants.SessionOptions.visual_bell
// libtmux:parity libtmux._internal.constants.SessionOptions.visual_silence
// libtmux:parity libtmux._internal.constants.SessionOptions.word_separators
// libtmux:parity libtmux._internal.constants.WindowOptions
// libtmux:parity libtmux._internal.constants.WindowOptions.aggressive_resize
// libtmux:parity libtmux._internal.constants.WindowOptions.automatic_rename
// libtmux:parity libtmux._internal.constants.WindowOptions.automatic_rename_format
// libtmux:parity libtmux._internal.constants.WindowOptions.clock_mode_colour
// libtmux:parity libtmux._internal.constants.WindowOptions.clock_mode_style
// libtmux:parity libtmux._internal.constants.WindowOptions.copy_mode_current_line_number_style
// libtmux:parity libtmux._internal.constants.WindowOptions.copy_mode_current_match_style
// libtmux:parity libtmux._internal.constants.WindowOptions.copy_mode_line_number_style
// libtmux:parity libtmux._internal.constants.WindowOptions.copy_mode_line_numbers
// libtmux:parity libtmux._internal.constants.WindowOptions.copy_mode_mark_style
// libtmux:parity libtmux._internal.constants.WindowOptions.copy_mode_match_style
// libtmux:parity libtmux._internal.constants.WindowOptions.fill_character
// libtmux:parity libtmux._internal.constants.WindowOptions.main_pane_height
// libtmux:parity libtmux._internal.constants.WindowOptions.main_pane_width
// libtmux:parity libtmux._internal.constants.WindowOptions.mode_keys
// libtmux:parity libtmux._internal.constants.WindowOptions.mode_style
// libtmux:parity libtmux._internal.constants.WindowOptions.monitor_activity
// libtmux:parity libtmux._internal.constants.WindowOptions.monitor_bell
// libtmux:parity libtmux._internal.constants.WindowOptions.monitor_silence
// libtmux:parity libtmux._internal.constants.WindowOptions.other_pane_height
// libtmux:parity libtmux._internal.constants.WindowOptions.other_pane_width
// libtmux:parity libtmux._internal.constants.WindowOptions.pane_active_border_style
// libtmux:parity libtmux._internal.constants.WindowOptions.pane_base_index
// libtmux:parity libtmux._internal.constants.WindowOptions.pane_border_format
// libtmux:parity libtmux._internal.constants.WindowOptions.pane_border_indicators
// libtmux:parity libtmux._internal.constants.WindowOptions.pane_border_lines
// libtmux:parity libtmux._internal.constants.WindowOptions.pane_border_status
// libtmux:parity libtmux._internal.constants.WindowOptions.pane_border_style
// libtmux:parity libtmux._internal.constants.WindowOptions.popup_border_lines
// libtmux:parity libtmux._internal.constants.WindowOptions.popup_border_style
// libtmux:parity libtmux._internal.constants.WindowOptions.popup_style
// libtmux:parity libtmux._internal.constants.WindowOptions.tiled_layout_max_columns
// libtmux:parity libtmux._internal.constants.WindowOptions.tree_mode_preview_format
// libtmux:parity libtmux._internal.constants.WindowOptions.tree_mode_preview_style
// libtmux:parity libtmux._internal.constants.WindowOptions.window_pane_current_status_format
// libtmux:parity libtmux._internal.constants.WindowOptions.window_pane_status_format
// libtmux:parity libtmux._internal.constants.WindowOptions.window_size
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_activity_style
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_bell_style
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_current_format
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_current_style
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_format
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_last_style
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_separator
// libtmux:parity libtmux._internal.constants.WindowOptions.window_status_style
// libtmux:parity libtmux._internal.constants.WindowOptions.wrap_search
func TestGeneratedOptionAndHookMethodCounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  int
	}{
		{name: "server options", value: tmux.ServerOptionValues{}, want: 28},
		{name: "session options", value: tmux.SessionOptionValues{}, want: 54},
		{name: "window options", value: tmux.WindowOptionValues{}, want: 74},
		{name: "pane options", value: tmux.PaneOptionValues{}, want: 19},
		{name: "server hooks", value: tmux.ServerHookValues{}, want: 57},
		{name: "session hooks", value: tmux.SessionHookValues{}, want: 57},
		{name: "window hooks", value: tmux.WindowHookValues{}, want: 13},
		{name: "pane hooks", value: tmux.PaneHookValues{}, want: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := reflect.TypeOf(test.value).NumMethod(); got != test.want {
				t.Fatalf("method count = %d, want %d", got, test.want)
			}
		})
	}
}
