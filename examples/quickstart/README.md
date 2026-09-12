# quickstart

A whole session, window, and pane lifecycle: make a session, add a window, split
it, type a command into the new pane, and read the pane's output back until the
command's line arrives.

Start here. It is the shortest program that touches every level of the
hierarchy — server, session, window, pane — and the only one that both writes to
a pane and reads from it.

## Running it

```console
$ go -C examples run ./quickstart
```

```
libtmux ready
```

`libtmux ready` is the pane's own output, captured back out of it. The session
is killed on the way out, so nothing is left behind.

## What to look at

**Writing and reading are the standard interfaces.** `pane.Writer(ctx)` is an
`io.Writer` whose newline is the Enter key, so `fmt.Fprintln` submits a line
the way a person would. `output.Reader(ctx)` is an `io.Reader` over what the
pane prints after the observation opened, so `bufio.Scanner` reads it back a
line at a time and the program returns the moment the line arrives — no loop
that captures and sleeps.

**Open the reader before typing.** An observation starts when it is opened;
typing afterwards means the reply cannot be missed.

**The split runs `sh`.** A pane's default shell is your login shell, and what
that does at startup is yours. A plain POSIX shell keeps the example about tmux.

**`new("work")`** is Go 1.26's way to take the address of a literal. Request
fields are pointers only where tmux distinguishes an empty value from an
omitted one; the rest are plain values.

**The pane never refreshes.** The `pane` value is what tmux said when the split
happened. Reading through the observation asks tmux for more; it does not
update the value in hand.

## Testing your own version

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes:

```console
$ go -C examples test ./quickstart
```
