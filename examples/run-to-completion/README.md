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
1
2
3
stopped by signal 9
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

## Following a command that has not finished

`Run` waits. `Start` does not: it returns a `Running` handle while the command
is still going, so its output can be followed and it can be stopped.

<!-- docs:run-streaming -->

```go
// Start returns while the command is still running, so its output can be
// followed and it can be stopped from another goroutine. The stream begins
// where StreamTo opens it, so this command waits before its first line;
// result.Lines holds the screen either way.
running, err := session.Start(ctx, "sleep 1; seq 1 3; sleep 30", tmux.RunOptions{})
if err != nil {
	return fmt.Errorf("start command: %w", err)
}
stopped := make(chan error, 1)
go func() {
	time.Sleep(2 * time.Second)
	stopped <- running.Kill(ctx)
}()
streamed, err := running.StreamTo(ctx, os.Stdout)
if err != nil {
	return fmt.Errorf("stream command: %w", err)
}
if err := <-stopped; err != nil {
	return fmt.Errorf("stop command: %w", err)
}
fmt.Println("stopped by signal", streamed.Signal)
```

<!-- docs:end -->

**`StreamTo` is the whole loop.** It copies what the command prints into any
writer and returns the same result `Wait` would, so nothing about following a
command needs a goroutine of the caller's. `Kill` may run in one, as it does
here.

**`Kill` is the process group, not the pane.** tmux resolves the pid itself
when the command runs, so killing does not depend on a pid this program read
earlier, and `result.Signal` reports what ended it — as data, not an error.

**The screen is authoritative.** `result.Lines` is a capture and always holds
what the command showed. The stream is what tmux pushed while it ran: it starts
where `StreamTo` opens it, which is why this command waits before its first
line, and tmux can drop a line printed with nothing between it and the
command's exit.

**The streamed bytes are the terminal's.** Each line arrives ending `\r\n`,
because that is what a program writing to a tty produces.

## Testing your own version

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes:

```console
$ go -C examples test ./run-to-completion
```
