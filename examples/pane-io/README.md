# pane-io

A pane as a pair of streams: type into it through an `io.Writer`, read what it
prints back through an `io.Reader`.

Both are the standard interfaces, so everything that already works on a file or
a socket works on a pane — `fmt.Fprintln` to submit a line, `bufio.Scanner` to
read lines back, `io.Copy` to mirror a pane to stdout, `regexp.MatchReader` to
wait for a pattern.

## Running it

```console
$ go -C examples run ./pane-io
```

```
heard ready
```

## What to look at

<!-- docs:pane-io -->

```go
// Open the reader before typing, so nothing the command prints is missed.
output, err := pane.OpenObservation(ctx)
if err != nil {
	return fmt.Errorf("observe pane: %w", err)
}
defer func() { err = errors.Join(err, output.Close()) }()

if _, err := fmt.Fprintln(pane.Writer(ctx), "printf 'ready\\n'"); err != nil {
	return fmt.Errorf("type command: %w", err)
}
scanner := bufio.NewScanner(output.Reader(ctx))
for scanner.Scan() {
	if scanner.Text() == "ready" {
		fmt.Println("heard ready")
		return nil
	}
}
return fmt.Errorf("read pane: %w", scanner.Err())
```

<!-- docs:end -->

**Open the reader first.** An observation starts at the moment it is opened;
output before that is the pane's history, not the stream. Typing after opening
means the reply cannot be missed.

**A newline is the Enter key.** `Fprintln` types the text and presses Enter,
the way a person would. Text without a trailing newline is typed and left on
the line.

**The terminal echoes.** The command's own text arrives on the reader before
the command's output, because the pty echoed it as it was typed — the same as
on screen. That is why the loop matches a whole line rather than a substring.

**The bytes are the terminal's.** Escape sequences and carriage returns arrive
as the program wrote them. `bufio.Scanner` drops the `\r` of a `\r\n` line
ending; anything else is yours to interpret.

## Testing your own version

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes:

```console
$ go -C examples test ./pane-io
```
