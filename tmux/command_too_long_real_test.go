package tmux_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

	_, err := fmt.Fprintln(pane.Writer(ctx), strings.Repeat("x", 20_000))
	if err == nil || !strings.Contains(err.Error(), "command too long") {
		t.Fatalf("writing 20 000 bytes to a pane = %v, want it refused as too long", err)
	}
	if strings.Contains(err.Error(), "xxxx") {
		t.Fatalf("the refusal carries what was typed: %.200v", err)
	}
}
