package tmuxtest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func ExampleWaitFor() {
	attempts := 0
	err := tmuxtest.WaitFor(context.Background(), time.Millisecond, func(context.Context) (bool, error) {
		attempts++
		return attempts == 3, nil
	})
	fmt.Println(err)
	fmt.Println(attempts)
	// Output:
	// <nil>
	// 3
}

// ExampleRunInPane is the whole of testing a program against a real tmux: run
// it, wait for what it draws, type at it.
//
// Everything the example creates ends with the test, and nothing is shared with
// the tmux the developer running it happens to have open.
func ExampleRunInPane() {
	// t is the test these helpers are called from; they report failures
	// through it rather than returning errors.
	var t *testing.T
	ctx := context.Background()
	pane := tmuxtest.RunInPane(ctx, t, "printf 'ready\n'; cat")

	tmuxtest.WaitForLine(ctx, t, pane, "ready")
	tmuxtest.Type(ctx, t, pane, "a line for the program")
	tmuxtest.WaitForLine(ctx, t, pane, "a line for the program")
}

// ExampleWaitForScreen covers a condition the named waits do not: two values
// that have to appear together rather than in any order.
func ExampleWaitForScreen() {
	var t *testing.T
	ctx := context.Background()
	pane := tmuxtest.RunInPane(ctx, t, "printf 'host: up\nqueue: 0\n'")

	tmuxtest.WaitForScreen(ctx, t, pane, "a settled status block", func(screen []string) bool {
		var host, queue bool
		for _, line := range screen {
			host = host || strings.Contains(line, "host: up")
			queue = queue || strings.Contains(line, "queue: 0")
		}
		return host && queue
	})
}
