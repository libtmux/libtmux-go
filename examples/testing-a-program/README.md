# testing-a-program

A whole `go test` file that runs a program in a real tmux pane, types at it,
and waits for the answer. This directory holds only the test.

## Running it

```console
$ go -C examples test ./testing-a-program
```

## The test

<!-- docs:file examples/testing-a-program/greeter_test.go -->

```go
package greeter_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Main(m))
}

func TestGreeterAnswersALine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A stand-in for your program: it greets each name it reads.
	greeter := `while read name; do echo "hello, $name"; done`
	pane := tmuxtest.RunInPane(ctx, t, greeter)

	tmuxtest.Type(ctx, t, pane, "world")
	tmuxtest.WaitForLine(ctx, t, pane, "hello, world")
}
```

<!-- docs:end -->

## What to look at

**The wait names the answer.** The pane echoes `world` as it is typed, so
waiting for `world` would pass whether or not the greeter ran. `hello, world`
appears only if it did.

**`TestMain` is required.** `tmuxtest.Main` removes the servers the tests
started and checks that each one's process died before its socket goes.

**Nothing is shared.** The pane runs in a server on a socket of its own, with a
config of its own, so the test reaches neither your tmux nor another test's.

[`tmux/tmuxtest`](../../tmux/tmuxtest/) has the other waits and what a failed
one prints.
