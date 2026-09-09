# run-to-completion

Run a program in a tmux pane, wait for it to finish, and read back its exit
status and what its terminal showed.

This is `os/exec` for a program that needs a terminal. The command runs under
tmux with a tty, so it sees `isatty`, colour, and a width, and what comes back
is the screen tmux rendered rather than the bytes the program wrote.

## Running it

```console
$ go -C examples run ./run-to-completion
```

```
screen: /dev/pts/3
exited 3
```

`tty` printed the pane's terminal, which a command run through a pipe could not
have done. The window the command ran in is removed once the result is read,
and the session is killed on the way out.

## What to look at

<!-- docs:run-to-completion -->

```go
result, err := session.Run(ctx, "tty; exit 3", tmux.RunOptions{})
if err != nil {
	return fmt.Errorf("run command: %w", err)
}
for _, line := range result.Lines {
	fmt.Println("screen:", line)
}
fmt.Println("exited", result.Status)
```

<!-- docs:end -->

**A nonzero status is a result, not an error.** `err` reports what tmux could
not do — start the window, wait, read the screen. What the command did is in
`result.Status`, the way `exec.ExitError` carries it.

**Nothing polls.** tmux runs a `pane-died` hook inside the server that signals
a wait-for channel of this call's own, so `Run` returns when the process exits,
not on the next tick of a loop. Nothing needs a tmux binary on the command's
`PATH`.

**`Keep: true` leaves the window in place.** `result.Pane` then names a pane
you can look at, which is what you want when a build fails and the screen is
the evidence.

## Testing your own version

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes:

```console
$ go -C examples test ./run-to-completion
```
