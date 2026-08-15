# control-mode-subscribe

Watching what a tmux server does, rather than asking it repeatedly.

A control-mode connection is a tmux client that stays open. tmux pushes what
happens down it, so a change is heard once, when it happens, instead of being
discovered by a poll that has to guess how often to ask.

## Running it

```console
$ go -C examples run ./control-mode-subscribe
```

```
notification: %session-changed
notification: %session-renamed
heard the rename
```

The example renames its own session and waits to hear about it.

## What to look at

**The rename happens after the connection is open.** A rename done first would
be history the stream never mentions — tmux pushes what happens next, not what
already did.

**Notifications arrive as an iterator.** `Notifications` yields until the
context ends or the connection closes, and yields an error in the same range, so
a read failure is handled where the notifications are rather than somewhere
else.

**More arrives than you asked for.** `%session-changed` comes first here.
Filtering by `Kind()` is how you pick out the one you are waiting for.

**The connection is a client.** While open it appears in `list-clients` and
counts toward `session_attached`, which is why opening one is a choice rather
than something the library does for you.

## Testing your own version

```console
$ go -C examples test ./control-mode-subscribe
```
