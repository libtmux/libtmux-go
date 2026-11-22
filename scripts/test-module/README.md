# test-module

Runs one module's `go test`, leaving out the `Example` functions that print a
feature the tmux release lacks. An `Example` has no `t.Skip`, so
`go test -skip` names it instead.

The `tests` workflow runs it for each module and each tmux lane, and
[`matrix`](../matrix) runs it for each build in a matrix. The tmux that decides
is `$LIBTMUX_TEST_TMUX`, else `$LIBTMUX_TMUX`, else `tmux` on `PATH`; an empty
or whitespace-only value counts as unset.

Modules are the directories in the `use` block of `go.work`. A tmux older than
3.7 omits the control examples, and the comparison is by release number, so
`3.5a` is older and `3.10` is not. Flags after the module go to `go test`.

`tmux -V` has a 10 s limit (`bounded.PROBE`), so a hung binary fails the run
with the cannot-read-the-version message.

Test the `workspace` module with the race detector:

```console
$ uv run scripts/test-module/run_tests.py workspace -race -count=1
```

Run its tests:

```console
$ uv run --project scripts pytest scripts/test-module
```
