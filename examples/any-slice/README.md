# any-slice

`tmuxq` on a slice of your own. Nothing in the package names a tmux type, so
this program needs no tmux at all.

## Running it

```console
$ go -C examples run ./any-slice
```

It prints:

<!-- docs:output examples/any-slice/output.txt -->

```text
open: test
open: deploy
first open: test
the only open task: tmuxq: multiple matches
```

<!-- docs:end -->

## The program

<!-- docs:file examples/any-slice/main.go -->

```go
// Command any-slice filters a slice of its own with tmuxq, without tmux.
package main

import (
	"fmt"

	"github.com/libtmux/libtmux-go/tmuxq"
)

func main() {
	type task struct {
		name string
		done bool
	}
	tasks := []task{{"build", true}, {"test", false}, {"deploy", false}}
	open := func(t *task) bool { return !t.done }

	for _, t := range tmuxq.Where(tasks, open) {
		fmt.Println("open:", t.name)
	}
	first, _ := tmuxq.First(tasks, open)
	fmt.Println("first open:", first.name)

	// Two tasks are open, so asking for the only one is an error.
	_, err := tmuxq.ExactlyOne(tasks, open)
	fmt.Println("the only open task:", err)
}
```

<!-- docs:end -->

## What to look at

**`Where` returns a fresh slice.** Changing it cannot change `tasks`.

**`First` reports absence with comma-ok.** No match is an ordinary answer, not
a failure, so it is not an error.

**`ExactlyOne` tells none from several.** It returns `tmuxq.ErrNoMatch` or
`tmuxq.ErrMultipleMatches`, and `errors.Is` tells them apart.

## Running its test

```console
$ go -C examples test ./any-slice
```

The test compares what the program prints with [`output.txt`](output.txt).
