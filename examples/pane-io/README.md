# pane-io

Type a command into a pane and wait for its answer, then read what the pane
prints next as a stream.

A pane is an `io.Writer`, and what it prints is an `io.Reader`. What works on a
file or a socket works on a pane:

- `fmt.Fprintln` submits a line.
- `bufio.Scanner` reads lines back.
- `io.Copy` mirrors a pane to stdout.

## Running it

This example needs tmux 3.7 or newer for its pane observation.

```console
$ go -C examples run ./pane-io
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/pane-io/output.txt -->

```text
the pane answered 6 x 7 = 42
read the line: streamed 2 lines
```

<!-- docs:end -->

## Type, then wait for the answer

<!-- docs:pane-wait -->

```go
// Given: ctx context.Context; pane tmux.Pane
// Open the observation before typing, so no output is missed.
output, err := pane.OpenObservation(ctx)
if err != nil {
	return fmt.Errorf("observe pane: %w", err)
}
defer func() { err = errors.Join(err, output.Close()) }()

// A pane is an io.Writer, and a newline is the Enter key.
command := `echo "6 x 7 = $((6 * 7))"`
if _, err := fmt.Fprintln(pane.Writer(ctx), command); err != nil {
	return fmt.Errorf("type command: %w", err)
}
// The terminal echoes what is typed, so wait for the answer: the typed
// line says "$((6 * 7))" and never "42".
if _, err := output.WaitFor(ctx, func(text string) bool {
	return strings.Contains(text, "6 x 7 = 42")
}); err != nil {
	return fmt.Errorf("wait for answer: %w", err)
}
fmt.Println("the pane answered 6 x 7 = 42")
```

<!-- docs:end -->

**Open the observation first.** It starts at the moment it is opened; output
before that is the pane's history, not the stream. Typing after opening means
the answer cannot be missed.

**A newline is the Enter key.** `Fprintln` types the text and presses Enter.
Text without a trailing newline is typed and left on the line.

**Wait for what the command prints, not what you typed.** The terminal echoes
the typed line, which is pane output, so a wait for `6 x 7` would return before
the command ran. The answer, `42`, appears only once the shell computes it.

**`WaitFor` is pushed, not polled.** tmux reports each write as it happens, and
`WaitFor` calls the match function with everything the pane has written since
the observation opened.

**`WaitForText` polls, on every release.** It reads the visible screen until it
holds the text, so it needs no observation:

<!-- docs:pane-wait-text -->

```go
// Given: ctx context.Context; pane tmux.Pane
// A pane is an io.Writer, and a newline is the Enter key.
command := `echo "6 x 7 = $((6 * 7))"`
if _, err := fmt.Fprintln(pane.Writer(ctx), command); err != nil {
	return fmt.Errorf("type command: %w", err)
}
// The screen already holds the typed line, which says "$((6 * 7))"
// and never "42", so the answer is what the wait names.
if _, err := pane.WaitForText(ctx, tmux.WaitForTextRequest{
	Text: "6 x 7 = 42",
}); err != nil {
	return fmt.Errorf("wait for answer: %w", err)
}
fmt.Println("the pane answered 6 x 7 = 42")
```

<!-- docs:end -->

## Read the pane as a stream

<!-- docs:pane-io -->

```go
// Given: ctx context.Context; pane tmux.Pane; output *tmux.PaneObservation
// The observation is also an io.Reader over what the pane prints next, so
// whatever reads a stream reads a pane.
next := `echo "streamed $((1 + 1)) lines"`
if _, err := fmt.Fprintln(pane.Writer(ctx), next); err != nil {
	return fmt.Errorf("type command: %w", err)
}
scanner := bufio.NewScanner(output.Reader(ctx))
for scanner.Scan() {
	// These are the terminal's bytes: the echo comes first, and a prompt
	// can share the reply's line. Match how the reply ends.
	if strings.HasSuffix(scanner.Text(), "streamed 2 lines") {
		fmt.Println("read the line: streamed 2 lines")
		return nil
	}
}
return fmt.Errorf("read pane: %w", scanner.Err())
```

<!-- docs:end -->

**The bytes are the terminal's.** Escape sequences and carriage returns arrive
as the program wrote them. `bufio.Scanner` drops the `\r` of a `\r\n` line
ending; anything else is yours to interpret.

**A prompt can share the reply's line.** A shell still starting prints its
first prompt after the command is typed, so the reply arrives as
`$ streamed 2 lines`. The loop matches how the line ends.

## Running its test

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes, and compares what it prints with
[`output.txt`](output.txt):

```console
$ go -C examples test ./pane-io
```
