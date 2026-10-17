# control-mode-subscribe

Watching what a tmux server does, rather than asking it repeatedly.

A control-mode connection is a tmux client that stays open. tmux pushes what
happens down it, so a change is heard once, when it happens, instead of being
discovered by a poll that has to guess how often to ask.

The notification stream supports the same tmux 3.3a through 3.8 range as the
ordinary process API.

## Running it

```console
$ go -C examples run ./control-mode-subscribe
```

It starts a tmux server of its own, on a private socket with an empty
config, and prints:

<!-- docs:output examples/control-mode-subscribe/output.txt -->

```text
notification: %session-changed
notification: %session-renamed
heard the rename
session has 2 windows
```

<!-- docs:end -->

The example renames its own session and waits to hear about it, then asks tmux
to report the session's window count and opens a window.

## Hearing what tmux does

`Session.OpenNotifications` and `Server.OpenNotifications` return owned
streams. Zero options keep tmux's changes and leave pane output out; set
`IncludePaneOutput` when watching what a pane prints. Before tmux 3.6,
destroying the attached session follows its `detach-on-destroy` policy and may
end the stream.

<!-- docs:watching -->

```go
// Given: ctx context.Context; session tmux.Session
stream, err := session.OpenNotifications(ctx, tmux.NotificationOptions{})
if err != nil {
	return fmt.Errorf("open notification stream: %w", err)
}
defer func() { err = errors.Join(err, stream.Close()) }()

// Rename after subscribing; notifications do not include earlier changes.
if _, err := session.Rename(ctx, "control-example"); err != nil {
	return fmt.Errorf("rename session: %w", err)
}

for {
	notification, err := stream.Next(ctx)
	if err != nil {
		return fmt.Errorf("read notification: %w", err)
	}
	fmt.Printf("notification: %s\n", notification.Kind())
	if notification.Kind() == tmux.ControlNotificationSessionRenamed {
		fmt.Println("heard the rename")
		break
	}
}
```

<!-- docs:end -->

## Asking tmux to watch a value

A subscription asks tmux to evaluate a format and report it when its value
changes, so a program hears about a state it cares about, such as a window
count or a pane's current command, without asking again:

<!-- docs:subscribing -->

```go
// Given: ctx context.Context; session tmux.Session;
// stream *tmux.NotificationStream
// A subscription is a format tmux evaluates for you: it reports the value
// when it first looks, about a second later, and then each time it changes.
if err := stream.Subscribe(ctx, tmux.SubscriptionRequest{
	Name: "windows", Format: "#{session_windows}",
}); err != nil {
	return fmt.Errorf("subscribe: %w", err)
}
if _, err := session.NewWindow(ctx, tmux.NewWindowRequest{}); err != nil {
	return fmt.Errorf("open window: %w", err)
}
for {
	notification, err := stream.Next(ctx)
	if err != nil {
		return fmt.Errorf("read notification: %w", err)
	}
	if change, ok := notification.Subscription(); ok && change.Value == "2" {
		fmt.Println("session has", change.Value, "windows")
		return nil
	}
}
```

<!-- docs:end -->

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

## Running its test

```console
$ go -C examples test ./control-mode-subscribe
```
