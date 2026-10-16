package tmux_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

// tmux refuses a command larger than one client message, and says why. The
// reason must survive the redaction that hides what was typed.
//
//libtmux:real-tmux
func TestAnOversizedSendSaysTheCommandIsTooLong(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pane := tmuxtest.RunInPane(ctx, t, "cat > /dev/null")

	text := strings.Repeat("x", 20_000)
	err := pane.SendKeys(ctx, tmux.SendKeysRequest{Command: &text, Literal: true})
	if err == nil || !strings.Contains(err.Error(), "command too long") {
		t.Fatalf("sending 20 000 bytes to a pane = %v, want it refused as too long", err)
	}
	if strings.Contains(err.Error(), "xxxx") {
		t.Fatalf("the refusal carries what was typed: %.200v", err)
	}
}

// A Writer types whatever it is given, however large, so io.Copy works. Text
// outside printable ASCII costs more of tmux's 16 KiB per byte than ASCII.
//
//libtmux:real-tmux
func TestPaneWriterTypesAnyAmountWhole(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	typed := filepath.Join(t.TempDir(), "typed")
	// A terminal in canonical mode drops what overflows its line buffer, so
	// the reader takes the bytes raw, and says when it is ready for them.
	pane := tmuxtest.RunInPane(ctx, t, "stty -icanon -echo; tmux wait-for -S raw; head -n 2 > '"+
		typed+"'; tmux wait-for -S typed")
	server := pane.Server()
	if err := server.WaitFor(ctx, tmux.WaitForRequest{Channel: "raw"}); err != nil {
		t.Fatal(err)
	}

	// Two- and three-byte characters, copied through a buffer that is no
	// multiple of either, so writes end inside a character.
	split := strings.Repeat("é日", 6_000) + "\n"
	// One line larger than tmux takes in one command, in a single write.
	long := strings.Repeat("x", 20_000) + "\n"
	writer := pane.Writer(ctx)
	source := struct{ io.Reader }{strings.NewReader(split)}
	if _, err := io.CopyBuffer(writer, source, make([]byte, 7_777)); err != nil {
		t.Fatalf("copying %d bytes into the pane: %v", len(split), err)
	}
	if _, err := io.WriteString(writer, long); err != nil {
		t.Fatalf("writing %d bytes at once: %v", len(long), err)
	}
	if err := server.WaitFor(ctx, tmux.WaitForRequest{Channel: "typed"}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(typed)
	if err != nil {
		t.Fatal(err)
	}
	if want := split + long; string(got) != want {
		t.Fatalf("the pane read %d bytes, %d of them in 日, want %d bytes, 18 000 in 日",
			len(got), 3*strings.Count(string(got), "日"), len(want))
	}
}
