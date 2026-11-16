# run-to-completion

Run a program in a tmux pane, wait for it to finish, and read back its exit
status and what its terminal showed.

This is `os/exec` for a program that needs a terminal. The command runs under
tmux with a tty, so it sees `isatty`, colour, and a width, and what comes back
is the screen tmux rendered rather than the bytes the program wrote.

## Running it

This example needs tmux 3.8 or newer for managed literal programs.

```console
$ go -C examples run ./run-to-completion
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/run-to-completion/output.txt -->

```text
screen: stdout is a terminal
exited 3
stream: live output
stopped: killed
input: hello terminal
```

<!-- docs:end -->

`test -t 1` held, so the command had a terminal, which one run through a pipe
does not. The window the command ran in is removed once the
result is read, and the session is killed on the way out.

## What to look at

<!-- docs:run-to-completion -->

```go
// Given: ctx context.Context; session tmux.Session
// The command runs with a terminal, so test -t holds where a pipe fails.
arguments := []string{"-c", "test -t 1 && echo stdout is a terminal; exit 3"}
result, err := session.RunProgram(ctx, "sh", arguments, tmux.RunOptions{})
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

**tmux signals the exit.** A `pane-died` hook signals a wait-for channel of this
call's own, so `RunProgram` returns when the process exits. A
liveness check stands behind it. No tmux binary is needed on the command's `PATH`.

**`Keep: true` leaves the window in place.** `result.Pane` then names a pane
you can look at, which is what you want when a build fails and the screen is
the evidence.

## Following a command that has not finished

`RunProgram` waits. `StartProgram` returns a `Running` handle while the command
is still going, so its output can be followed and it can be stopped.

<!-- docs:run-streaming -->

```go
// Given: ctx context.Context; session tmux.Session
// Subscribe before triggering output. The screen returned by Wait remains
// the final rendered result; this reader follows terminal bytes.
running, err := session.StartProgram(ctx, "cat", nil, tmux.RunOptions{})
if err != nil {
	return fmt.Errorf("start command: %w", err)
}
observation, err := running.Pane().OpenObservation(ctx)
if err != nil {
	return err
}
defer func() { err = errors.Join(err, observation.Close()) }()
if err := running.SendKeys(ctx, tmux.SendKeysRequest{Command: new("live output"), Literal: true}); err != nil {
	return err
}
line, err := bufio.NewReader(observation.Reader(ctx)).ReadString('\n')
if err != nil {
	return err
}
fmt.Println("stream:", strings.TrimSpace(line))
if err := observation.Close(); err != nil {
	return err
}
if err := running.Kill(ctx); err != nil {
	return fmt.Errorf("stop command: %w", err)
}
streamed, err := running.Wait(ctx)
if err != nil {
	return err
}
fmt.Println("stopped:", streamed.Signal)
```

<!-- docs:end -->

The observation opens before input triggers output. Closing it leaves the
program running; `Kill` dispatches SIGKILL to its process group, and `Wait`
reports the main process's exit. Owned input and destruction consume the
pending generation witness in the same daemon queue group as their effect,
so a respawned replacement is refused. Signal
delivery is asynchronous, and descendants that leave the group are outside
this guarantee.

`result.Lines` captures the final rendered screen and bounded history. The
observation carries terminal bytes received after subscription. Output around
process exit may be missing from the byte stream; neither result reconstructs
history tmux has discarded.

**The streamed bytes are the terminal's.** Each line arrives ending `\r\n`,
because that is what a program writing to a tty produces. The output above
shows them as a terminal draws them.

## Running its test

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes:

```console
$ go -C examples test ./run-to-completion
```
