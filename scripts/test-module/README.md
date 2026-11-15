# test-module

Runs one module's `go test`, leaving out the `Example` functions that print a
feature the tmux release lacks. An `Example` has no `t.Skip`, so
`go test -skip` names it instead.

The `tests` workflow runs it for each module and each tmux lane, and
[`matrix`](../matrix) runs it for each build in a matrix. The tmux that decides
is `$LIBTMUX_TMUX`, else `tmux` on `PATH`.

Modules are `.`, `examples`, `workspace`, `mcp`, `benchmarks` and
`internal/tools`. Flags after the module go to `go test`.

Test the `workspace` module with the race detector:

```console
$ uv run scripts/test-module/run_tests.py workspace -race -count=1
```

Run its tests:

```console
$ uv run pytest scripts/test-module
```
