# environment

Session environment variables, and finding the pane a program is running in.

Two halves of the same idea: what tmux stores against a session, and what tmux
tells a process about where it is.

## Running it

```console
$ go -C examples run ./environment
```

```
ready true
```

`ready` is the value set and read back. `true` is the pane rediscovered from a
`TMUX` environment matching the pane the example split.

## What to look at

**Present is not the same as empty.** `GetEnvironment` reports whether the
variable is there separately from its value, because tmux distinguishes a
variable set to nothing from one that was never set.

**`PaneFromEnv` is how a program finds itself.** tmux gives every process it
starts `TMUX` and `TMUX_PANE`. Handing those to `PaneFromEnv` returns the pane
the program is running in, without being told which one that is.

**That is why the harness scrubs them.** A test inheriting `TMUX` from the
terminal it was launched in would find the developer's pane instead of its own,
so `tmuxtest` removes those variables from the environment it hands tmux.

## Testing your own version

```console
$ go -C examples test ./environment
```

The test asserts both halves in the one line the example prints: either alone
would pass while the other was broken.
