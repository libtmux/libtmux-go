# quickstart

A whole session, window, and pane lifecycle: make a session, add a window, split
it, send a command into the new pane, and read the pane back until the command's
output appears.

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

**Reading is a poll, not a wait.** `SendKeys` returns as soon as tmux has taken
the keys, which is before the shell has run them. The loop captures the pane
until the text appears rather than sleeping and hoping.

**`Literal: true`** sends the characters as written. Without it tmux interprets
key names, so a command containing them would arrive as something else.

**The pane never refreshes.** The `pane` value is what tmux said when the split
happened. Capturing asks tmux again; it does not update the value in hand.

## Testing your own version

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes:

```console
$ go -C examples test ./quickstart
```
