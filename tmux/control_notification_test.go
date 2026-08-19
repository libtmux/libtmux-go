package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestControlNotificationDecodesOwnedPaneOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		line     string
		wantPane PaneID
		want     []byte
	}{
		{
			name:     "output",
			line:     `%output %5 A\000\015\134\377`,
			wantPane: PaneID("%5"),
			want:     []byte{'A', 0, '\r', '\\', 0xff},
		},
		{
			name:     "extended output",
			line:     `%extended-output %4 18 future : value : stays \134`,
			wantPane: PaneID("%4"),
			want:     []byte(`value : stays \`),
		},
		{
			name:     "empty output",
			line:     `%output %3 `,
			wantPane: PaneID("%3"),
			want:     []byte{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notification, err := ParseControlNotification([]byte(test.line))
			if err != nil {
				t.Fatalf("ParseControlNotification() error = %v", err)
			}
			pane, output, ok := notification.Output()
			if !ok || pane != test.wantPane || !bytes.Equal(output, test.want) {
				t.Fatalf(
					"Output() = (%q, %q, %t), want (%q, %q, true)",
					pane,
					output,
					ok,
					test.wantPane,
					test.want,
				)
			}
			if len(output) != 0 {
				output[0] = 'X'
				_, again, _ := notification.Output()
				if !bytes.Equal(again, test.want) {
					t.Fatalf("Output() aliases returned storage: %q", again)
				}
			}
		})
	}

	notification, err := ParseControlNotification([]byte("%sessions-changed"))
	if err != nil {
		t.Fatal(err)
	}
	if pane, output, ok := notification.Output(); ok || pane != "" || output != nil {
		t.Fatalf("non-output Output() = (%q, %q, %t), want zero values", pane, output, ok)
	}
}

func TestParseControlNotificationRejectsMalformedOutputEscapes(t *testing.T) {
	t.Parallel()

	tests := []string{
		`%output %1 sensitive\`,
		`%output %1 sensitive\12`,
		`%output %1 sensitive\400`,
		`%output %1 sensitive\08x`,
		`%extended-output %1 0 : sensitive\word`,
	}
	for _, line := range tests {
		_, err := ParseControlNotification([]byte(line))
		if !errors.Is(err, ErrMalformedControlNotification) {
			t.Fatalf("ParseControlNotification(%q) error = %v", line, err)
		}
		if strings.Contains(fmt.Sprintf("%v %#v", err, err), "sensitive") {
			t.Fatalf("error disclosed output record: %v", err)
		}
	}
}

// libtmux:parity libtmux._internal.constants.Hooks.from_stdout
// libtmux:parity libtmux._internal.constants.Hooks.from_stdout#parameter-branch:value:8045c0e2d5f6
func TestParseControlNotificationCoversPinnedWireVocabulary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		line      string
		kind      ControlNotificationKind
		minimum   string
		arguments []string
	}{
		{name: "client detached", line: "%client-detached /dev/pts/4", kind: ControlNotificationClientDetached, minimum: "3.2a", arguments: []string{"/dev/pts/4"}},
		{name: "client session changed", line: "%client-session-changed /dev/pts/4 $2 named session", kind: ControlNotificationClientSessionChanged, minimum: "3.2a", arguments: []string{"/dev/pts/4", "$2", "named session"}},
		{name: "config error", line: "%config-error bad configuration", kind: ControlNotificationConfigError, minimum: "3.4", arguments: []string{"bad configuration"}},
		{name: "continue", line: "%continue %1", kind: ControlNotificationContinue, minimum: "3.2a", arguments: []string{"%1"}},
		{name: "exit", line: "%exit lost server connection", kind: ControlNotificationExit, minimum: "3.2a", arguments: []string{"lost server connection"}},
		{name: "extended output", line: `%extended-output %4 18 future-a future-b : value : stays \134`, kind: ControlNotificationExtendedOutput, minimum: "3.2a", arguments: []string{"%4", "18", "future-a", "future-b", `value : stays \134`}},
		{name: "layout change", line: "%layout-change @3 b25f,80x24,0,0,3 b25f,80x24,0,0,3 *", kind: ControlNotificationLayoutChange, minimum: "3.2a", arguments: []string{"@3", "b25f,80x24,0,0,3", "b25f,80x24,0,0,3", "*"}},
		{name: "message", line: "%message message with  two spaces", kind: ControlNotificationMessage, minimum: "3.4", arguments: []string{"message with  two spaces"}},
		{name: "output", line: `%output %5 hello\015\134world`, kind: ControlNotificationOutput, minimum: "3.2a", arguments: []string{"%5", `hello\015\134world`}},
		{name: "pane mode changed", line: "%pane-mode-changed %5", kind: ControlNotificationPaneModeChanged, minimum: "3.2a", arguments: []string{"%5"}},
		{name: "paste buffer changed", line: "%paste-buffer-changed go-buffer", kind: ControlNotificationPasteBufferChanged, minimum: "3.4", arguments: []string{"go-buffer"}},
		{name: "paste buffer deleted", line: "%paste-buffer-deleted go-buffer", kind: ControlNotificationPasteBufferDeleted, minimum: "3.4", arguments: []string{"go-buffer"}},
		{name: "pause", line: "%pause %5", kind: ControlNotificationPause, minimum: "3.2a", arguments: []string{"%5"}},
		{name: "session changed", line: "%session-changed $2 named session", kind: ControlNotificationSessionChanged, minimum: "3.2a", arguments: []string{"$2", "named session"}},
		{name: "session renamed", line: "%session-renamed $2 renamed session", kind: ControlNotificationSessionRenamed, minimum: "3.2a", arguments: []string{"$2", "renamed session"}},
		{name: "session window changed", line: "%session-window-changed $2 @3", kind: ControlNotificationSessionWindowChanged, minimum: "3.2a", arguments: []string{"$2", "@3"}},
		{name: "sessions changed", line: "%sessions-changed", kind: ControlNotificationSessionsChanged, minimum: "3.2a", arguments: []string{}},
		{name: "subscription changed", line: "%subscription-changed status $2 @3 7 %5 future : value : untouched", kind: ControlNotificationSubscriptionChanged, minimum: "3.2a", arguments: []string{"status", "$2", "@3", "7", "%5", "future", "value : untouched"}},
		{name: "unlinked window add", line: "%unlinked-window-add @3", kind: ControlNotificationUnlinkedWindowAdd, minimum: "3.2a", arguments: []string{"@3"}},
		{name: "unlinked window close", line: "%unlinked-window-close @3", kind: ControlNotificationUnlinkedWindowClose, minimum: "3.2a", arguments: []string{"@3"}},
		{name: "unlinked window renamed", line: "%unlinked-window-renamed @3 renamed window", kind: ControlNotificationUnlinkedWindowRenamed, minimum: "3.2a", arguments: []string{"@3", "renamed window"}},
		{name: "window add", line: "%window-add @3", kind: ControlNotificationWindowAdd, minimum: "3.2a", arguments: []string{"@3"}},
		{name: "window close", line: "%window-close @3", kind: ControlNotificationWindowClose, minimum: "3.2a", arguments: []string{"@3"}},
		{name: "window pane changed", line: "%window-pane-changed @3 %5", kind: ControlNotificationWindowPaneChanged, minimum: "3.2a", arguments: []string{"@3", "%5"}},
		{name: "window renamed", line: "%window-renamed @3 renamed window", kind: ControlNotificationWindowRenamed, minimum: "3.2a", arguments: []string{"@3", "renamed window"}},
	}

	seen := make(map[ControlNotificationKind]struct{}, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notification, err := ParseControlNotification([]byte(test.line))
			if err != nil {
				t.Fatalf("ParseControlNotification() error = %v", err)
			}
			if notification.Kind() != test.kind {
				t.Fatalf("Kind() = %q, want %q", notification.Kind(), test.kind)
			}
			if !slices.Equal(notification.Arguments(), test.arguments) {
				t.Fatalf("Arguments() = %#v, want %#v", notification.Arguments(), test.arguments)
			}
			minimum, ok := test.kind.MinimumVersion()
			if !ok || minimum.String() != test.minimum {
				t.Fatalf("MinimumVersion() = (%q, %t), want (%q, true)", minimum, ok, test.minimum)
			}
		})
		if _, duplicate := seen[test.kind]; duplicate {
			t.Fatalf("duplicate kind %q in pinned vocabulary", test.kind)
		}
		seen[test.kind] = struct{}{}
	}
	if len(seen) != 25 {
		t.Fatalf("parsed %d notification kinds, want 25", len(seen))
	}
	if _, ok := ControlNotificationKind("%future-notification").MinimumVersion(); ok {
		t.Fatal("unknown kind has a minimum version")
	}
}

func TestParseControlNotificationPreservesEmptyAndOptionalTails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want []string
	}{
		{line: "%exit", want: []string{}},
		{line: "%layout-change @1 tiled tiled ", want: []string{"@1", "tiled", "tiled", ""}},
		{line: "%message ", want: []string{""}},
		{line: "%output %1 ", want: []string{"%1", ""}},
		{line: "%subscription-changed status $1 - - - : ", want: []string{"status", "$1", "-", "-", "-", ""}},
	}
	for _, test := range tests {
		notification, err := ParseControlNotification([]byte(test.line))
		if err != nil {
			t.Fatalf("ParseControlNotification(%q) error = %v", test.line, err)
		}
		if !slices.Equal(notification.Arguments(), test.want) {
			t.Errorf("ParseControlNotification(%q) arguments = %#v, want %#v", test.line, notification.Arguments(), test.want)
		}
	}
}

func TestParseControlNotificationRejectsMalformedAndUnknownWithoutDisclosure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		line     []byte
		category error
	}{
		{name: "empty", line: []byte{}, category: ErrMalformedControlNotification},
		{name: "not protocol", line: []byte("ordinary output"), category: ErrMalformedControlNotification},
		{name: "unknown", line: []byte("%future-sensitive sensitive-token"), category: ErrUnknownControlNotification},
		{name: "invalid kind", line: []byte("%future_sensitive sensitive-token"), category: ErrMalformedControlNotification},
		{name: "tab kind delimiter", line: []byte("%sessions-changed\tsensitive-token"), category: ErrMalformedControlNotification},
		{name: "line feed", line: []byte("%sessions-changed\n"), category: ErrMalformedControlNotification},
		{name: "embedded line feed", line: []byte("%message sensitive-token\nsecond"), category: ErrMalformedControlNotification},
		{name: "NUL", line: []byte("%message sensitive-token\x00"), category: ErrMalformedControlNotification},
		{name: "missing fixed argument", line: []byte("%continue"), category: ErrMalformedControlNotification},
		{name: "extra fixed argument", line: []byte("%sessions-changed extra"), category: ErrMalformedControlNotification},
		{name: "empty fixed argument", line: []byte("%window-pane-changed @1  %2"), category: ErrMalformedControlNotification},
		{name: "missing required tail", line: []byte("%session-renamed $1"), category: ErrMalformedControlNotification},
		{name: "empty forbidden tail", line: []byte("%session-renamed $1 "), category: ErrMalformedControlNotification},
		{name: "empty optional tail", line: []byte("%exit "), category: ErrMalformedControlNotification},
		{name: "missing colon", line: []byte("%extended-output %1 2 sensitive-token"), category: ErrMalformedControlNotification},
		{name: "too few colon arguments", line: []byte("%subscription-changed status $1 : sensitive-token"), category: ErrMalformedControlNotification},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseControlNotification(test.line)
			if !errors.Is(err, test.category) {
				t.Fatalf("ParseControlNotification() error = %v, want category %v", err, test.category)
			}
			var notificationError *ControlNotificationError
			if !errors.As(err, &notificationError) {
				t.Fatalf("ParseControlNotification() error type = %T, want *ControlNotificationError", err)
			}
			if notificationError.Offset < 0 || notificationError.Reason == "" || notificationError.Category == nil {
				t.Fatalf("ControlNotificationError = %#v, want offset, reason, and category", notificationError)
			}
			serialized := fmt.Sprintf("%v %#v", err, err)
			if strings.Contains(serialized, "sensitive-token") || strings.Contains(serialized, "future-sensitive") {
				t.Fatalf("error disclosed serialized notification: %s", serialized)
			}
		})
	}
}

func TestControlNotificationOwnsArgumentsAndSupportsConcurrentReads(t *testing.T) {
	t.Parallel()

	line := []byte("%session-renamed $7 original name")
	notification, err := ParseControlNotification(line)
	if err != nil {
		t.Fatalf("ParseControlNotification() error = %v", err)
	}
	copy(line, "%session-renamed $7 mutated name!")
	arguments := notification.Arguments()
	arguments[0] = "$99"
	arguments[1] = "mutated"
	if got := notification.Arguments(); !slices.Equal(got, []string{"$7", "original name"}) {
		t.Fatalf("Arguments() after mutation = %#v, want owned values", got)
	}

	const readers = 32
	var wait sync.WaitGroup
	for range readers {
		wait.Go(func() {
			if notification.Kind() != ControlNotificationSessionRenamed {
				t.Errorf("Kind() = %q", notification.Kind())
			}
			if got := notification.Arguments(); !slices.Equal(got, []string{"$7", "original name"}) {
				t.Errorf("Arguments() = %#v", got)
			}
		})
	}
	wait.Wait()
}
