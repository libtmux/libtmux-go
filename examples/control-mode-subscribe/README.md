# control-mode-subscribe

Watching what a tmux server does, rather than asking it repeatedly.

A control-mode connection is a tmux client that stays open. tmux pushes what
happens down it, so a change is heard once, when it happens, instead of being
discovered by a poll that has to guess how often to ask.

The notification stream supports the same tmux 3.2a-through-3.7c range as the
ordinary process API.

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

**Notifications arrive in wire order.** `Next` waits until tmux sends the next
notification, the context ends, or the stream closes. A read failure is handled
at the exact read that observed it.

**More arrives than you asked for.** `%session-changed` comes first here.
Filtering by `Kind()` is how you pick out the one you are waiting for.

**The stream owns a client.** While open it appears in `list-clients` and counts
toward `session_attached`, which is why opening one is a choice rather than
something the library does for you.

## Testing your own version

```console
$ go -C examples test ./control-mode-subscribe
```
