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

```console
$ go -C examples run ./byte-streams
```

```
pasted 42 bytes, compressed the screen into 70
archive: /tmp/libtmux-byte-streams-12345.gz
```

## What to look at

<!-- docs:byte-streams -->

```go
// Given: ctx context.Context; server tmux.Server; pane tmux.Pane;
// payload *strings.Reader; archive string; marker string
// Pasting hands the bytes to the pane's pty; the program reading it echoes
// them back on its own schedule. Watch before pasting, so the wait cannot
// start after the bytes it is waiting for already arrived, and capture only
// once they are on the screen -- a capture taken straight after the paste
// archives whatever the pane happened to be showing, which on a loaded
// machine is still an empty screen.
observation, err := pane.OpenObservation(ctx)
if err != nil {
	return fmt.Errorf("observe pane: %w", err)
}
defer func() { err = errors.Join(err, observation.Close()) }()

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
// The pane announces the bytes as it echoes them, so nothing here has to
// guess how long that takes or re-capture on a timer.
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

## Testing your own version

[`example_test.go`](example_test.go) runs this against a real tmux on a socket
the test harness owns and removes, then decompresses the archive and checks the
payload survived the trip:

```console
$ go -C examples test ./byte-streams
```
