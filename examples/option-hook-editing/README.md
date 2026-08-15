# option-hook-editing

Options and hooks as typed accessors rather than strings.

Every tmux option and every hook has a generated accessor with the Go type the
option actually holds, so a wrong value is a compile error instead of a runtime
complaint from tmux.

## Running it

```console
$ go -C examples run ./option-hook-editing
```

```
client-attached hook present: true
```

## What to look at

**Scopes are separate objects.** `session.SetMouse` writes to that session;
`server.GlobalSessionScope()` writes the global value every session inherits.
Which one you hold decides what you changed, so it is a different receiver
rather than a flag.

**A status line is a sparse array.** tmux indexes status format slots, and they
need not be contiguous — this sets 0 and 2 and leaves 1 alone. `NewSparseArray`
carries that shape instead of flattening it into a slice with a hole.

**Hooks read back typed too.** `Hooks()` returns accessors, so
`ClientAttached()` is a method rather than a map lookup on a string that might
be misspelled.

**Present, not just non-empty.** The accessor reports whether the hook is set
separately from what it is set to.

## Testing your own version

```console
$ go -C examples test ./option-hook-editing
```

The test asserts `true`: `false` would mean the write was accepted and the read
did not find it, which is the failure worth catching.
