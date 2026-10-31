# byte-streams

Bytes into tmux and back out through the standard interfaces: a payload read
from an `io.Reader`, and a scrollback written to an `io.Writer`.

Typing a payload has two problems this avoids. A shell reads what it is sent, so
`$HOME`, quotes and backslashes mean something on the way in; and keys cross
tmux's command channel one line at a time, which caps a paste at tmux's
16 KiB command limit and cannot carry a NUL at all. A buffer loaded from a
reader has neither limit: tmux reads it as a stream, so the bytes arrive exactly
as written, at any size.

Coming back out, `CaptureTo` writes the screen to any writer — here a
`gzip.Writer` onto a file, with no intermediate copy of the scrollback in memory
and no temporary file to clean up.

## Running it

This example needs tmux 3.7 or newer for its pane observation.

```console
$ go -C examples run ./byte-streams
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/byte-streams/output.txt -->

```text
pasted 42 bytes
archived the pane's scrollback as gzip
```

<!-- docs:end -->

It also names the archive it wrote on standard error, a path that differs on
every run.

## What to look at

### In, from an `io.Reader`

<!-- docs:byte-streams-in -->

```go
// Given: ctx context.Context; server tmux.Server; pane tmux.Pane;
// payload *strings.Reader
// tmux reads the buffer from the reader as a stream, so no quoting rule
// applies to the payload and no command line has to hold it.
name := "payload"
if err := server.LoadBufferFrom(ctx, payload, tmux.LoadBufferFromOptions{
	Name: &name,
}); err != nil {
	return fmt.Errorf("load payload: %w", err)
}
if err := pane.PasteBuffer(ctx, tmux.PasteBufferRequest{
	BufferName: &name, DeleteAfter: true,
}); err != nil {
	return fmt.Errorf("paste payload: %w", err)
}
```

<!-- docs:end -->

### Out, to an `io.Writer`

The program opens an observation on the pane before it pastes, so the wait
below cannot start after the bytes it is waiting for have already arrived:

<!-- docs:byte-streams-out -->

```go
// Given: ctx context.Context; pane tmux.Pane;
// observation *tmux.PaneObservation; archive string; marker string
// Capture only once the bytes are on the screen. A capture taken straight
// after the paste archives whatever the pane happened to be showing, which
// on a loaded machine is still an empty screen. The pane announces the
// bytes as it echoes them, so nothing here guesses how long that takes.
seen := strings.Builder{}
for _, line := range observation.Baseline() {
	seen.WriteString(line)
}
for !strings.Contains(seen.String(), marker) {
	notification, err := observation.NextNotification(ctx)
	if err != nil {
		return fmt.Errorf("follow pane output: %w", err)
	}
	if _, output, ok := notification.Output(); ok {
		seen.Write(output)
	}
}

file, err := os.Create(archive)
if err != nil {
	return fmt.Errorf("create archive: %w", err)
}
defer func() { err = errors.Join(err, file.Close()) }()
compressor := gzip.NewWriter(file)
if err := pane.CaptureTo(ctx, compressor, tmux.CapturePaneRequest{
	Start: tmux.CaptureBoundary, End: tmux.CaptureBoundary,
}); err != nil {
	return fmt.Errorf("capture scrollback: %w", err)
}
if err := compressor.Close(); err != nil {
	return fmt.Errorf("finish archive: %w", err)
}
```

<!-- docs:end -->

**The payload is never an argument.** `LoadBufferFrom` hands tmux a stream, so
no quoting rule applies and nothing is bounded by how long a command line may
be. `Server.SetBuffer` is the other way to fill a buffer and is bounded by
both.

**`DeleteAfter` keeps the buffer list clean.** tmux keeps a buffer until
something drops it, and the most recent buffer is what a person's own paste key
reaches.

**The archive path carries the process id.** Two runs at once would otherwise
write the same file. The archive is the example's output, so it stays for you to
read with `gzip -dc`; each run leaves one, and nothing removes it.

**A capture is a point in time.** `CaptureTo` writes the screen as it is when
the call runs. Use `CaptureBoundary` for both ends to take the whole history
rather than the visible rows.

**These need a tmux process.** tmux gives a control client no stdin or stdout,
so a connection-bound value refuses; `Server.LoadBuffer`,
`Server.SaveBuffer` and `Pane.CaptureToFile` take a path and work over a
connection.

## Running its test

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes, then decompresses the archive and checks the
payload survived the trip:

```console
$ go -C examples test ./byte-streams
```
